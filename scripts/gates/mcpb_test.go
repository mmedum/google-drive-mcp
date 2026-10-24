package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/mmedum/google-drive-mcp/v3/internal/config"
)

// TestTheCommittedManifestCarriesThePlaceholder is the guard against a
// manifest in the tree claiming a version. It cannot be right for long:
// the tree outlives any release, so a real version there is a stale one
// waiting to ship.
func TestTheCommittedManifestCarriesThePlaceholder(t *testing.T) {
	t.Chdir("../..")
	var out bytes.Buffer
	if err := mcpbManifest(&out, []string{"1.2.3"}); err != nil {
		t.Fatalf("mcpb-manifest: %v", err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(out.Bytes(), &manifest); err != nil {
		t.Fatalf("the injected manifest is not JSON: %v", err)
	}
	if got := manifest["version"]; got != "1.2.3" {
		t.Errorf("version = %v, want 1.2.3", got)
	}
	// Everything else has to survive: the injection rewrites one field
	// and a struct that modeled the schema would silently drop the rest.
	for _, key := range []string{"$schema", "manifest_version", "name", "server", "user_config"} {
		if manifest[key] == nil {
			t.Errorf("the injected manifest lost %s", key)
		}
	}
}

// TestARealVersionInTheTreeIsRefused proves the guard bites.
func TestARealVersionInTheTreeIsRefused(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(path, []byte(`{"version":"1.0.0"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	err := mcpbManifest(&bytes.Buffer{}, []string{"2.0.0", path})
	if err == nil {
		t.Fatal("a manifest carrying a real version was accepted")
	}
	if !strings.Contains(err.Error(), mcpbPlaceholder) {
		t.Errorf("the refusal does not say what the manifest must carry: %v", err)
	}
}

// TestTheManifestIsCheckedAgainstTheBundle is what replaced validating
// against the published schema, and it catches what a schema cannot: a
// manifest that is well formed and names files nobody packed. Each break
// is one defect and gives one report.
func TestTheManifestIsCheckedAgainstTheBundle(t *testing.T) {
	packed := map[string]string{
		"server/google-drive-mcp-darwin": "somewhere",
		"server/launch-linux.sh":         "somewhere",
		"server/google-drive-mcp.exe":    "somewhere",
		"LICENSE":                        "somewhere",
		"README.md":                      "somewhere",
	}
	good := func() map[string]any {
		return map[string]any{
			// A real $schema and support URL: the check reads both
			// now, and a stub for a value nothing looked at is how a
			// fixture stops describing the thing it stands for.
			"$schema": "https://raw.githubusercontent.com/anthropics/mcpb/v2.1.2/" +
				"schemas/mcpb-manifest-v0.3.schema.json",
			"manifest_version": "0.3", "name": "n",
			"support": "https://example.invalid/issues",
			"version": "1.0.0", "description": "d", "author": map[string]any{"name": "Example Maintainer"},
			"privacy_policies": []any{"https://policies.google.com/privacy"},
			"compatibility": map[string]any{
				"claude_desktop": claudeDesktopFloor,
				"platforms":      []any{"darwin", "linux", "win32"},
			},
			// A real user_config entry: the schema requires a type, a
			// title and a description on each one, and a fixture that
			// omits them has stopped describing what it stands for.
			"user_config": map[string]any{"client_secret": map[string]any{
				"type":        "file",
				"title":       "OAuth client secret",
				"description": "The client secret JSON downloaded from Google Cloud.",
			}},
			"server": map[string]any{
				"type":        "binary",
				"entry_point": "server/google-drive-mcp-darwin",
				"mcp_config": map[string]any{
					"command": "${__dirname}/server/google-drive-mcp-darwin",
					"env":     map[string]any{"GDRIVE_CLIENT_SECRET": "${user_config.client_secret}"},
					"platform_overrides": map[string]any{
						"linux": map[string]any{"command": "${__dirname}/server/launch-linux.sh"},
						"win32": map[string]any{"command": "${__dirname}/server/google-drive-mcp.exe"},
					},
				},
			},
		}
	}
	if problems := checkManifest(good(), packed); len(problems) > 0 {
		t.Fatalf("a correct manifest was rejected:\n%s", strings.Join(problems, "\n"))
	}

	cases := []struct {
		name   string
		break_ func(map[string]any)
		want   string
	}{
		{
			name:   "an entry point nobody packed",
			break_: func(m map[string]any) { dig(m, "server")["entry_point"] = "server/typo" },
			want:   `server.entry_point names "server/typo", which is not a program the packer stages`,
		},
		{
			name:   "an entry point that is a document",
			break_: func(m map[string]any) { dig(m, "server")["entry_point"] = "LICENSE" },
			want:   `server.entry_point names "LICENSE", which is not a program the packer stages`,
		},
		{
			name: "a platform override pointing nowhere",
			break_: func(m map[string]any) {
				dig(m, "server", "mcp_config", "platform_overrides", "linux")["command"] = "${__dirname}/server/gone.sh"
			},
			want: `platform_overrides.linux.command names "server/gone.sh", which is not a program`,
		},
		{
			name: "a command that is a document",
			break_: func(m map[string]any) {
				dig(m, "server", "mcp_config")["command"] = "${__dirname}/README.md"
			},
			want: `server.mcp_config.command names "README.md", which is not a program the packer stages`,
		},
		{
			// The specification never says what a relative command is
			// resolved against, and the reference host spawns it as
			// written.
			name: "a command relative to the bundle",
			break_: func(m map[string]any) {
				dig(m, "server", "mcp_config")["command"] = "server/google-drive-mcp-darwin"
			},
			want: `server.mcp_config.command is "server/google-drive-mcp-darwin"; a command starts with ${__dirname}/`,
		},
		{
			name: "a command from the root",
			break_: func(m map[string]any) {
				dig(m, "server", "mcp_config", "platform_overrides", "win32")["command"] = "/server/google-drive-mcp.exe"
			},
			want: `platform_overrides.win32.command is "/server/google-drive-mcp.exe"; a command starts with ${__dirname}/`,
		},
		{
			name: "a user_config key nobody declares",
			break_: func(m map[string]any) {
				dig(m, "server", "mcp_config", "env")["GDRIVE_CLIENT_SECRET"] = "${user_config.oauth_json}"
			},
			want: "which user_config does not declare",
		},
		{
			// Composed, with the undeclared key second: every reference
			// in a value is spent, not only the first.
			name: "an undeclared key after a declared one",
			break_: func(m map[string]any) {
				dig(m, "server", "mcp_config", "env")["GDRIVE_CLIENT_SECRET"] = "${user_config.client_secret}/${user_config.oauth_json}"
			},
			want: "spends ${user_config.oauth_json}",
		},
		{
			name: "an override for a platform the bundle does not claim",
			break_: func(m map[string]any) {
				dig(m, "compatibility")["platforms"] = []any{"darwin", "win32"}
			},
			want: "platform_overrides.linux is an override for a platform compatibility.platforms does not claim",
		},
		{
			// The direction the override check does not cover, and the
			// one that would have shipped: delete the win32 override and
			// Windows runs the DEFAULT command, which is the macOS
			// universal binary. That file really is staged, so every
			// other check here is satisfied.
			name: "a claimed platform with no command of its own",
			break_: func(m map[string]any) {
				delete(dig(m, "server", "mcp_config", "platform_overrides"), "win32")
			},
			want: "on win32 the bundle would spawn",
		},
		{
			name:   "a server that is not a binary",
			break_: func(m map[string]any) { dig(m, "server")["type"] = "node" },
			want:   "this bundle ships binaries",
		},
		{
			name:   "no privacy policy",
			break_: func(m map[string]any) { delete(m, "privacy_policies") },
			want:   "privacy_policies is empty",
		},
		{
			name:   "an empty privacy policy list",
			break_: func(m map[string]any) { m["privacy_policies"] = []any{} },
			want:   "privacy_policies is empty",
		},
		{
			name:   "no Claude Desktop floor",
			break_: func(m map[string]any) { delete(dig(m, "compatibility"), "claude_desktop") },
			want:   `compatibility.claude_desktop is ""; the family's bundles claim ">=0.10.0"`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := good()
			c.break_(m)
			problems := checkManifest(m, packed)
			if len(problems) != 1 || !strings.Contains(problems[0], c.want) {
				t.Errorf("the report is not the one problem %q:\n%s", c.want, strings.Join(problems, "\n"))
			}
		})
	}
}

// TestTheBundleIsAZipWithTheModesItNeeds. A zip records the mode, and
// everything under server/ is run: without the execute bit the bundle
// installs and then cannot start.
func TestTheBundleIsAZipWithTheModesItNeeds(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "payload")
	if err := os.WriteFile(src, []byte("binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(dir, "out.mcpb")
	err := writeBundle(bundle, map[string]any{"name": "n"}, map[string]string{
		"server/google-drive-mcp-darwin": src,
		"LICENSE":                        src,
	})
	if err != nil {
		t.Fatalf("writeBundle: %v", err)
	}

	r, err := zip.OpenReader(bundle)
	if err != nil {
		t.Fatalf("the bundle is not a readable zip: %v", err)
	}
	defer func() { _ = r.Close() }()

	modes := map[string]os.FileMode{}
	for _, f := range r.File {
		modes[f.Name] = f.Mode().Perm()
		if f.Modified.IsZero() {
			t.Errorf("%s carries no modification time, which writes as an impossible date", f.Name)
		}
	}
	if modes["server/google-drive-mcp-darwin"] != 0o755 {
		t.Errorf("the server binary is %o, want 755", modes["server/google-drive-mcp-darwin"])
	}
	if modes["LICENSE"] != 0o644 {
		t.Errorf("LICENSE is %o, want 644", modes["LICENSE"])
	}
	if _, ok := modes["manifest.json"]; !ok {
		t.Error("the bundle has no manifest.json at its root")
	}
}

// TestTheCommittedManifestNamesFilesThePackerStages is the half of the
// packer's validation that needs no build, and it is a gate for that
// reason: every question checkManifest asks is referential — does this
// name a file that will be there — and the names are static even when
// the binaries are not.
//
// Before this ran on every commit, a manifest naming a file nobody
// stages was a release-day failure. It is a commit-day one now.
func TestTheCommittedManifestNamesFilesThePackerStages(t *testing.T) {
	t.Chdir("../..")
	var out bytes.Buffer
	if err := mcpbCheck(&out, nil); err != nil {
		t.Fatalf("the committed manifest does not match what the packer stages: %v\n%s", err, out.String())
	}
	// And it read something: today's 8 staged files, 3 platforms and 9
	// user_config keys.
	var files, platforms, keys int
	if _, err := fmt.Sscanf(out.String(), "mcpb manifest ok (%d files staged, %d platforms, %d user_config keys)",
		&files, &platforms, &keys); err != nil {
		t.Fatalf("cannot read the summary %q: %v", out.String(), err)
	}
	if files < 4 || platforms < 3 || keys < 9 {
		t.Errorf("summary %q is under its floors of 4 files, 3 platforms and 9 user_config keys", out.String())
	}
}

// TestAManifestNamingAnUnstagedFileIsRefused proves the gate above
// bites, against the real staged names rather than an invented set: a
// check nobody has watched fail is a check nobody knows the shape of.
func TestAManifestNamingAnUnstagedFileIsRefused(t *testing.T) {
	t.Chdir("../..")
	manifest, err := readManifest(mcpbManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	server, _ := manifest["server"].(map[string]any)
	server["entry_point"] = "server/google-drive-mcp-riscv64"
	problems := checkManifest(manifest, stagedNames())
	if len(problems) == 0 {
		t.Fatal("an entry point the packer never stages was accepted")
	}
	if !strings.Contains(strings.Join(problems, "\n"), "riscv64") {
		t.Errorf("the report does not name the file:\n%s", strings.Join(problems, "\n"))
	}
}

// TestTheStagedNamesAreTheOnesThePackerWrites holds the two halves
// together. mcpbCheck answers a referential question from stagedNames
// alone; if the packer put a file in the bundle that stagedNames does
// not know about, the gate would be checking against a tree that is not
// the one shipped.
func TestTheStagedNamesAreTheOnesThePackerWrites(t *testing.T) {
	names := stagedNames()
	for _, b := range binaries {
		if _, ok := names[b.as]; !ok {
			t.Errorf("the packer stages %s and stagedNames does not list it", b.as)
		}
	}
	for name := range alongside {
		if _, ok := names[name]; !ok {
			t.Errorf("the packer stages %s and stagedNames does not list it", name)
		}
	}
	if len(names) != len(binaries)+len(alongside) {
		t.Errorf("stagedNames has %d entries and the packer stages %d",
			len(names), len(binaries)+len(alongside))
	}
}

// TestTheLinuxLauncherPicksABinaryAndKeepsStdoutClean runs the launcher
// rather than reading it.
//
// It is the one file in the bundle that is not a binary, and it stands
// where the manifest cannot: a manifest names a command per platform and
// has no key for the architecture, so Linux gets a shell script and the
// choice is made at start-up. Two of its properties are load-bearing and
// neither is visible in a review. It must `exec` rather than call, or a
// shell sits in the middle of the stdio the MCP session runs over. And
// on an architecture the bundle does not carry, its complaint must go to
// STDERR: a line of English on stdout corrupts the JSON-RPC session
// before the client's first request completes, which reads to the user
// as the server being broken rather than absent.
//
// The architecture is faked with a uname earlier on PATH, so the same
// three cases run on any machine.
func TestTheLinuxLauncherPicksABinaryAndKeepsStdoutClean(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the launcher is /bin/sh, and it is not the Windows entry point")
	}
	dir := t.TempDir()
	launcher := filepath.Join(dir, "launch-linux.sh")
	body, err := os.ReadFile(filepath.Join("..", "..", alongside[linuxLauncher]))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(launcher, body, 0o700); err != nil {
		t.Fatal(err)
	}
	// The names come from the packer, so a rename that broke the pairing
	// fails here rather than on somebody's machine. Read from `binaries`
	// and not written out: a review pointed out that this comment was
	// false while the names were literals, since the test then staged
	// what it had typed and the launcher looked for what IT had typed,
	// and the two agreed with each other rather than with the packer.
	for _, b := range binaries {
		if b.platform != "" {
			continue
		}
		name := strings.TrimPrefix(b.as, "server/")
		script := "#!/bin/sh\necho ran " + name + "\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	fake := filepath.Join(dir, "fake")
	if err := os.Mkdir(fake, 0o700); err != nil {
		t.Fatal(err)
	}
	uname := "#!/bin/sh\necho \"$FAKE_ARCH\"\n"
	if err := os.WriteFile(filepath.Join(fake, "uname"), []byte(uname), 0o700); err != nil {
		t.Fatal(err)
	}

	run := func(arch string) (stdout, stderr string, code int) {
		cmd := exec.Command("/bin/sh", launcher)
		cmd.Env = append(os.Environ(), "FAKE_ARCH="+arch, "PATH="+fake+":"+os.Getenv("PATH"))
		var out, errOut bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errOut
		err := cmd.Run()
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return out.String(), errOut.String(), exit.ExitCode()
		}
		if err != nil {
			t.Fatalf("running the launcher for %s: %v", arch, err)
		}
		return out.String(), errOut.String(), 0
	}

	for _, c := range []struct{ arch, want string }{
		{"x86_64", "google-drive-mcp-linux-amd64"},
		{"amd64", "google-drive-mcp-linux-amd64"},
		{"aarch64", "google-drive-mcp-linux-arm64"},
		{"arm64", "google-drive-mcp-linux-arm64"},
	} {
		stdout, stderr, code := run(c.arch)
		if code != 0 {
			t.Errorf("%s: exit %d, stderr %q", c.arch, code, stderr)
		}
		if !strings.Contains(stdout, c.want) {
			t.Errorf("%s ran %q, want %s", c.arch, strings.TrimSpace(stdout), c.want)
		}
	}

	stdout, stderr, code := run("riscv64")
	if code == 0 {
		t.Error("an architecture the bundle does not carry exited 0")
	}
	if stdout != "" {
		t.Errorf("the launcher wrote %q to stdout, which is the JSON-RPC channel", stdout)
	}
	if !strings.Contains(stderr, "riscv64") {
		t.Errorf("the complaint does not name the architecture: %q", stderr)
	}

	// A binary missing from the bundle is the other way this ends, and
	// it must end the same way: nothing on stdout.
	if err := os.Remove(filepath.Join(dir, "google-drive-mcp-linux-amd64")); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code = run("x86_64")
	if code == 0 {
		t.Error("a missing binary exited 0")
	}
	if stdout != "" {
		t.Errorf("the launcher wrote %q to stdout for a missing binary", stdout)
	}
	if !strings.Contains(stderr, "google-drive-mcp-linux-amd64") {
		t.Errorf("the complaint does not name the missing binary: %q", stderr)
	}
}

// TestTheLauncherIsHeldToTheStagedNames. The launcher picks a binary by
// name from a shell script no manifest mentions, so checkManifest cannot
// see those names: renaming a staged Linux binary passed every gate and
// every test until this check existed, and the bundle would then have
// failed for every Linux user.
//
// It swaps the packer's own list rather than a copy, because the check's
// whole job is to compare the launcher against what the packer really
// stages — a fixture list would be two literals agreeing with each other
// again, which is the mistake this file has already made once.
func TestTheLauncherIsHeldToTheStagedNames(t *testing.T) {
	t.Chdir("../..")
	if problems := checkLauncher(); len(problems) > 0 {
		t.Fatalf("the committed launcher does not name what the packer stages:\n%s",
			strings.Join(problems, "\n"))
	}

	original := binaries
	defer func() { binaries = original }()
	renamed := slices.Clone(binaries)
	for i := range renamed {
		if renamed[i].platform == "" {
			renamed[i].as = "server/renamed-" + filepath.Base(renamed[i].as)
		}
	}
	binaries = renamed

	problems := checkLauncher()
	if len(problems) == 0 {
		t.Fatal("a renamed Linux binary was accepted")
	}
	if !strings.Contains(strings.Join(problems, "\n"), "renamed-") {
		t.Errorf("the report does not name the file:\n%s", strings.Join(problems, "\n"))
	}
}

// TestThePlatformsGoreleaserBuildsAreStaged. The two lists live in
// different files and only one direction rots loudly: a platform REMOVED
// from the matrix fails at pack time when the glob finds nothing, and a
// platform ADDED to it is simply absent from the bundle, silently, for as
// long as nobody looks.
func TestThePlatformsGoreleaserBuildsAreStaged(t *testing.T) {
	t.Chdir("../..")
	if problems := checkBuildMatrix(); len(problems) > 0 {
		t.Fatalf("the bundle does not cover what goreleaser builds:\n%s", strings.Join(problems, "\n"))
	}

	// And the matrix is really being read, rather than an empty list
	// agreeing with an empty list.
	source, err := os.ReadFile(".goreleaser.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if got := matrixValues(string(source), "goos"); len(got) < 3 {
		t.Errorf("read %v as the goos matrix; that is too few to be the real one", got)
	}
}

// What the manifest says about ITSELF, which is the half neither the
// staged-tree checks above nor a schema can make: whether the reference
// is pinned to bytes rather than to a branch, and whether the version is
// one somebody checked.
//
// Each case below is well formed, and four of the six pass every check
// that holds the document against itself — which is the point. A 0.2
// manifest citing a 0.2 schema is stale and entirely consistent.
func TestTheManifestsOwnDeclarationIsHeld(t *testing.T) {
	const pinned = "https://raw.githubusercontent.com/anthropics/mcpb/v2.1.2/schemas/mcpb-manifest-v0.3.schema.json"
	const bySHA = "https://raw.githubusercontent.com/anthropics/mcpb/" +
		"0123456789abcdef0123456789abcdef01234567/schemas/mcpb-manifest-v0.3.schema.json"

	// A commit SHA is the other ref that cannot move, and the stronger
	// of the two; refusing it would push somebody back to a branch.
	for _, ok := range []string{pinned, bySHA} {
		if problems := declarationProblems(ok, "0.3", "https://example.invalid/issues"); len(problems) > 0 {
			t.Fatalf("a well-formed declaration was refused:\n%s", strings.Join(problems, "\n"))
		}
	}

	cases := []struct{ name, schema, version, support, want string }{
		{"no $schema", "", "0.3", "x", "no $schema"},
		{
			"the unpinned dist path",
			"https://raw.githubusercontent.com/anthropics/mcpb/main/dist/mcpb-manifest.schema.json",
			"0.3", "x", "not upstream's published",
		},
		{
			"served from a branch",
			"https://raw.githubusercontent.com/anthropics/mcpb/main/schemas/mcpb-manifest-v0.3.schema.json",
			"0.3", "x", "can be re-pointed",
		},
		{
			"a partial tag, which upstream re-points",
			"https://raw.githubusercontent.com/anthropics/mcpb/v2.1/schemas/mcpb-manifest-v0.3.schema.json",
			"0.3", "x", "can be re-pointed",
		},
		{
			"the right file from another host",
			"https://example.invalid/schemas/mcpb-manifest-v0.3.schema.json",
			"0.3", "x", "not upstream's published",
		},
		{
			"a pinned schema that disagrees with manifest_version",
			pinned, "0.4", "x", "cannot claim one version",
		},
		{
			"stale, and entirely self-consistent",
			"https://raw.githubusercontent.com/anthropics/mcpb/v2.1.2/schemas/mcpb-manifest-v0.2.schema.json",
			"0.2", "x", "this repository has checked 0.3",
		},
		{"no support URL", pinned, "0.3", "", "no support URL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			problems := declarationProblems(tc.schema, tc.version, tc.support)
			if len(problems) == 0 {
				t.Fatalf("%s was accepted", tc.name)
			}
			if !strings.Contains(strings.Join(problems, "\n"), tc.want) {
				t.Fatalf("wanted %q, got:\n%s", tc.want, strings.Join(problems, "\n"))
			}
		})
	}
}

// The floor compares numbers. As text "0.10" sorts before "0.3", which
// stays right until the tenth minor version and then reads like the
// manifest's fault.
func TestTheFloorComparesVersionsNumerically(t *testing.T) {
	for _, c := range []struct {
		version, floor string
		want           bool
	}{
		{"0.3", "0.3", false},
		{"0.2", "0.3", true},
		{"0.10", "0.3", false},
		{"0.9", "1.0", true},
		{"nonsense", "0.3", true},
	} {
		if got := olderThan(c.version, c.floor); got != c.want {
			t.Errorf("olderThan(%q, %q) = %v, want %v", c.version, c.floor, got, c.want)
		}
	}
}

// The committed manifest meets the floor, stated rather than read from
// the file: a test taking its expected value from the thing under test
// passes on any value.
func TestTheCommittedManifestMeetsTheFloor(t *testing.T) {
	t.Chdir("../..")
	manifest, err := readManifest(mcpbManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := manifest["manifest_version"].(string); got != "0.3" {
		t.Fatalf("the committed manifest declares %q; this repository has checked 0.3", got)
	}
}

// committedManifest is the committed manifest with one edit applied.
func committedManifest(t *testing.T, edit func(map[string]any)) map[string]any {
	t.Helper()
	manifest, err := readManifest(filepath.Join("..", "..", mcpbManifestPath))
	if err != nil {
		t.Fatal(err)
	}
	edit(manifest)
	return manifest
}

// dig is the object at a path in a decoded manifest.
func dig(doc map[string]any, path ...string) map[string]any {
	for _, key := range path {
		doc = doc[key].(map[string]any)
	}
	return doc
}

// Each rule the bundle's settings are held to, broken once in the
// committed manifest.
func TestTheBundleSettingsRefuseEachBreak(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(map[string]any)
		want string
	}{
		{"a switch the bundle turns on", func(d map[string]any) {
			dig(d, "user_config", "enable_destructive")["default"] = true
		}, "the bundle's defaults configure the server otherwise than no environment does, in EnableDestructive;"},
		{"a required switch the bundle turns on", func(d map[string]any) {
			dig(d, "user_config", "enable_destructive")["required"] = true
			dig(d, "user_config", "enable_destructive")["default"] = true
		}, "the bundle's defaults configure the server otherwise than no environment does, in EnableDestructive;"},
		{"a switch asked as text", func(d map[string]any) {
			dig(d, "user_config", "read_only")["type"] = "string"
			dig(d, "user_config", "read_only")["default"] = "false"
		}, "user_config.read_only is a string; GDRIVE_READ_ONLY is a switch, so ask for a boolean"},
		{"an optional key with no default", func(d map[string]any) {
			delete(dig(d, "user_config", "local_dir"), "default")
		}, "user_config.local_dir is optional with no default"},
		{"a setting the bundle no longer asks for", func(d map[string]any) {
			delete(dig(d, "user_config"), "activity")
			delete(dig(d, "server", "mcp_config", "env"), "GDRIVE_ACTIVITY")
		}, "user_config does not ask for activity, which sets GDRIVE_ACTIVITY"},
		{"a setting spent under another key", func(d map[string]any) {
			dig(d, "server", "mcp_config", "env")["GDRIVE_PROFILE"] = "${user_config.sharing}"
		}, `mcp_config.env.GDRIVE_PROFILE is "${user_config.sharing}", want "${user_config.profile}"`},
		{"a key no setting has", func(d map[string]any) {
			dig(d, "user_config")["verbose"] = map[string]any{
				"type": "boolean", "title": "Verbose", "description": "d", "default": false}
		}, "user_config asks for verbose, which is no setting's key"},
		{"a variable the bundle leaves out", func(d map[string]any) {
			dig(d, "server", "mcp_config", "env")["GDRIVE_LOG_LEVEL"] = "debug"
		}, "mcp_config.env sets GDRIVE_LOG_LEVEL, which the bundle leaves out: diagnostics"},
		{"a variable the server does not read", func(d map[string]any) {
			dig(d, "server", "mcp_config", "env")["GDRIVE_NOTHING"] = "x"
		}, "mcp_config.env sets GDRIVE_NOTHING, which the server does not read"},
		{"a default that configures the server", func(d map[string]any) {
			dig(d, "user_config", "sharing")["default"] = "off"
		}, "the bundle's defaults configure the server otherwise than no environment does, in Sharing;"},
		{"a default the server refuses", func(d map[string]any) {
			dig(d, "user_config", "max_download")["default"] = "lots"
		}, `the environment the bundle's defaults give the server is refused: config: invalid: max download "lots"`},
		{"a required key's default the server refuses", func(d map[string]any) {
			dig(d, "user_config", "local_dir")["required"] = true
			dig(d, "user_config", "local_dir")["default"] = "downloads"
		}, `the environment the bundle's defaults give the server is refused: config: invalid: local dir "downloads"`},
		{"a list default", func(d map[string]any) {
			dig(d, "user_config", "profile")["default"] = []any{"work"}
		}, "user_config.profile defaults to a list, which the defaults check cannot build an environment from"},
		{"base args missing", func(d map[string]any) {
			delete(dig(d, "server", "mcp_config"), "args")
		}, "mcp_config.args is missing"},
		{"base args null", func(d map[string]any) {
			dig(d, "server", "mcp_config")["args"] = nil
		}, "mcp_config.args is null"},
		{"base args with a flag", func(d map[string]any) {
			dig(d, "server", "mcp_config")["args"] = []any{"--enable-destructive=true"}
		}, "mcp_config.args is not []"},
		{"an empty override args", func(d map[string]any) {
			dig(d, "server", "mcp_config", "platform_overrides", "linux")["args"] = []any{}
		}, "platform_overrides.linux.args is set"},
		{"an empty override env", func(d map[string]any) {
			dig(d, "server", "mcp_config", "platform_overrides", "win32")["env"] = map[string]any{}
		}, "platform_overrides.win32.env is set"},
		{"a profile field that does not say what a name is", func(d map[string]any) {
			dig(d, "user_config", "profile")["description"] = "Which stored login to use."
		}, `user_config.profile does not say "up to 64 characters from a to z, 0 to 9, - and _, starting with a ` +
			`letter or digit", which is what GDRIVE_PROFILE accepts`},
		{"a sharing field that does not list the modes", func(d map[string]any) {
			dig(d, "user_config", "sharing")["description"] = "Which sharing tools to register."
		}, `user_config.sharing does not say "all or off", which is what GDRIVE_SHARING accepts`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			problems := settingProblems(committedManifest(t, tc.edit), config.Vars(), bundleLeftOut)
			if !slices.ContainsFunc(problems, func(p string) bool { return strings.Contains(p, tc.want) }) {
				t.Errorf("problems:\n%s\nwant one containing %q", strings.Join(problems, "\n"), tc.want)
			}
		})
	}
}

// The bundle asks for every variable the server reads, less those left
// out with a reason. A new one fails until the manifest asks for it, a
// reason for one the server does not read fails, and so does a bundle
// that asks for no setting whose field is held to what it accepts.
func TestTheBundleAsksForEverySettingNotLeftOut(t *testing.T) {
	asked, problems := askedFrom(config.Vars(), bundleLeftOut)
	var names []string
	for _, v := range asked {
		names = append(names, v.Name)
	}
	want := []string{"GDRIVE_PROFILE", "GDRIVE_READ_ONLY", "GDRIVE_ENABLE_DESTRUCTIVE", "GDRIVE_SHARING",
		"GDRIVE_LOCAL_DIR", "GDRIVE_MAX_DOWNLOAD", "GDRIVE_LABELS", "GDRIVE_ACTIVITY", "GDRIVE_CLIENT_SECRET"}
	if !slices.Equal(names, want) || len(problems) != 0 {
		t.Fatalf("asked %v with problems %v, want %v and none", names, problems, want)
	}

	manifest := committedManifest(t, func(map[string]any) {})
	got := strings.Join(settingProblems(manifest, append(config.Vars(), config.Var{Name: "GDRIVE_NEW"}), bundleLeftOut), "\n")
	if !strings.Contains(got, "user_config does not ask for new, which sets GDRIVE_NEW") {
		t.Errorf("a setting neither asked for nor left out passed: %s", got)
	}
	leftOut := maps.Clone(bundleLeftOut)
	leftOut["GDRIVE_GONE"] = "removed"
	got = strings.Join(settingProblems(manifest, config.Vars(), leftOut), "\n")
	if !strings.Contains(got, "bundleLeftOut names GDRIVE_GONE, which the server does not read") {
		t.Errorf("a reason for a setting the server does not read passed: %s", got)
	}

	// A bundle that drops the profile field is told so once.
	dropped := committedManifest(t, func(d map[string]any) {
		delete(dig(d, "user_config"), "profile")
		delete(dig(d, "server", "mcp_config", "env"), "GDRIVE_PROFILE")
	})
	if p := slices.DeleteFunc(settingProblems(dropped, config.Vars(), bundleLeftOut), func(p string) bool {
		return !strings.Contains(p, "GDRIVE_PROFILE")
	}); !slices.Equal(p, []string{"user_config does not ask for profile, which sets GDRIVE_PROFILE"}) {
		t.Errorf("a dropped profile field gives %q, want the one problem", p)
	}
	// With the setting left out, nothing holds a field to what the server
	// accepts, and that is refused rather than passed in silence.
	for name, rule := range map[string]string{
		"GDRIVE_PROFILE": config.ProfileRule,
		"GDRIVE_SHARING": "all or off",
	} {
		leftOut := maps.Clone(bundleLeftOut)
		leftOut[name] = "left out"
		got := strings.Join(settingProblems(manifest, config.Vars(), leftOut), "\n")
		if want := fmt.Sprintf("the bundle asks for no %s, so nothing holds its field to %q", name, rule); !strings.Contains(got, want) {
			t.Errorf("a bundle with no %s passed its field check:\n%s\nwant %q", name, got, want)
		}
	}
}

// Every list or object default is reported, in key order, and the
// comparison still runs over the rest.
func TestTheDefaultsReportEveryUnreadableDefault(t *testing.T) {
	manifest := committedManifest(t, func(d map[string]any) {
		dig(d, "user_config", "profile")["default"] = []any{"work"}
		dig(d, "user_config", "local_dir")["default"] = map[string]any{"path": "x"}
		dig(d, "user_config", "labels")["default"] = true
	})
	got := defaultsProblems(startDefaults(manifest))
	want := []string{
		"user_config.local_dir defaults to an object, which",
		"user_config.profile defaults to a list, which",
		"the bundle's defaults configure the server otherwise than no environment does, in Labels;",
	}
	if len(got) != len(want) {
		t.Fatalf("got %q, want %d problems starting %q", got, len(want), want)
	}
	for i := range want {
		if !strings.HasPrefix(got[i], want[i]) {
			t.Errorf("problem %d is %q, want one starting %q", i, got[i], want[i])
		}
	}
}

// refusingPair is the committed manifest with GDRIVE_LOCAL_DIR spending
// labels and activity, under a folder that has a directory for every
// pair of their values but both on: so the server refuses labels and
// activity together, and nothing else.
func refusingPair(t *testing.T, edit func(map[string]any)) map[string]any {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"false-false", "true-false", "false-true"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return committedManifest(t, func(d map[string]any) {
		dig(d, "server", "mcp_config", "env")["GDRIVE_LOCAL_DIR"] =
			dir + string(filepath.Separator) + "${user_config.labels}-${user_config.activity}"
		edit(d)
	})
}

// A field the defaults change is named, an unexported one included
// where its type compares; an unexported one that does not is reported
// unnamed, rather than panicking.
func TestTheConfigDiffNamesTheFieldsThatDiffer(t *testing.T) {
	type cfg struct {
		Shown  int
		hidden int
		rest   []string
	}
	for _, tc := range []struct {
		b    cfg
		want []string
	}{
		{cfg{2, 1, nil}, []string{"Shown"}},
		{cfg{1, 2, nil}, []string{"hidden"}},
		{cfg{1, 1, []string{"x"}}, []string{"an unexported field"}},
		{cfg{1, 1, nil}, nil},
	} {
		if got := fieldDiff(cfg{1, 1, nil}, tc.b); !slices.Equal(got, tc.want) {
			t.Errorf("fieldDiff against %+v = %q, want %q", tc.b, got, tc.want)
		}
	}
}

// Every set of the switches is started, and the refused ones are held to
// the declared list both ways, each defect reported once. A declared key
// is a switch with a title, and its description names the others'.
func TestTheBundleRefusalsAreDeclared(t *testing.T) {
	asked, _ := askedFrom(config.Vars(), bundleLeftOut)
	refusals := func(manifest map[string]any, declared [][]string) []string {
		return refusalProblems(manifest, asked, declared, startDefaults(manifest).err)
	}
	const undeclared = "the server refuses activity, labels together, and bundleRefused does not declare it"
	if got := refusals(refusingPair(t, func(map[string]any) {}), nil); len(got) != 1 || !strings.HasPrefix(got[0], undeclared) {
		t.Errorf("an undeclared refusal gives %q, want the one problem %q", got, undeclared)
	}
	// Declared, and each field naming the other's title, it passes; a
	// title is named without the space around it.
	named := func(d map[string]any) {
		dig(d, "user_config", "labels")["description"] = "Refused with Activity on."
		dig(d, "user_config", "activity")["description"] = "Refused with Workspace labels on."
		dig(d, "user_config", "activity")["title"] = " Activity "
	}
	if got := refusals(refusingPair(t, named), [][]string{{"activity", "labels"}}); len(got) != 0 {
		t.Errorf("a declared refusal the fields name gives %q, want nothing", got)
	}
	got := refusals(refusingPair(t, func(map[string]any) {}), [][]string{{"activity", "labels"}})
	want := []string{
		`user_config.activity does not name "Workspace labels", which the server refuses it with`,
		`user_config.labels does not name "Activity", which the server refuses it with`,
	}
	if !slices.Equal(got, want) {
		t.Errorf("a declared refusal the fields do not name gives %q, want %q", got, want)
	}

	manifest := committedManifest(t, func(map[string]any) {})
	const starts = "bundleRefused declares that the server refuses enable_destructive, read_only together, and it starts with them"
	if got := refusals(manifest, [][]string{{"enable_destructive", "read_only"}}); !slices.Contains(got, starts) ||
		len(slices.DeleteFunc(got, func(p string) bool { return p != starts })) != 1 {
		t.Errorf("a declared refusal the server starts with gives %q, want %q once", got, starts)
	}
	// When the defaults do not start, no set is started: the defaults
	// check reports it.
	if got := refusalProblems(manifest, asked, [][]string{{"enable_destructive", "read_only"}},
		errors.New("the defaults do not start")); slices.Contains(got, starts) {
		t.Errorf("a set was started beside defaults that do not start: %q", got)
	}
	got = refusals(manifest, [][]string{{"profile", "read_only"}})
	if !slices.Contains(got, "bundleRefused names profile, which is not a switch the bundle asks for") {
		t.Errorf("a declared key that is not a switch gives %q", got)
	}

	// read_only is in both sets, and a missing title is one problem, not
	// one per set, whether or not the defaults start.
	untitled := committedManifest(t, func(d map[string]any) { dig(d, "user_config", "read_only")["title"] = " " })
	for _, defaultsErr := range []error{nil, errors.New("the defaults do not start")} {
		got := refusalProblems(untitled, asked, [][]string{{"labels", "read_only"}, {"activity", "read_only"}}, defaultsErr)
		if n := len(slices.DeleteFunc(got, func(p string) bool {
			return !strings.HasPrefix(p, "user_config.read_only has no title")
		})); n != 1 {
			t.Errorf("defaults error %v: an untitled read_only is reported %d times, want 1", defaultsErr, n)
		}
	}
}

// A switch in a set is on, whatever its default: a required one with
// none, or one with a list, is not left out, which would hide a refusal.
func TestTheRefusalsTurnASwitchOnWhateverItsDefault(t *testing.T) {
	asked, _ := askedFrom(config.Vars(), bundleLeftOut)
	const want = "the server refuses activity, labels together, and bundleRefused does not declare it"
	for name, edit := range map[string]func(map[string]any){
		"required with no default": func(d map[string]any) {
			dig(d, "user_config", "labels")["required"] = true
			delete(dig(d, "user_config", "labels"), "default")
		},
		"a list default": func(d map[string]any) { dig(d, "user_config", "labels")["default"] = []any{"true"} },
	} {
		manifest := refusingPair(t, edit)
		got := refusalProblems(manifest, asked, nil, startDefaults(manifest).err)
		if !slices.ContainsFunc(got, func(p string) bool { return strings.HasPrefix(p, want) }) {
			t.Errorf("%s: got %q, want one starting %q", name, got, want)
		}
	}
}

// The launcher's install hint names the module go.mod declares, read
// under Windows line endings too. The path moves with each major
// version, so a hint left behind sends somebody to an old one.
func TestTheLauncherInstallHintIsGoModsModule(t *testing.T) {
	const hint = `echo "no binary." "Install with: go install example.com/m/v3/cmd/google-drive-mcp@latest" >&2`
	for _, goMod := range []string{"module example.com/m/v3\n\ngo 1.27\n", "module example.com/m/v3\r\n\r\ngo 1.27\r\n"} {
		if problems := installHintProblems(hint, goMod); len(problems) > 0 {
			t.Errorf("go.mod %q: %v", goMod, problems)
		}
	}
	for _, tc := range []struct{ name, launcher, goMod, want string }{
		{"a hint without the major version", strings.Replace(hint, "/v3", "", 1), "module example.com/m/v3\n",
			`the launcher says "go install example.com/m/cmd/google-drive-mcp@latest"`},
		{"no hint at all", "exit 1", "module example.com/m/v3\n", "gives no install hint"},
		{"no module line", hint, "go 1.27\n", "go.mod names no module"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.Join(installHintProblems(tc.launcher, tc.goMod), "\n")
			if !strings.Contains(got, tc.want) {
				t.Errorf("problems %q, want %q", got, tc.want)
			}
		})
	}
}
