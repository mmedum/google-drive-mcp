package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// The Claude Desktop bundle. A `.mcpb` is a zip carrying a manifest, the
// binaries for every platform it claims, and the licence and README —
// opened in Claude Desktop it installs the server and asks for the OAuth
// client JSON, so nobody has to edit a config file by hand.
//
// mcpbPlaceholder is the version the COMMITTED manifest carries. The
// real one is written in as the bundle is packed, so a manifest in the
// tree can never be stale: it does not claim a version at all.
const mcpbPlaceholder = "0.0.0-dev"

// mcpbCLI is the official packer, pinned exactly like every other tool
// the release fetches. It validates the manifest against the published
// schema before it writes, which is the reason to shell out to it rather
// than zip the directory here: re-implementing that validation in Go
// would mean trusting our copy of a schema we do not own.
//
// This is the one place anything outside the Go toolchain runs, and it
// runs at RELEASE time only — nothing from it enters the repository, the
// module graph or the binary. Recorded as a deviation in §17b.
const mcpbCLI = "@anthropic-ai/mcpb@2.1.2"

// mcpbManifest writes the packaging manifest with a real version in it.
//
// Through a decode and an encode rather than a text substitution,
// because the manifest is JSON and a sed over JSON is how a quote ends
// up inside a string. It refuses a manifest that is not carrying the
// placeholder, so a real version committed by accident fails the build
// instead of shipping under the wrong number.
func mcpbManifest(out io.Writer, args []string) error {
	version := arg(args, 0, "")
	path := arg(args, 1, "packaging/mcpb/manifest.json")
	if version == "" {
		return fmt.Errorf("usage: gates mcpb-manifest VERSION [MANIFEST]")
	}
	raw, err := os.ReadFile(path) //nolint:gosec // a path this repository owns
	if err != nil {
		return err
	}
	// A map rather than a struct: this rewrites one field and must not
	// drop the rest, and the manifest schema is not ours to model.
	var manifest map[string]any
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	if got, _ := manifest["version"].(string); got != mcpbPlaceholder {
		return fmt.Errorf("%s carries version %q; the committed manifest must carry %q, "+
			"so that a version in the tree can never be a stale one", path, got, mcpbPlaceholder)
	}
	manifest["version"] = strings.TrimPrefix(version, "v")

	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(manifest)
}

// mcpbPack builds the bundle from the binaries goreleaser has just made.
//
// It runs as the universal binary's post hook, which is the one point in
// the pipeline where every binary exists and the checksum file has not
// been written yet. That is what puts the bundle into checksums.txt with
// the archives, under the same signature. Anywhere later and it ships
// unsigned.
func mcpbPack(out io.Writer, args []string) error {
	version := arg(args, 0, "")
	dist := arg(args, 1, "dist")
	if version == "" {
		return fmt.Errorf("usage: gates mcpb-pack VERSION [DIST]")
	}
	version = strings.TrimPrefix(version, "v")

	// A manifest picks a binary by platform and has no key for the
	// architecture, so every platform it claims has to work on both.
	// macOS does through the universal binary and Windows through amd64,
	// which its arm64 build runs under emulation. Linux has neither, so
	// the bundle carries both Linux binaries and a launcher.
	staged := []struct{ glob, as, what string }{
		{"*darwin_all*/google-drive-mcp", "google-drive-mcp", "darwin universal binary"},
		{"*windows_amd64*/google-drive-mcp.exe", "google-drive-mcp.exe", "windows amd64 binary"},
		{"*linux_amd64*/google-drive-mcp", "google-drive-mcp-amd64", "linux amd64 binary"},
		{"*linux_arm64*/google-drive-mcp", "google-drive-mcp-arm64", "linux arm64 binary"},
	}

	stage, err := os.MkdirTemp("", "mcpb-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	server := filepath.Join(stage, "server")
	if err := os.MkdirAll(server, 0o755); err != nil {
		return err
	}

	for _, s := range staged {
		src, err := onlyMatch(filepath.Join(dist, s.glob), s.what)
		if err != nil {
			return err
		}
		// 0755 on all of them: the packer forces the execute bit on the
		// entry point alone and copies the filesystem mode for the rest,
		// so the Windows binary would arrive unrunnable otherwise.
		if err := copyFile(src, filepath.Join(server, s.as), 0o755); err != nil {
			return err
		}
	}
	if err := copyFile("packaging/mcpb/linux-launch.sh", filepath.Join(server, "linux-launch.sh"), 0o755); err != nil {
		return err
	}
	for _, f := range []string{"LICENSE", "README.md"} {
		if err := copyFile(f, filepath.Join(stage, filepath.Base(f)), 0o644); err != nil {
			return err
		}
	}

	manifest, err := os.Create(filepath.Join(stage, "manifest.json")) //nolint:gosec // a temporary directory this function made
	if err != nil {
		return err
	}
	if err := mcpbManifest(manifest, []string{version, "packaging/mcpb/manifest.json"}); err != nil {
		_ = manifest.Close()
		return err
	}
	if err := manifest.Close(); err != nil {
		return err
	}

	bundle := filepath.Join(dist, fmt.Sprintf("google-drive-mcp_%s.mcpb", version))
	cmd := exec.Command("npx", "--yes", mcpbCLI, "pack", stage, bundle) //nolint:gosec // both arguments are built here
	cmd.Stdout = out
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("mcpb pack: %w", err)
	}
	if _, err := os.Stat(bundle); err != nil {
		return fmt.Errorf("%s was not written: %w", bundle, err)
	}
	_, _ = fmt.Fprintf(out, "mcpb-pack: wrote %s\n", bundle)
	return nil
}

// onlyMatch resolves a glob that must name exactly one file.
//
// The layout under dist/ carries a build id and an amd64 variant, so a
// glob that quietly matched two would pack whichever sorted first — and
// a bundle built from the wrong binary is not something a checksum
// catches, because the checksum is of what was built.
func onlyMatch(pattern, what string) (string, error) {
	found, err := filepath.Glob(pattern)
	if err != nil {
		return "", err
	}
	if len(found) != 1 {
		return "", fmt.Errorf("expected exactly one %s matching %s, found %d: %s",
			what, pattern, len(found), strings.Join(found, " "))
	}
	return found[0], nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src) //nolint:gosec // paths this repository owns or built
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode) //nolint:gosec // a temporary directory this process made
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
