package main

import (
	"archive/zip"
	"cmp"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mmedum/google-drive-mcp/v3/internal/config"
	"github.com/mmedum/google-drive-mcp/v3/internal/credentials"
	"github.com/mmedum/google-drive-mcp/v3/internal/userconfig"
)

// The Claude Desktop bundle. A `.mcpb` is a zip carrying a manifest, the
// binaries for every platform it claims, and the license and README —
// opened in Claude Desktop it installs the server and asks for the OAuth
// client JSON and the other settings a client's configuration carries,
// so nobody has to edit a config file by hand.
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
	{"*darwin_all*/google-drive-mcp", "server/google-drive-mcp-darwin", "darwin universal binary", "darwin"},
	{"*windows_amd64*/google-drive-mcp.exe", "server/google-drive-mcp.exe", "windows amd64 binary", "win32"},
	// Neither Linux binary is an entry point: the launcher below is, and
	// it picks between these two from `uname -m`.
	{"*linux_amd64*/google-drive-mcp", "server/google-drive-mcp-linux-amd64", "linux amd64 binary", ""},
	{"*linux_arm64*/google-drive-mcp", "server/google-drive-mcp-linux-arm64", "linux arm64 binary", ""},
}

// linuxLauncher is the Linux entry point, and the only staged file that
// is not a binary. It is here rather than in alongside because it is
// spawned, and because the two names it chooses between have to match
// what the packer stages — see checkLauncher.
const linuxLauncher = "server/launch-linux.sh"

// alongside are the files that come from the tree rather than from the
// build, so they are the same on every platform and at any moment.
var alongside = map[string]string{
	linuxLauncher: "packaging/mcpb/launch-linux.sh",
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
	problems = append(problems, settingProblems(manifest, serverVars())...)
	problems = append(problems, checkLauncher()...)
	problems = append(problems, checkBuildMatrix()...)
	if len(problems) > 0 {
		for _, p := range problems {
			_, _ = fmt.Fprintln(out, "  "+p)
		}
		return fmt.Errorf("%d problem(s) in %s", len(problems), mcpbManifestPath)
	}
	userConfig, _ := manifest["user_config"].(map[string]any)
	_, _ = fmt.Fprintf(out, "mcpb manifest ok (%d files staged, %d platforms, %d user_config keys)\n",
		len(contents)+1, len(manifestPlatforms(manifest)), len(userConfig))
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
	problems = append(problems, settingProblems(manifest, serverVars())...)
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
	schemaRef, _ := manifest["$schema"].(string)
	formatVersion, _ := manifest["manifest_version"].(string)
	supportURL, _ := manifest["support"].(string)
	problems = append(problems, declarationProblems(schemaRef, formatVersion, supportURL)...)

	// The manifest specification requires privacy_policies of a server
	// that connects to a service processing user data, as this one does
	// to Google, and neither the schema nor `mcpb validate` enforces it.
	if policies, _ := manifest["privacy_policies"].([]any); len(policies) == 0 {
		problems = append(problems, "privacy_policies is empty; the manifest specification requires one for a "+
			"server that connects to a service processing user data, as this one does to Google")
	}
	compat, _ := manifest["compatibility"].(map[string]any)
	if got, _ := compat["claude_desktop"].(string); got != claudeDesktopFloor {
		problems = append(problems, fmt.Sprintf("compatibility.claude_desktop is %q; the family's bundles claim %q",
			got, claudeDesktopFloor))
	}

	// And against the schema the manifest cites, which the declaration
	// rules only name: a document can satisfy every claim about the
	// REFERENCE without satisfying the schema itself.
	if encoded, err := json.Marshal(manifest); err != nil {
		problems = append(problems, "the manifest could not be re-encoded to check against its schema: "+err.Error())
	} else if err := validateDocument(mcpbSchemaFile, "the manifest", encoded); err != nil {
		problems = append(problems, err.Error())
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
// a checkable fact for a harder-to-read one. Its install hint is held to
// go.mod the same way.
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
	goMod, err := os.ReadFile("go.mod")
	if err != nil {
		return append(problems, "cannot read go.mod: "+err.Error())
	}
	return append(problems, installHintProblems(string(body), string(goMod))...)
}

// installHintProblems holds the launcher's install hint, given on an
// architecture the bundle does not carry, to the module go.mod declares.
// The path changes with every major version from v2, so a hint written
// before one names a module that `go install` no longer updates.
func installHintProblems(launcher, goMod string) []string {
	module := moduleLine.FindStringSubmatch(goMod)
	if module == nil {
		return []string{"go.mod names no module"}
	}
	want := "go install " + module[1] + "/cmd/google-drive-mcp@latest"
	hints := installHint.FindAllString(launcher, -1)
	if len(hints) == 0 {
		return []string{"the launcher gives no install hint for an architecture the bundle does not carry"}
	}
	var problems []string
	for _, hint := range hints {
		if hint != want {
			problems = append(problems, fmt.Sprintf("the launcher says %q, and go.mod's module gives %q", hint, want))
		}
	}
	return problems
}

var (
	// moduleLine is go.mod's module directive. Trailing space is allowed
	// because a checkout with Windows line endings ends it with "\r".
	moduleLine  = regexp.MustCompile(`(?m)^module\s+(\S+)\s*$`)
	installHint = regexp.MustCompile(`go install [^\s"]+`)
)

// checkBuildMatrix holds the staged binaries to what goreleaser builds.
//
// The two lists are written in different files by different people
// thinking about different things — `.goreleaser.yaml` decides what
// exists, `binaries` decides what reaches the bundle — and only one
// direction rots loudly. A platform REMOVED from the matrix fails at
// pack time, when onlyMatch finds no file. A platform ADDED to it is
// simply absent from the bundle, silently, for as long as nobody looks.
//
// That is the shape `gates parity` exists for one file over, and this is
// the same argument applied to the release's other pair of lists.
func checkBuildMatrix() []string {
	source, err := os.ReadFile(".goreleaser.yaml")
	if err != nil {
		return []string{"cannot read .goreleaser.yaml: " + err.Error()}
	}
	built := matrixValues(string(source), "goos")
	arches := matrixValues(string(source), "goarch")
	if len(built) == 0 || len(arches) == 0 {
		return []string{"found no goos/goarch matrix in .goreleaser.yaml; has its shape changed?"}
	}

	// What the bundle carries, by the goos each staged file comes from.
	staged := map[string]bool{}
	for _, b := range binaries {
		for _, goos := range built {
			if strings.Contains(b.glob, goos+"_") || strings.Contains(b.glob, goos+"_all") {
				staged[goos] = true
			}
		}
		if strings.Contains(b.glob, "darwin_all") {
			staged["darwin"] = true
		}
	}
	var problems []string
	for _, goos := range built {
		if !staged[goos] {
			problems = append(problems, fmt.Sprintf(
				"goreleaser builds %s and the bundle stages nothing from it, so a %s user installing "+
					"the bundle gets a server that cannot start. Add it to binaries, or say in "+
					"compatibility.platforms that the bundle does not claim %s", goos, goos, goos))
		}
	}
	return problems
}

// matrixValues reads one inline list out of the build matrix, which is
// written as `goos: [linux, darwin, windows]`.
func matrixValues(source, key string) []string {
	m := regexp.MustCompile(`(?m)^\s*` + key + `:\s*\[([^\]]*)\]`).FindStringSubmatch(source)
	if m == nil {
		return nil
	}
	var out []string
	for _, v := range strings.Split(m[1], ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// upstreamSchema matches the host and path the manifest schema is
// published at, and captures the ref it is served from.
//
// An allow-list over the whole URL rather than a list of refs to refuse.
// Refusing `main`, `master` and `HEAD` is the obvious version and it
// passes a branch called anything else, a partial tag like `v2.1` that
// upstream re-points as it releases, and the right filename served by
// somebody who is not upstream.
var upstreamSchema = regexp.MustCompile(
	`^https://raw\.githubusercontent\.com/anthropics/mcpb/([^/]+)/schemas/mcpb-manifest-v(\d+\.\d+)\.schema\.json$`)

// immutableRef is a ref that cannot be moved under the document: a full
// release tag, or a commit SHA. A partial tag is a branch with a version
// number in it.
var immutableRef = regexp.MustCompile(`^(v[0-9]+\.[0-9]+\.[0-9]+|[0-9a-f]{40})$`)

// minManifestVersion is the format version this repository has checked.
//
// Checked 2026-09-17 against the published schemas: v0.2, v0.3 and v0.4
// are served and v0.5 is not, and 0.4's only change is a `uv` value in
// the `server.type` enum — which a `binary` server gains nothing from.
// Raise it only after checking what a newer format changes AND that a
// desktop installs a bundle declaring it.
const minManifestVersion = "0.3"

// claudeDesktopFloor is the Claude Desktop version every bundle in the
// family claims.
const claudeDesktopFloor = ">=0.10.0"

// declarationProblems holds what the manifest says about ITSELF: which
// format it is, which document defines that format, and where a failing
// install should be reported.
//
// checkManifest above holds the manifest against the bundle, which is
// the half a schema cannot do. This is the other half, and it is not the
// schema's either: the questions are whether the reference is pinned to
// bytes rather than to a branch, and whether the version is one somebody
// checked. A manifest agreeing with its own $schema is still a manifest
// a version behind — 0.2 beside a 0.2 schema passes every claim that
// holds the document against itself, which is how that shape spread.
func declarationProblems(schema, manifestVersion, support string) []string {
	var problems []string

	switch {
	case schema == "":
		problems = append(problems,
			"the manifest has no $schema, so nothing says which version of the format it is")
	case !upstreamSchema.MatchString(schema):
		problems = append(problems, fmt.Sprintf(
			"$schema is %q, which is not upstream's published mcpb-manifest-v<version>.schema.json path. "+
				"A schema fetched from anywhere else is not the document this format is defined by", schema))
	default:
		parts := upstreamSchema.FindStringSubmatch(schema)
		if !immutableRef.MatchString(parts[1]) {
			problems = append(problems, fmt.Sprintf(
				"$schema is served from %q, which can be re-pointed — a branch, or a partial tag. The "+
					"path pins the format and the ref pins the bytes, so an amendment upstream changes "+
					"what this document validates against. Name a full tag or a commit SHA", parts[1]))
		}
		if parts[2] != manifestVersion {
			problems = append(problems, fmt.Sprintf(
				"manifest_version is %q and $schema pins v%s; a document cannot claim one version and "+
					"validate against another", manifestVersion, parts[2]))
		}
	}

	if olderThan(manifestVersion, minManifestVersion) {
		problems = append(problems, fmt.Sprintf(
			"manifest_version is %q and this repository has checked %s", manifestVersion, minManifestVersion))
	}
	if support == "" {
		problems = append(problems,
			"the manifest has no support URL, so a bundle that fails on somebody's desktop does not say "+
				"where to report it")
	}
	return problems
}

// olderThan compares major.minor numerically. As text "0.10" sorts
// before "0.3", a bug that waits for the tenth minor version.
func olderThan(version, floor string) bool {
	major, minor, ok := majorMinor(version)
	floorMajor, floorMinor, floorOK := majorMinor(floor)
	if !ok || !floorOK {
		return !ok
	}
	if major != floorMajor {
		return major < floorMajor
	}
	return minor < floorMinor
}

func majorMinor(v string) (int, int, bool) {
	before, after, found := strings.Cut(v, ".")
	if !found {
		return 0, 0, false
	}
	major, err := strconv.Atoi(before)
	if err != nil {
		return 0, 0, false
	}
	minor, err := strconv.Atoi(after)
	if err != nil {
		return 0, 0, false
	}
	return major, minor, true
}

// bundleAsked are the settings the bundle asks for at install: what a
// person sets in another client's configuration. Each is asked under
// its name without the prefix, lowercased, which gives the family's
// bundles the same keys.
var bundleAsked = []string{
	config.EnvPrefix + "CLIENT_SECRET",
	config.EnvPrefix + "PROFILE",
	config.EnvPrefix + "READ_ONLY",
	config.EnvPrefix + "ENABLE_DESTRUCTIVE",
	config.EnvPrefix + "SHARING",
	config.EnvPrefix + "LOCAL_DIR",
	config.EnvPrefix + "MAX_DOWNLOAD",
	config.EnvPrefix + "LABELS",
	config.EnvPrefix + "ACTIVITY",
}

// bundleLeftOut is every other variable the server reads, with why the
// bundle does not ask for it.
var bundleLeftOut = map[string]string{
	config.EnvPrefix + "LOG_LEVEL":      "diagnostics, set by hand while chasing a problem",
	config.EnvPrefix + "LOG_FORMAT":     "diagnostics, set by hand while chasing a problem",
	config.EnvPrefix + "HTTP_TIMEOUT":   "tuning, whose default suits an install",
	config.EnvPrefix + "REQUIRE_PROMPT": "removed in 3.0.0, and read only to refuse it set",
	userconfig.EnvDir:                   "moves every profile, which tests do; a second account is a profile",
	credentials.EnvVar:                  "a credential for automation; a bundle install uses the login's keyring entry",
}

// serverVars is every variable the server reads: what Define looks up,
// and the two read before the flags are, named by their own constants
// so a rename is a compile error here rather than a shorter list.
func serverVars() []string {
	vars := append(config.EnvVars(), userconfig.EnvDir, credentials.EnvVar)
	slices.Sort(vars)
	return slices.Compact(vars)
}

// bundleKey is the user_config key a setting is asked under.
func bundleKey(name string) string {
	return strings.ToLower(strings.TrimPrefix(name, config.EnvPrefix))
}

// settingProblems holds what the bundle asks for to the server's own
// settings. known is every variable the server reads, an argument so a
// test can add one.
//
// Every variable is asked for or left out with a reason. Each one asked
// for reaches its variable, and nothing else is set. A switch defaults
// to the server's own default, compared as JSON so a text "false" is
// refused: in Claude Desktop the only guard besides the switch is its
// approval of each tool, which a person can set to always allow, so a
// bundle install may turn on nothing an unconfigured server leaves off.
// An optional key has a default, because the reference host passes
// ${user_config.x} through as written when it has none and the person
// leaves the field blank. And the environment the defaults give the
// server configures it as no environment does.
func settingProblems(manifest map[string]any, known []string) []string {
	var problems []string
	for _, name := range known {
		if !slices.Contains(bundleAsked, name) && bundleLeftOut[name] == "" {
			problems = append(problems, fmt.Sprintf("the server reads %s, and the bundle neither asks for it "+
				"nor says why not", name))
		}
	}
	for _, name := range slices.Concat(bundleAsked, sorted(bundleLeftOut)) {
		if !slices.Contains(known, name) {
			problems = append(problems, fmt.Sprintf("the bundle has a verdict on %s, which the server does not read", name))
		}
	}

	switches := map[string]string{}
	for _, v := range config.Vars() {
		if v.Switch {
			switches[v.Name] = v.Default
		}
	}
	declared, _ := manifest["user_config"].(map[string]any)
	server, _ := manifest["server"].(map[string]any)
	cfg, _ := server["mcp_config"].(map[string]any)
	env, _ := cfg["env"].(map[string]any)

	asked := map[string]bool{}
	for _, name := range bundleAsked {
		key := bundleKey(name)
		asked[key] = true
		opt, ok := declared[key].(map[string]any)
		if !ok {
			problems = append(problems, fmt.Sprintf("user_config does not ask for %s, which sets %s", key, name))
			continue
		}
		want := "${user_config." + key + "}"
		if got, _ := env[name].(string); got != want {
			problems = append(problems, fmt.Sprintf("mcp_config.env.%s is %q, want %q", name, got, want))
		}
		if def, isSwitch := switches[name]; isSwitch {
			got, _ := json.Marshal(opt["default"])
			if string(got) != def {
				kind, _ := opt["type"].(string)
				problems = append(problems, fmt.Sprintf("user_config.%s is a %s defaulting to %s; %s is a switch "+
					"the server defaults to %s, and a bundle install may turn on nothing an unconfigured server "+
					"leaves off", key, cmp.Or(kind, "field"), got, name, def))
			}
		}
	}
	for _, key := range sorted(declared) {
		if !asked[key] {
			problems = append(problems, fmt.Sprintf("user_config asks for %s, which is no setting's key", key))
		}
		opt, _ := declared[key].(map[string]any)
		required, _ := opt["required"].(bool)
		if _, hasDefault := opt["default"]; !required && !hasDefault {
			problems = append(problems, fmt.Sprintf("user_config.%s is optional with no default; the reference host "+
				"passes ${user_config.%s} to the server as written when the field is left blank", key, key))
		}
	}
	for _, name := range sorted(env) {
		if !slices.Contains(bundleAsked, name) {
			problems = append(problems, fmt.Sprintf("mcp_config.env sets %s, which the bundle does not ask for", name))
		}
	}
	if p := defaultsProblem(declared, env); p != "" {
		problems = append(problems, p)
	}
	return problems
}

// defaultsProblem reports a bundle whose defaults configure the server
// otherwise than no environment does.
//
// The environment is built as the reference host builds it: a boolean
// becomes "true" or "false", a text default stays as it is, and a
// reference with no default is left as written. A setting the person
// must supply is left out, since it is theirs either way.
func defaultsProblem(declared, env map[string]any) string {
	given := map[string]string{}
	for name, raw := range env {
		value, _ := raw.(string)
		required := false
		given[name] = userConfigRef.ReplaceAllStringFunc(value, func(ref string) string {
			opt, _ := declared[userConfigRef.FindStringSubmatch(ref)[1]].(map[string]any)
			if r, _ := opt["required"].(bool); r {
				required = true
			}
			switch d := opt["default"].(type) {
			case bool:
				return strconv.FormatBool(d)
			case string:
				return d
			case float64:
				return strconv.FormatFloat(d, 'f', -1, 64)
			}
			return ref
		})
		if required {
			delete(given, name)
		}
	}
	build := func(env map[string]string) (config.Config, error) {
		fs := flag.NewFlagSet("bundle", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		return config.Define(fs, func(name string) string { return env[name] }).Build()
	}
	got, err := build(given)
	if err != nil {
		return fmt.Sprintf("the environment the bundle's defaults give the server is refused: %v", err)
	}
	want, err := build(nil)
	if err != nil {
		return fmt.Sprintf("an unconfigured server is refused: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		return fmt.Sprintf("the bundle's defaults configure the server otherwise than no environment does: %v", given)
	}
	return ""
}
