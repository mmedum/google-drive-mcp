package main

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// The Claude Desktop bundle. A `.mcpb` is a zip carrying a manifest, the
// binaries for every platform it claims, and the licence and README —
// opened in Claude Desktop it installs the server and asks for the OAuth
// client JSON, so nobody has to edit a config file by hand.
//
// It is packed here rather than by the official Node CLI, because this
// repository is Go and only Go: an interpreter `make check` needs is a
// prerequisite nobody declared. A bundle is a deflate zip and Go writes
// those, so the only thing the CLI was really buying was validation of
// the manifest against its published schema.
//
// checkManifest below replaces that, and is not merely a substitute. A
// schema can say the manifest is well formed; it cannot say that
// entry_point names a file that is actually in the bundle, or that a
// platform override points at something that was staged, or that
// ${user_config.x} refers to a key that exists. Those are the failures
// that produce a bundle which packs cleanly and then does nothing when a
// person opens it, and they are checkable only against the staged tree.

// mcpbPlaceholder is the version the COMMITTED manifest carries. The
// real one is written in as the bundle is packed, so a manifest in the
// tree can never be stale: it does not claim a version at all.
const mcpbPlaceholder = "0.0.0-dev"

// mcpbManifestPath is the committed manifest, which both the gate and
// the packer read: one file, so a check of it is a check of what ships.
const mcpbManifestPath = "packaging/mcpb/manifest.json"

// mcpbManifest prints the packaging manifest with a real version in it.
//
// Through a decode and an encode rather than a text substitution,
// because the manifest is JSON and a sed over JSON is how a quote ends
// up inside a string. It refuses a manifest that is not carrying the
// placeholder, so a real version committed by accident fails the build
// instead of shipping under the wrong number.
func mcpbManifest(out io.Writer, args []string) error {
	version := arg(args, 0, "")
	path := arg(args, 1, mcpbManifestPath)
	if version == "" {
		return fmt.Errorf("usage: gates mcpb-manifest VERSION [MANIFEST]")
	}
	manifest, err := readCommittedManifest(path)
	if err != nil {
		return err
	}
	manifest["version"] = strings.TrimPrefix(version, "v")

	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(manifest)
}

// readCommittedManifest reads a manifest and refuses one that is not
// carrying the placeholder.
//
// One function because there were three copies of the check, and they
// had already drifted: two shared the long explanation and the third
// gave a bare one, so the same mistake read differently depending on
// which entry point somebody hit.
func readCommittedManifest(path string) (map[string]any, error) {
	manifest, err := readManifest(path)
	if err != nil {
		return nil, err
	}
	if got, _ := manifest["version"].(string); got != mcpbPlaceholder {
		return nil, fmt.Errorf("%s carries version %q; the committed manifest must carry %q, "+
			"so that a version in the tree can never be a stale one", path, got, mcpbPlaceholder)
	}
	return manifest, nil
}

// readManifest decodes into a map rather than a struct: the packer
// rewrites one field and must not drop the rest, and the manifest schema
// is not this repository's to model.
func readManifest(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // a path this repository owns
	if err != nil {
		return nil, err
	}
	var manifest map[string]any
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	return manifest, nil
}

// staged is one file going into the bundle: where to find it in the
// build output, and what it is called inside. The mode is decided in
// writeBundle from the name, because everything under server/ needs the
// execute bit and nothing else does.
type staged struct {
	glob, as, what string
	// platform is the manifest platform this file is the entry point
	// for, or empty for one that is carried but never spawned.
	platform string
}

// binaries are the four built files the bundle carries.
//
// A manifest picks a binary by platform and has no key for the
// architecture, so every platform it claims has to work on both. macOS
// does through the universal binary and Windows through amd64, which its
// arm64 build runs under emulation. Linux has neither, so the bundle
// carries both Linux binaries and a launcher that picks between them.
var binaries = []staged{
	{"*darwin_all*/google-drive-mcp", "server/google-drive-mcp", "darwin universal binary", "darwin"},
	{"*windows_amd64*/google-drive-mcp.exe", "server/google-drive-mcp.exe", "windows amd64 binary", "win32"},
	// Neither Linux binary is an entry point: the launcher below is, and
	// it picks between these two from `uname -m`.
	{"*linux_amd64*/google-drive-mcp", "server/google-drive-mcp-amd64", "linux amd64 binary", ""},
	{"*linux_arm64*/google-drive-mcp", "server/google-drive-mcp-arm64", "linux arm64 binary", ""},
}

// linuxLauncher is the Linux entry point, and the only staged file that
// is not a binary. It is here rather than in alongside because it is
// spawned, and because the two names it chooses between have to match
// what the packer stages — see checkLauncher.
const linuxLauncher = "server/linux-launch.sh"

// alongside are the files that come from the tree rather than from the
// build, so they are the same on every platform and at any moment.
var alongside = map[string]string{
	linuxLauncher: "packaging/mcpb/linux-launch.sh",
	"LICENSE":     "LICENSE",
	"README.md":   "README.md",
}

// stagedNames is every name the bundle will carry, with no build.
//
// The names are static and the FILES are not, which is the whole reason
// this is a function of its own: everything checkManifest asks is
// referential — does entry_point name something that will be there — and
// a referential question needs the names alone. So `gates mcpb` answers
// it on every commit, and a manifest that names a file nobody stages
// fails the day it is written rather than on release day.
func stagedNames() map[string]string {
	out := make(map[string]string, len(binaries)+len(alongside))
	for _, b := range binaries {
		out[b.as] = ""
	}
	for name, src := range alongside {
		out[name] = src
	}
	return out
}

// mcpbCheck holds the COMMITTED manifest to the tree, with no build.
//
// It is the half of the packer's validation that does not need binaries,
// and it is a gate rather than a release step for that reason: a
// manifest naming a file nobody will stage is a fact about two files in
// the repository, and waiting for a tagged release to learn it is
// waiting for the most expensive moment there is.
func mcpbCheck(out io.Writer, _ []string) error {
	manifest, err := readCommittedManifest(mcpbManifestPath)
	if err != nil {
		return err
	}
	contents := stagedNames()
	problems := checkManifest(manifest, contents)
	problems = append(problems, checkLauncher()...)
	if len(problems) > 0 {
		for _, p := range problems {
			_, _ = fmt.Fprintln(out, "  "+p)
		}
		return fmt.Errorf("%d problem(s) in %s", len(problems), mcpbManifestPath)
	}
	_, _ = fmt.Fprintf(out, "mcpb manifest ok (%d files staged, %d platforms)\n",
		len(contents)+1, len(manifestPlatforms(manifest)))
	return nil
}

// mcpbPack builds the bundle from the binaries goreleaser has just made.
//
// It runs as the universal binary's post hook, which is the one point in
// the pipeline where every binary exists and the checksum file has not
// been written yet. That is what MAKES it possible for the bundle to be
// in checksums.txt, and therefore under the signature, since the
// signature is over that file — but it is not what puts it there.
// goreleaser hashes the artifacts IT built, and a file a hook drops into
// dist/ is not one of those: `checksum.extra_files` in .goreleaser.yaml
// is what covers it, and `release.extra_files` is what uploads it. A
// sibling repository following this hook alone produced a bundle that
// packed, agreed about its version everywhere it was asked, and was
// absent from checksums.txt — which looks exactly like a correct build.
func mcpbPack(out io.Writer, args []string) error {
	version := arg(args, 0, "")
	dist := arg(args, 1, "dist")
	if version == "" {
		return fmt.Errorf("usage: gates mcpb-pack VERSION [DIST]")
	}
	version = strings.TrimPrefix(version, "v")

	contents := stagedNames()
	for _, b := range binaries {
		src, err := onlyMatch(filepath.Join(dist, b.glob), b.what)
		if err != nil {
			return err
		}
		contents[b.as] = src
	}

	manifest, err := readCommittedManifest(mcpbManifestPath)
	if err != nil {
		return err
	}
	manifest["version"] = version
	problems := checkManifest(manifest, contents)
	problems = append(problems, checkLauncher()...)
	if len(problems) > 0 {
		for _, p := range problems {
			_, _ = fmt.Fprintln(out, "  "+p)
		}
		return fmt.Errorf("%d problem(s) in the bundle manifest", len(problems))
	}

	bundle := filepath.Join(dist, fmt.Sprintf("google-drive-mcp_%s.mcpb", version))
	if err := writeBundle(bundle, manifest, contents); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "mcpb-pack: wrote %s (%d files)\n", bundle, len(contents)+1)
	return nil
}

// zipEpoch is the timestamp every entry carries.
//
// Fixed rather than the source file's time, so the same inputs give a
// byte-identical archive — goreleaser already stamps the binaries with
// the commit's time for the same reason. It is the earliest a zip can
// represent: leaving it unset writes zeroes, which display as the
// impossible "1980-00-00" and are a malformed date rather than an
// absent one.
var zipEpoch = time.Date(1980, time.January, 1, 0, 0, 0, 0, time.UTC)

// writeBundle writes the zip. Names are sorted so the same inputs give
// the same archive, which is the least a build can offer somebody
// checking a checksum.
func writeBundle(bundle string, manifest map[string]any, contents map[string]string) error {
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.Create(bundle) //nolint:gosec // a path built from dist and the version
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	zw := zip.NewWriter(f)

	if err := addBytes(zw, "manifest.json", append(encoded, '\n'), 0o644); err != nil {
		return err
	}
	for _, name := range sorted(contents) {
		mode := os.FileMode(0o644)
		if strings.HasPrefix(name, "server/") {
			mode = 0o755
		}
		if err := addFile(zw, name, contents[name], mode); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return f.Close()
}

func addBytes(zw *zip.Writer, name string, body []byte, mode os.FileMode) error {
	h := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: zipEpoch}
	h.SetMode(mode)
	w, err := zw.CreateHeader(h)
	if err != nil {
		return err
	}
	_, err = w.Write(body)
	return err
}

func addFile(zw *zip.Writer, name, src string, mode os.FileMode) error {
	in, err := os.Open(src) //nolint:gosec // paths this repository owns or resolved from dist
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	h := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: zipEpoch}
	h.SetMode(mode)
	w, err := zw.CreateHeader(h)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, in)
	return err
}

// checkManifest holds the manifest to the bundle actually being built.
//
// The official packer validates against the published schema, which this
// does not do — and in exchange this checks the thing a schema cannot:
// that every path the manifest names is a file that is going in. A
// manifest whose entry_point points at a binary nobody staged is well
// formed by any schema and produces a bundle that installs and then does
// nothing.
func checkManifest(manifest map[string]any, contents map[string]string) []string {
	var problems []string
	for _, key := range []string{"$schema", "manifest_version", "name", "version", "description", "author", "server"} {
		if manifest[key] == nil {
			problems = append(problems, "manifest has no "+key)
		}
	}
	server, _ := manifest["server"].(map[string]any)
	if server == nil {
		return append(problems, "manifest has no server block")
	}
	if kind, _ := server["type"].(string); kind != "binary" {
		problems = append(problems, fmt.Sprintf("server.type is %q, and this bundle ships binaries", kind))
	}

	inBundle := func(where, name string) {
		if name == "" {
			problems = append(problems, where+" is empty")
			return
		}
		if _, ok := contents[name]; !ok {
			problems = append(problems, fmt.Sprintf(
				"%s names %q, which is not one of the files being packed", where, name))
		}
	}
	entry, _ := server["entry_point"].(string)
	inBundle("server.entry_point", entry)

	cfg, _ := server["mcp_config"].(map[string]any)
	if cfg == nil {
		return append(problems, "manifest has no server.mcp_config")
	}
	command, _ := cfg["command"].(string)
	inBundle("server.mcp_config.command", bundlePath(command))

	overrides, _ := cfg["platform_overrides"].(map[string]any)
	claimed := manifestPlatforms(manifest)
	for _, platform := range sorted(overrides) {
		over, _ := overrides[platform].(map[string]any)
		c, _ := over["command"].(string)
		inBundle("platform_overrides."+platform+".command", bundlePath(c))
		// An override for a platform the bundle does not claim is the
		// same class of defect as a command nobody staged: well formed,
		// packs, and reaches nobody. Claude Desktop picks the override
		// by platform, so one for a platform compatibility rules out is
		// a command that can never run.
		if len(claimed) > 0 && !slices.Contains(claimed, platform) {
			problems = append(problems, fmt.Sprintf(
				"platform_overrides.%s is an override for a platform compatibility.platforms does not "+
					"claim (%s), so nothing will ever use it",
				platform, strings.Join(claimed, ", ")))
		}
	}

	// And the direction that matters more, which the check above does
	// not cover: every platform the bundle CLAIMS must spawn the file
	// staged for it. A review found that deleting the win32 override
	// passed — Windows would then have spawned the default command,
	// which is the darwin universal binary, because that file really is
	// in the bundle and inBundle only asks whether a name is staged. A
	// Mach-O binary on Windows is exactly the "packs cleanly and then
	// does nothing" failure this function exists to catch.
	for _, platform := range claimed {
		want := entryPointFor(platform)
		if want == "" {
			problems = append(problems, fmt.Sprintf(
				"compatibility.platforms claims %s and this packer stages no entry point for it",
				platform))
			continue
		}
		got := bundlePath(command)
		if over, ok := overrides[platform].(map[string]any); ok {
			if c, ok := over["command"].(string); ok {
				got = bundlePath(c)
			}
		}
		if got != want {
			problems = append(problems, fmt.Sprintf(
				"on %s the bundle would spawn %q, and the file staged for %s is %q. Give %s a "+
					"platform_overrides entry, or make the default command name %q",
				platform, got, platform, want, platform, want))
		}
	}

	// Every ${user_config.x} the config spends has to be a key somebody
	// is asked for at install, or the server starts without it.
	declared, _ := manifest["user_config"].(map[string]any)
	env, _ := cfg["env"].(map[string]any)
	for _, name := range sorted(env) {
		value, _ := env[name].(string)
		for _, key := range userConfigKeys(value) {
			if _, found := declared[key]; !found {
				problems = append(problems, fmt.Sprintf(
					"env %s spends ${user_config.%s}, which user_config does not declare", name, key))
			}
		}
	}
	return problems
}

// userConfigKeys reads every ${user_config.x} a value spends.
//
// Every one, not the value when it is nothing but a reference. An
// earlier version matched the whole value, so a composed one —
// "${user_config.local_dir}/sub" — skipped the check entirely and the
// server would start with the variable unsubstituted. Found by review,
// with a probe.
func userConfigKeys(value string) []string {
	found := userConfigRef.FindAllStringSubmatch(value, -1)
	out := make([]string, 0, len(found))
	for _, m := range found {
		out = append(out, m[1])
	}
	return out
}

// userConfigRef matches one ${user_config.x} reference anywhere in a
// value.
var userConfigRef = regexp.MustCompile(`\$\{user_config\.([^}]+)\}`)

// bundlePath turns a manifest command into the name it has inside the
// bundle. ${__dirname} is where the bundle was unpacked.
func bundlePath(command string) string {
	return path.Clean(strings.TrimPrefix(strings.TrimPrefix(command, "${__dirname}"), "/"))
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

// manifestPlatforms is what compatibility.platforms claims, in the order
// it claims it. An empty list means the manifest says nothing, which the
// callers treat as "no opinion" rather than as "no platforms": a bundle
// with no compatibility block installs everywhere.
func manifestPlatforms(manifest map[string]any) []string {
	compat, _ := manifest["compatibility"].(map[string]any)
	raw, _ := compat["platforms"].([]any)
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// entryPointFor is the file a platform spawns: its own binary, or the
// launcher where the platform needs one binary per architecture.
func entryPointFor(platform string) string {
	if platform == "linux" {
		return linuxLauncher
	}
	for _, b := range binaries {
		if b.platform == platform {
			return b.as
		}
	}
	return ""
}

// checkLauncher holds the Linux launcher to the names the packer stages.
//
// The launcher picks a binary from `uname -m` and spawns it by name, and
// those names are written out in a shell script that no manifest
// mentions — so checkManifest cannot see them, and a review found that
// renaming a staged Linux binary passed every gate and every test. The
// bundle would then have failed for every Linux user, with the
// launcher's own "missing from the bundle" message, which is the failure
// this file exists to make impossible.
//
// It asserts the names rather than generating the script: a launcher is
// a thing a person should be able to read, and generating it would trade
// a checkable fact for a harder-to-read one.
func checkLauncher() []string {
	source, ok := alongside[linuxLauncher]
	if !ok {
		return []string{"the packer stages no " + linuxLauncher}
	}
	body, err := os.ReadFile(source) //nolint:gosec // a path this repository owns
	if err != nil {
		return []string{"cannot read " + source + ": " + err.Error()}
	}
	var problems []string
	named := 0
	for _, b := range binaries {
		if !strings.HasPrefix(b.as, "server/") || b.platform != "" {
			continue
		}
		name := strings.TrimPrefix(b.as, "server/")
		if !strings.Contains(string(body), name) {
			problems = append(problems, fmt.Sprintf(
				"%s stages %s and %s never names it, so the launcher would spawn something that is "+
					"not in the bundle", linuxLauncher, b.as, source))
			continue
		}
		named++
	}
	if named == 0 && len(problems) == 0 {
		return []string{source + " names none of the staged binaries; this check is not reading what it " +
			"is meant to"}
	}
	return problems
}
