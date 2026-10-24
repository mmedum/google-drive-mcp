package main

import (
	"archive/zip"
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mmedum/google-drive-mcp/v3/internal/config"
	"github.com/mmedum/google-drive-mcp/v3/internal/server"
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

// mcpbManifestPath is the committed manifest, which the gate and the
// packer both read: one file, so a check of it is a check of what ships.
const mcpbManifestPath = "packaging/mcpb/manifest.json"

// mcpbManifest prints the packaging manifest with a real version in it:
// the committed one under root, held to every check the gate runs, and
// stamped as the packer stamps it.
func mcpbManifest(out io.Writer, args []string) error {
	version := arg(args, 0, "")
	root := arg(args, 1, ".")
	if version == "" {
		return fmt.Errorf("usage: gates mcpb-manifest VERSION [ROOT]")
	}
	manifest, problems, err := committedManifest(root)
	if err != nil {
		return err
	}
	if len(problems) > 0 {
		return refused(out, problems)
	}
	stamped, err := stampManifest(manifest, version)
	if err != nil {
		return err
	}
	_, err = out.Write(stamped)
	return err
}

// committedManifest is where the gate and the packer both start: the
// committed manifest under root, read once and decoded once, and every
// problem manifestProblems finds in it and in the files beside it under
// the same root. A real version in it is a problem too: the tree
// outlives any release, so a version there is a stale one waiting to
// ship.
//
// It decodes into a map rather than a struct: the packer rewrites one
// field and must not drop the rest, and the manifest schema is not this
// repository's to model. A key repeated in one object is refused before
// anything else is checked. The decode keeps the last, as the schema
// validator and the host do, while a person reading the file can stop at
// the first, so the document reviewed would not be the one shipped.
func committedManifest(root string) (manifest map[string]any, problems []string, err error) {
	raw, err := os.ReadFile(filepath.Join(root, mcpbManifestPath)) //nolint:gosec // a repository path from a constant
	if err != nil {
		return nil, nil, err
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, []string{fmt.Sprintf("decode %s: %v", mcpbManifestPath, err)}, nil
	}
	if repeated := repeatedKeys(raw); len(repeated) > 0 {
		for _, at := range repeated {
			problems = append(problems, fmt.Sprintf("the manifest has %s more than once; the decode, the schema "+
				"and the host keep the last, and a person reading the file can stop at the first", at))
		}
		return nil, problems, nil
	}
	if got, _ := manifest["version"].(string); got != mcpbPlaceholder {
		problems = append(problems, fmt.Sprintf("%s carries version %q; the committed manifest must carry %q, "+
			"so that a version in the tree can never be a stale one", mcpbManifestPath, got, mcpbPlaceholder))
	}
	return manifest, append(problems, manifestProblems(manifest, root)...), nil
}

// repeatedKeys is the path of every key an object in raw has more than
// once, each once, in document order: "server" at the top level,
// "server.mcp_config.env.GDRIVE_PROFILE" inside, and [i] for an element
// of a list. A document that is not JSON has none here, since the
// decode reports it.
func repeatedKeys(raw []byte) []string {
	dec := json.NewDecoder(bytes.NewReader(raw))
	var repeated []string
	var walk func(path string) error
	walk = func(path string) error {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch tok {
		case json.Delim('{'):
			seen := map[string]int{}
			for dec.More() {
				keyTok, err := dec.Token()
				if err != nil {
					return err
				}
				key, _ := keyTok.(string)
				at := key
				if path != "" {
					at = path + "." + key
				}
				if seen[key]++; seen[key] == 2 {
					repeated = append(repeated, at)
				}
				if err := walk(at); err != nil {
					return err
				}
			}
		case json.Delim('['):
			for i := 0; dec.More(); i++ {
				if err := walk(fmt.Sprintf("%s[%d]", path, i)); err != nil {
					return err
				}
			}
		default:
			return nil
		}
		_, err = dec.Token() // the closing brace or bracket
		return err
	}
	if walk("") != nil {
		return nil
	}
	return repeated
}

// stampManifest is the committed manifest with version written in, as
// the bundle carries it. Through a decode and an encode rather than a
// text substitution, because the manifest is JSON and a sed over JSON is
// how a quote ends up inside a string. The encode sorts the keys and
// escapes <, > and &, so what it returns is the committed document but
// not its bytes, and it is held to the vendored schema again before
// anything ships it.
func stampManifest(manifest map[string]any, version string) ([]byte, error) {
	stamped := maps.Clone(manifest)
	stamped["version"] = strings.TrimPrefix(version, "v")
	encoded, err := json.MarshalIndent(stamped, "", "  ")
	if err != nil {
		return nil, err
	}
	encoded = append(encoded, '\n')
	if err := validateDocument(mcpbSchemaFile, "the stamped manifest", encoded); err != nil {
		return nil, err
	}
	return encoded, nil
}

// refused writes each problem to out, and is the error that stops the
// command.
func refused(out io.Writer, problems []string) error {
	for _, p := range problems {
		_, _ = fmt.Fprintln(out, "  "+p)
	}
	return fmt.Errorf("%d problem(s) in %s", len(problems), mcpbManifestPath)
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

// launcherPlatform is the platform the launcher is the entry point for.
const launcherPlatform = "linux"

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
	manifest, problems, err := committedManifest(".")
	if err != nil {
		return err
	}
	if len(problems) > 0 {
		return refused(out, problems)
	}
	userConfig, _ := manifest["user_config"].(map[string]any)
	_, _ = fmt.Fprintf(out, "mcpb manifest ok (%d files staged, %d platforms, %d user_config keys)\n",
		len(stagedNames())+1, len(manifestPlatforms(manifest)), len(userConfig))
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

	// The gate's own start, so the packer refuses what the gate refuses.
	manifest, problems, err := committedManifest(".")
	if err != nil {
		return err
	}
	if len(problems) > 0 {
		return refused(out, problems)
	}
	stamped, err := stampManifest(manifest, version)
	if err != nil {
		return err
	}

	contents := stagedNames()
	for _, b := range binaries {
		src, err := onlyMatch(filepath.Join(dist, b.glob), b.what)
		if err != nil {
			return err
		}
		contents[b.as] = src
	}

	bundle := filepath.Join(dist, fmt.Sprintf("google-drive-mcp_%s.mcpb", version))
	if err := writeBundle(bundle, stamped, contents); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "mcpb-pack: wrote %s (%d files)\n", bundle, len(contents)+1)
	return nil
}

// manifestProblems is every check the gate and the packer both run: the
// manifest against the names staged, the settings it asks for against
// the server's, the launcher and go.mod under root against the staged
// names, and the staged binaries against the build matrix under root.
func manifestProblems(manifest map[string]any, root string) []string {
	problems := checkManifest(manifest, stagedNames())
	problems = append(problems, settingProblems(manifest, config.Vars(), bundleLeftOut)...)
	problems = append(problems, checkLauncher(root)...)
	return append(problems, checkBuildMatrix(root)...)
}

// isProgram reports whether a staged name is a program: everything under
// server/ is run, and gets the execute bit, and nothing else is.
func isProgram(name string) bool {
	return strings.HasPrefix(name, "server/")
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

// writeBundle writes the zip, manifest being the stamped document as it
// ships. Names are sorted so the same inputs give the same archive,
// which is the least a build can offer somebody checking a checksum.
func writeBundle(bundle string, manifest []byte, contents map[string]string) error {
	f, err := os.Create(bundle) //nolint:gosec // a path built from dist and the version
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	zw := zip.NewWriter(f)

	if err := addBytes(zw, "manifest.json", manifest, 0o644); err != nil {
		return err
	}
	for _, name := range sorted(contents) {
		mode := os.FileMode(0o644)
		if isProgram(name) {
			mode = 0o755
		}
		if err := addFile(zw, name, contents[name], mode); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	// Close's error is the pack's: the last of the archive can reach the
	// disk only there, and a bundle cut short must not pass. The deferred
	// Close then does nothing.
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
	problems = append(problems, identityProblems(manifest)...)

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

	// Only a program can be spawned: LICENSE and README.md are staged to
	// be read.
	program := func(name string) bool {
		_, ok := contents[name]
		return ok && isProgram(name)
	}
	entry, _ := server["entry_point"].(string)
	if !program(entry) {
		problems = append(problems, fmt.Sprintf(
			"server.entry_point names %q, which is not a program the packer stages", entry))
	}

	cfg, _ := server["mcp_config"].(map[string]any)
	if cfg == nil {
		return append(problems, "manifest has no server.mcp_config")
	}
	// runs is the program each command spawns, keyed by platform with ""
	// for the base command. A command that names none is reported here
	// and left out, so the platform check below does not report it again.
	runs := map[string]string{}
	resolve := func(platform, where, command string) {
		// ${__dirname} is the one path the specification fixes to the
		// bundle; what a relative command is resolved against, it never
		// says (§18).
		name, ok := strings.CutPrefix(command, "${__dirname}/")
		switch {
		case !ok:
			problems = append(problems, fmt.Sprintf("%s is %q; a command starts with ${__dirname}/, the one "+
				"path the specification fixes to the bundle", where, command))
		case !program(name):
			problems = append(problems, fmt.Sprintf(
				"%s names %q, which is not a program the packer stages", where, name))
		default:
			runs[platform] = name
		}
	}
	command, _ := cfg["command"].(string)
	resolve("", "server.mcp_config.command", command)
	// entry_point is the file the default command runs, so the bundle
	// does not name one file as the server and spawn another. An entry
	// point or a command that names no program is reported above.
	if got, ok := runs[""]; ok && program(entry) && got != entry {
		problems = append(problems, fmt.Sprintf("server.entry_point is %q, and server.mcp_config.command runs %q",
			entry, got))
	}

	overrides, _ := cfg["platform_overrides"].(map[string]any)
	claimed := manifestPlatforms(manifest)
	for _, platform := range sorted(overrides) {
		over, _ := overrides[platform].(map[string]any)
		c, _ := over["command"].(string)
		resolve(platform, "platform_overrides."+platform+".command", c)
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
	// a staged program. A Mach-O binary on Windows is exactly the "packs
	// cleanly and then does nothing" failure this function exists to
	// catch.
	// A claimed platform nothing is staged for is reported above.
	for _, platform := range claimed {
		want := entryPointFor(platform)
		if want == "" {
			continue
		}
		from := ""
		if _, ok := overrides[platform]; ok {
			from = platform
		}
		if got, ok := runs[from]; ok && got != want {
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

// identityProblems holds what the manifest says it is: the server's own
// name, a binary, the family's Claude Desktop floor, a privacy policy,
// and exactly the platforms the packer stages an entry point for, so
// dropping one platform and its override together is refused.
// checkManifest holds each platform to the file staged for it.
func identityProblems(manifest map[string]any) []string {
	var problems []string
	if name, _ := manifest["name"].(string); name != mcpbName {
		problems = append(problems, fmt.Sprintf("the manifest's name is %q and the server's is %q", name, mcpbName))
	}
	server, _ := manifest["server"].(map[string]any)
	if kind, _ := server["type"].(string); server != nil && kind != "binary" {
		problems = append(problems, fmt.Sprintf("server.type is %q, and this bundle ships binaries", kind))
	}
	compat, _ := manifest["compatibility"].(map[string]any)
	if got, _ := compat["claude_desktop"].(string); got != claudeDesktopFloor {
		problems = append(problems, fmt.Sprintf("compatibility.claude_desktop is %q; the family's bundles claim %q",
			got, claudeDesktopFloor))
	}
	platforms, staged := slices.Sorted(slices.Values(manifestPlatforms(manifest))), stagedPlatforms()
	if !slices.Equal(platforms, staged) {
		problems = append(problems, fmt.Sprintf("compatibility.platforms is %v; the packer stages entry points for %v",
			platforms, staged))
	}
	// The manifest specification requires privacy_policies of a server
	// that connects to a service processing user data, as this one does
	// to Google, and neither the schema nor `mcpb validate` enforces it.
	if policies, _ := manifest["privacy_policies"].([]any); len(policies) == 0 {
		problems = append(problems, "privacy_policies is empty; the manifest specification requires one for a "+
			"server that connects to a service processing user data, as this one does to Google")
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
	if platform == launcherPlatform {
		return linuxLauncher
	}
	for _, b := range binaries {
		if b.platform == platform {
			return b.as
		}
	}
	return ""
}

// stagedPlatforms is every platform the packer stages an entry point
// for, sorted: each binary's, and the launcher's.
func stagedPlatforms() []string {
	platforms := []string{launcherPlatform}
	for _, b := range binaries {
		if b.platform != "" {
			platforms = append(platforms, b.platform)
		}
	}
	slices.Sort(platforms)
	return platforms
}

// mcpbName is the name the server gives itself, which the manifest's
// must be.
const mcpbName = server.Name

// checkLauncher holds the Linux launcher under root to the names the
// packer stages.
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
// go.mod under root the same way.
func checkLauncher(root string) []string {
	source, ok := alongside[linuxLauncher]
	if !ok {
		return []string{"the packer stages no " + linuxLauncher}
	}
	body, err := os.ReadFile(filepath.Join(root, source)) //nolint:gosec // a path this repository owns
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
	goMod, err := os.ReadFile(filepath.Join(root, "go.mod")) //nolint:gosec // a repository path from a constant
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

// checkBuildMatrix holds the staged binaries to what goreleaser builds,
// as .goreleaser.yaml under root says.
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
func checkBuildMatrix(root string) []string {
	source, err := os.ReadFile(filepath.Join(root, ".goreleaser.yaml")) //nolint:gosec // a repository path from a constant
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

// bundleLeftOut is every variable the server reads that the bundle does
// not ask for, with why. The bundle asks for every other one, each under
// its name without the prefix, lowercased, which gives the family's
// bundles the same keys.
var bundleLeftOut = map[string]string{
	config.EnvPrefix + "LOG_LEVEL":      "diagnostics, set by hand while chasing a problem",
	config.EnvPrefix + "LOG_FORMAT":     "diagnostics, set by hand while chasing a problem",
	config.EnvPrefix + "HTTP_TIMEOUT":   "tuning, whose default suits an install",
	config.EnvPrefix + "REQUIRE_PROMPT": "removed in 3.0.0, and read only to refuse it set",
	config.EnvConfigDir:                 "moves every profile, which tests do; a second account is a profile",
	config.EnvRefreshToken:              "a credential for automation; a bundle install uses the login's keyring entry",
}

// askedFrom is what the bundle asks for at install: every variable the
// server reads, less each one leftOut gives a reason for. A variable
// added later is asked for until the manifest asks for it or a reason is
// written. It also refuses a reason for a variable the server does not
// read, so a removed one cannot linger.
func askedFrom(vars []config.Var, leftOut map[string]string) (asked []config.Var, problems []string) {
	read := map[string]bool{}
	for _, v := range vars {
		read[v.Name] = true
		if leftOut[v.Name] == "" {
			asked = append(asked, v)
		}
	}
	for _, name := range sorted(leftOut) {
		if !read[name] {
			problems = append(problems, fmt.Sprintf("bundleLeftOut names %s, which the server does not read", name))
		}
	}
	return asked, problems
}

// bundleKey is the user_config key a setting is asked under.
func bundleKey(name string) string {
	return strings.ToLower(strings.TrimPrefix(name, config.EnvPrefix))
}

// fieldRules are the settings whose field must say what the server
// accepts, since it refuses anything else at start, each with the words
// the field must carry.
func fieldRules() map[string]string {
	return map[string]string{
		config.EnvPrefix + "PROFILE": config.ProfileRule,
		config.EnvPrefix + "SHARING": config.SharingRule,
	}
}

// settingProblems holds what the bundle asks for to the server's own
// settings. vars and leftOut are config.Vars and bundleLeftOut,
// arguments so a test can add to either.
//
// Every variable askedFrom derives is asked for and reaches its
// variable, and nothing else is set. A switch is asked as a boolean, so
// Claude Desktop shows it as one. An optional key has a default, because
// the reference host passes ${user_config.x} through as written when it
// has none and the person leaves the field blank. The defaults configure
// the server as no environment does, and the switches the server
// refuses together are the ones bundleRefused declares.
func settingProblems(manifest map[string]any, vars []config.Var, leftOut map[string]string) []string {
	asked, problems := askedFrom(vars, leftOut)
	declared, _ := manifest["user_config"].(map[string]any)
	server, _ := manifest["server"].(map[string]any)
	cfg, _ := server["mcp_config"].(map[string]any)
	env, _ := cfg["env"].(map[string]any)
	problems = append(problems, argsProblems(cfg)...)
	problems = append(problems, askedProblems(declared, env, asked)...)

	keys := map[string]bool{}
	for _, v := range asked {
		keys[bundleKey(v.Name)] = true
	}
	for _, key := range sorted(declared) {
		if !keys[key] {
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
		switch {
		case slices.ContainsFunc(asked, func(v config.Var) bool { return v.Name == name }):
		case leftOut[name] != "":
			problems = append(problems, fmt.Sprintf("mcp_config.env sets %s, which the bundle leaves out: %s",
				name, leftOut[name]))
		default:
			problems = append(problems, fmt.Sprintf("mcp_config.env sets %s, which the server does not read", name))
		}
	}
	defaults := startDefaults(manifest)
	problems = append(problems, defaultsProblems(defaults)...)
	return append(problems, refusalProblems(manifest, asked, bundleRefused, defaults.err)...)
}

// argsProblems holds the settings to mcp_config.env, which every other
// check reads. The base args stays [], and a platform override carries
// neither args nor env, an empty one included, because the reference
// host takes an override's in place of the base's (§18). An argument
// such as --enable-destructive=true would configure the server past
// every check on env.
func argsProblems(cfg map[string]any) []string {
	const keep = "keep it as [], the shape every released bundle has had, since no Claude Desktop launch " +
		"has shown the key optional"
	var problems []string
	args, present := cfg["args"]
	list, isList := args.([]any)
	switch {
	case !present:
		problems = append(problems, "mcp_config.args is missing; "+keep)
	case args == nil:
		problems = append(problems, "mcp_config.args is null; "+keep)
	case !isList || len(list) > 0:
		problems = append(problems, "mcp_config.args is not []; settings pass by env only, and an argument "+
			"there would configure the server past every check on env")
	}
	overrides, _ := cfg["platform_overrides"].(map[string]any)
	for _, platform := range sorted(overrides) {
		over, _ := overrides[platform].(map[string]any)
		if _, ok := over["args"]; ok {
			problems = append(problems, fmt.Sprintf("platform_overrides.%s.args is set; a host takes it in place of "+
				"mcp_config.args, an empty one too, and settings pass by env only", platform))
		}
		if _, ok := over["env"]; ok {
			problems = append(problems, fmt.Sprintf("platform_overrides.%s.env is set; a host takes it in place of "+
				"mcp_config.env, an empty one too, so put every variable in mcp_config.env", platform))
		}
	}
	return problems
}

// askedProblems holds each asked variable to its field: declared, spent
// by its own variable alone, a boolean for a switch, and saying what the
// server accepts where fieldRules names it. A bundle that asks for none
// of fieldRules' settings is refused, so that check cannot go quiet.
func askedProblems(declared, env map[string]any, asked []config.Var) []string {
	var problems []string
	rules := fieldRules()
	held := map[string]bool{}
	for _, v := range asked {
		key := bundleKey(v.Name)
		held[v.Name] = true
		opt, ok := declared[key].(map[string]any)
		if !ok {
			problems = append(problems, fmt.Sprintf("user_config does not ask for %s, which sets %s", key, v.Name))
			continue
		}
		want := "${user_config." + key + "}"
		if got, _ := env[v.Name].(string); got != want {
			problems = append(problems, fmt.Sprintf("mcp_config.env.%s is %q, want %q", v.Name, got, want))
		}
		if kind, _ := opt["type"].(string); v.Switch && kind != "boolean" {
			problems = append(problems, fmt.Sprintf("user_config.%s is a %s; %s is a switch, so ask for a boolean",
				key, cmp.Or(kind, "field with no type"), v.Name))
		}
		description, _ := opt["description"].(string)
		if rule := rules[v.Name]; rule != "" && !strings.Contains(description, rule) {
			problems = append(problems, fmt.Sprintf("user_config.%s does not say %q, which is what %s accepts",
				key, rule, v.Name))
		}
	}
	for _, name := range sorted(rules) {
		if !held[name] {
			problems = append(problems, fmt.Sprintf("the bundle asks for no %s, so nothing holds its field to %q",
				name, rules[name]))
		}
	}
	return problems
}

// hostEnv is the environment the reference host builds from the
// bundle's defaults, with each key in on set to true whatever its
// default, as the person setting it would. A boolean becomes "true" or
// "false", a string stays as it is, a number is written out, and a
// reference to a key with no default is left as written.
//
// A variable spending a required key with no default is left out: the
// person supplies it, and it is theirs either way. One with a default is
// not, since Claude Desktop's form may fill it in from the default and a
// person may accept it unread. A default that is a list or an object is
// not guessed at: its variable is left out, and its key is in unreadable
// with what it is.
func hostEnv(manifest map[string]any, on ...string) (env, unreadable map[string]string) {
	declared, _ := manifest["user_config"].(map[string]any)
	server, _ := manifest["server"].(map[string]any)
	cfg, _ := server["mcp_config"].(map[string]any)
	given, _ := cfg["env"].(map[string]any)
	env, unreadable = map[string]string{}, map[string]string{}
	for name, raw := range given {
		value, _ := raw.(string)
		var b strings.Builder
		skip, last := false, 0
		for _, at := range userConfigRef.FindAllStringSubmatchIndex(value, -1) {
			key, sub := value[at[2]:at[3]], value[at[0]:at[1]]
			opt, _ := declared[key].(map[string]any)
			def := opt["default"]
			if slices.Contains(on, key) {
				def = true
			}
			switch d := def.(type) {
			case bool:
				sub = strconv.FormatBool(d)
			case string:
				sub = d
			case float64:
				sub = strconv.FormatFloat(d, 'f', -1, 64)
			case nil:
				required, _ := opt["required"].(bool)
				skip = skip || required
			case []any:
				unreadable[key], skip = "a list", true
			case map[string]any:
				unreadable[key], skip = "an object", true
			}
			b.WriteString(value[last:at[0]] + sub)
			last = at[1]
		}
		if !skip {
			env[name] = b.String() + value[last:]
		}
	}
	return env, unreadable
}

// loadEnv starts the server's configuration from an environment alone.
func loadEnv(env map[string]string) (config.Config, error) {
	return config.Load(nil, func(name string) string { return env[name] })
}

// bundleDefaults is the environment a host builds from the bundle's
// defaults, and what the server starts with it, built once for the
// defaults and refusal checks.
type bundleDefaults struct {
	env, unreadable map[string]string
	config          config.Config
	err             error
}

func startDefaults(manifest map[string]any) bundleDefaults {
	var d bundleDefaults
	d.env, d.unreadable = hostEnv(manifest)
	d.config, d.err = loadEnv(d.env)
	return d
}

// defaultsProblems reports a bundle whose defaults configure the server
// otherwise than no environment does, naming the fields that differ. In
// Claude Desktop the only guard besides the switch is its approval of
// each tool, which a person can set to always allow, so a bundle install
// may turn on nothing an unconfigured server leaves off (§18). A list or
// object default is reported, every one in key order, and the comparison
// still runs over the rest.
func defaultsProblems(d bundleDefaults) []string {
	var problems []string
	for _, key := range sorted(d.unreadable) {
		problems = append(problems, fmt.Sprintf("user_config.%s defaults to %s, which the defaults check cannot "+
			"build an environment from: give it a boolean, a string or a number", key, d.unreadable[key]))
	}
	if d.err != nil {
		return append(problems, fmt.Sprintf("the environment the bundle's defaults give the server is refused: %v", d.err))
	}
	want, err := loadEnv(nil)
	if err != nil {
		return append(problems, fmt.Sprintf("an unconfigured server is refused: %v", err))
	}
	if diff := configDiff(d.config, want); len(diff) > 0 {
		problems = append(problems, fmt.Sprintf("the bundle's defaults configure the server otherwise than no "+
			"environment does, in %s; a bundle install may turn on nothing an unconfigured server leaves off",
			strings.Join(diff, ", ")))
	}
	return problems
}

// configDiff names the fields two configurations disagree on.
func configDiff(a, b config.Config) []string {
	return fieldDiff(a, b)
}

// fieldDiff names the fields of two values of one struct type that
// differ. An unexported field cannot be read out through Interface, so
// it is compared with Value.Equal where every value of its type compares;
// one that does not, and differs, is reported unnamed.
func fieldDiff(a, b any) []string {
	va, vb := reflect.ValueOf(a), reflect.ValueOf(b)
	var fields []string
	unnamed := false
	for i := range va.NumField() {
		f := va.Type().Field(i)
		switch {
		case f.IsExported():
			if !reflect.DeepEqual(va.Field(i).Interface(), vb.Field(i).Interface()) {
				fields = append(fields, f.Name)
			}
		case strictlyComparable(f.Type):
			if !va.Field(i).Equal(vb.Field(i)) {
				fields = append(fields, f.Name)
			}
		default:
			unnamed = true
		}
	}
	if unnamed && len(fields) == 0 && !reflect.DeepEqual(a, b) {
		fields = append(fields, "an unexported field")
	}
	return fields
}

// strictlyComparable reports whether every value of t compares with
// Value.Equal. An interface type compares as a type, but Equal panics on
// a dynamic value that does not, so one anywhere inside t rules it out.
func strictlyComparable(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Interface:
		return false
	case reflect.Struct:
		for i := range t.NumField() {
			if !strictlyComparable(t.Field(i).Type) {
				return false
			}
		}
		return true
	case reflect.Array:
		return strictlyComparable(t.Elem())
	}
	return t.Comparable()
}

// bundleRefused are the sets of the bundle's switches the server refuses
// at start, each sorted. Each field in one names the others' titles in
// its description. Build refuses none today: read-only leaves the
// destructive switch nothing to register rather than refusing it. The
// gate starts every set of the switches and holds the refused ones to
// this list, both ways, so a refusal added to Build fails here until it
// is declared and the fields say so.
var bundleRefused [][]string

// refusalProblems starts every set of the bundle's switches, each on
// over the defaults, and holds the ones the server refuses to declared:
// a refusal it does not declare, which no description then warns of, and
// a declared one the server starts with, each reported for the smallest
// set that shows it. A declared key that is not a switch the bundle asks
// for is refused too, and so is one with no title, which the others'
// descriptions would name, each once. When the defaults alone do not
// start, which defaultsErr says, it starts no set: the defaults check
// reports that, and every set would fail with it.
func refusalProblems(manifest map[string]any, asked []config.Var, declared [][]string, defaultsErr error) []string {
	options, _ := manifest["user_config"].(map[string]any)
	var switches []string
	for _, v := range asked {
		if _, ok := options[bundleKey(v.Name)]; ok && v.Switch {
			switches = append(switches, bundleKey(v.Name))
		}
	}
	slices.Sort(switches)
	problems := declaredProblems(options, switches, declared)
	if defaultsErr != nil {
		return problems
	}
	return append(problems, startProblems(manifest, switches, declared)...)
}

// declaredProblems holds each key in a declared set to a switch with a
// title, each once, and each switch's description to the titles of the
// others in its sets, trimmed.
func declaredProblems(options map[string]any, switches []string, declared [][]string) []string {
	field := func(key string) (title, description string) {
		opt, _ := options[key].(map[string]any)
		title, _ = opt["title"].(string)
		description, _ = opt["description"].(string)
		return strings.TrimSpace(title), description
	}
	var problems []string
	inSets := map[string]bool{}
	for _, set := range declared {
		for _, key := range set {
			inSets[key] = true
		}
	}
	titled := map[string]bool{}
	for _, key := range sorted(inSets) {
		title, _ := field(key)
		switch {
		case !slices.Contains(switches, key):
			problems = append(problems, fmt.Sprintf("bundleRefused names %s, which is not a switch the bundle asks for", key))
		case title == "":
			problems = append(problems, fmt.Sprintf("user_config.%s has no title, which the fields the server "+
				"refuses it with must name", key))
		default:
			titled[key] = true
		}
	}
	for _, set := range declared {
		for _, key := range set {
			if !slices.Contains(switches, key) {
				continue
			}
			_, description := field(key)
			for _, other := range set {
				if title, _ := field(other); other != key && titled[other] && !strings.Contains(description, title) {
					problems = append(problems, fmt.Sprintf("user_config.%s does not name %q, which the server "+
						"refuses it with", key, title))
				}
			}
		}
	}
	return problems
}

// startProblems starts every set of the switches over the defaults and
// holds the server's refusals to declared, both ways. Every subset of a
// set comes before it in this order, so the first set to show a defect
// is the smallest, and a superset is not reported again.
func startProblems(manifest map[string]any, switches []string, declared [][]string) []string {
	var problems []string
	var undeclared []int
	startsDeclared := map[int]bool{}
	for bits := 1; bits < 1<<len(switches); bits++ {
		var on []string
		for i, key := range switches {
			if bits&(1<<i) != 0 {
				on = append(on, key)
			}
		}
		env, _ := hostEnv(manifest, on...)
		_, err := loadEnv(env)
		var within []int
		for i, set := range declared {
			if !slices.ContainsFunc(set, func(key string) bool { return !slices.Contains(on, key) }) {
				within = append(within, i)
			}
		}
		switch {
		case err != nil && len(within) == 0:
			if slices.ContainsFunc(undeclared, func(shown int) bool { return shown&bits == shown }) {
				continue
			}
			undeclared = append(undeclared, bits)
			problems = append(problems, fmt.Sprintf("the server refuses %s together, and bundleRefused does not "+
				"declare it, so no field's description warns of it: %v", strings.Join(on, ", "), err))
		case err == nil:
			for _, i := range within {
				if !startsDeclared[i] {
					startsDeclared[i] = true
					problems = append(problems, fmt.Sprintf("bundleRefused declares that the server refuses %s "+
						"together, and it starts with them", strings.Join(declared[i], ", ")))
				}
			}
		}
	}
	return problems
}
