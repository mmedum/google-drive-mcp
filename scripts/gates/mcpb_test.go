package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
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
// manifest that is well formed and names files nobody packed.
func TestTheManifestIsCheckedAgainstTheBundle(t *testing.T) {
	packed := map[string]string{
		"server/google-drive-mcp-darwin": "somewhere",
		"server/launch-linux.sh":         "somewhere",
		"server/google-drive-mcp.exe":    "somewhere",
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
			"compatibility":    map[string]any{"claude_desktop": ">=0.10.0"},
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
			break_: func(m map[string]any) { m["server"].(map[string]any)["entry_point"] = "server/typo" },
			want:   "server.entry_point names \"server/typo\"",
		},
		{
			name: "a platform override pointing nowhere",
			break_: func(m map[string]any) {
				cfg := m["server"].(map[string]any)["mcp_config"].(map[string]any)
				cfg["platform_overrides"].(map[string]any)["linux"].(map[string]any)["command"] = "${__dirname}/server/gone.sh"
			},
			want: "platform_overrides.linux.command names \"server/gone.sh\"",
		},
		{
			name: "a user_config key nobody declares",
			break_: func(m map[string]any) {
				cfg := m["server"].(map[string]any)["mcp_config"].(map[string]any)
				cfg["env"].(map[string]any)["GDRIVE_CLIENT_SECRET"] = "${user_config.oauth_json}"
			},
			want: "which user_config does not declare",
		},
		{
			// Composed, with the undeclared key second: every reference
			// in a value is spent, not only the first.
			name: "an undeclared key after a declared one",
			break_: func(m map[string]any) {
				cfg := m["server"].(map[string]any)["mcp_config"].(map[string]any)
				cfg["env"].(map[string]any)["GDRIVE_CLIENT_SECRET"] = "${user_config.client_secret}/${user_config.oauth_json}"
			},
			want: "spends ${user_config.oauth_json}",
		},
		{
			name: "an override for a platform the bundle does not claim",
			break_: func(m map[string]any) {
				m["compatibility"] = map[string]any{"claude_desktop": ">=0.10.0", "platforms": []any{"darwin", "win32"}}
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
				m["compatibility"] = map[string]any{"claude_desktop": ">=0.10.0", "platforms": []any{"darwin", "win32"}}
				cfg := m["server"].(map[string]any)["mcp_config"].(map[string]any)
				delete(cfg["platform_overrides"].(map[string]any), "win32")
			},
			want: "on win32 the bundle would spawn",
		},
		{
			name:   "a server that is not a binary",
			break_: func(m map[string]any) { m["server"].(map[string]any)["type"] = "node" },
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
			break_: func(m map[string]any) { delete(m, "compatibility") },
			want:   `compatibility.claude_desktop is ""; the family's bundles claim ">=0.10.0"`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := good()
			c.break_(m)
			problems := checkManifest(m, packed)
			if len(problems) == 0 {
				t.Fatal("a broken manifest was accepted")
			}
			if !strings.Contains(strings.Join(problems, "\n"), c.want) {
				t.Errorf("the report does not say %q:\n%s", c.want, strings.Join(problems, "\n"))
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
// committed manifest, and the shape that ships passing all of them.
func TestTheBundleSettingsRefuseEachBreak(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(map[string]any)
		want string
	}{
		{name: "the shape that ships", edit: func(map[string]any) {}},
		{"a switch the bundle turns on", func(d map[string]any) {
			dig(d, "user_config", "enable_destructive")["default"] = true
		}, "user_config.enable_destructive is a boolean defaulting to true; GDRIVE_ENABLE_DESTRUCTIVE is a " +
			"switch the server defaults to false"},
		{"a switch asked as text", func(d map[string]any) {
			dig(d, "user_config", "read_only")["type"] = "string"
			dig(d, "user_config", "read_only")["default"] = "false"
		}, `user_config.read_only is a string defaulting to "false"; GDRIVE_READ_ONLY is a switch`},
		{"a switch with no default", func(d map[string]any) {
			delete(dig(d, "user_config", "labels"), "default")
		}, "user_config.labels is a boolean defaulting to null"},
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
		{"a variable the bundle does not ask for", func(d map[string]any) {
			dig(d, "server", "mcp_config", "env")["GDRIVE_LOG_LEVEL"] = "debug"
		}, "mcp_config.env sets GDRIVE_LOG_LEVEL, which the bundle does not ask for"},
		{"a default that configures the server", func(d map[string]any) {
			dig(d, "user_config", "sharing")["default"] = "off"
		}, "the bundle's defaults configure the server otherwise than no environment does"},
		{"a default the server refuses", func(d map[string]any) {
			dig(d, "user_config", "max_download")["default"] = "lots"
		}, `the environment the bundle's defaults give the server is refused: config: invalid: max download "lots"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			problems := settingProblems(committedManifest(t, tc.edit), serverVars())
			if tc.want == "" {
				if len(problems) > 0 {
					t.Fatalf("the shape that ships was refused:\n%s", strings.Join(problems, "\n"))
				}
				return
			}
			if !slices.ContainsFunc(problems, func(p string) bool { return strings.Contains(p, tc.want) }) {
				t.Errorf("problems:\n%s\nwant one containing %q", strings.Join(problems, "\n"), tc.want)
			}
		})
	}
}

// The 3.0.0 bundle's names are refused, so the family's layout cannot
// drift back in one file and not the others.
func TestTheOldLayoutIsRefused(t *testing.T) {
	for _, tc := range []struct{ name, entry, linux, want string }{
		{"the old macOS name", "server/google-drive-mcp", "server/launch-linux.sh",
			`server.entry_point names "server/google-drive-mcp"`},
		{"the old launcher name", "server/google-drive-mcp-darwin", "server/linux-launch.sh",
			`platform_overrides.linux.command names "server/linux-launch.sh"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manifest := committedManifest(t, func(d map[string]any) {
				dig(d, "server")["entry_point"] = tc.entry
				dig(d, "server", "mcp_config", "platform_overrides", "linux")["command"] = "${__dirname}/" + tc.linux
			})
			problems := strings.Join(checkManifest(manifest, stagedNames()), "\n")
			if !strings.Contains(problems, tc.want) {
				t.Errorf("problems:\n%s\nwant %q", problems, tc.want)
			}
		})
	}
}

// Every variable the server reads has a verdict: asked for, or left out
// with a reason. A new one with neither fails, and so does a verdict on
// one the server no longer reads.
func TestEverySettingHasABundleVerdict(t *testing.T) {
	known := serverVars()
	if len(known) < 15 {
		t.Fatalf("the server reads %d variables; this would be checking a stub", len(known))
	}
	manifest := committedManifest(t, func(map[string]any) {})
	if p := settingProblems(manifest, known); len(p) != 0 {
		t.Fatalf("the committed manifest fails: %v", p)
	}
	got := strings.Join(settingProblems(manifest, append(slices.Clone(known), "GDRIVE_NEW")), "\n")
	if !strings.Contains(got, "the server reads GDRIVE_NEW, and the bundle neither asks for it nor says why not") {
		t.Errorf("a setting with no verdict passed: %s", got)
	}
	without := slices.DeleteFunc(slices.Clone(known), func(s string) bool { return s == "GDRIVE_HTTP_TIMEOUT" })
	got = strings.Join(settingProblems(manifest, without), "\n")
	if !strings.Contains(got, "the bundle has a verdict on GDRIVE_HTTP_TIMEOUT, which the server does not read") {
		t.Errorf("a verdict on a setting the server does not read passed: %s", got)
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
