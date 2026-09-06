package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	// and a struct that modelled the schema would silently drop the rest.
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
		"server/google-drive-mcp":     "somewhere",
		"server/linux-launch.sh":      "somewhere",
		"server/google-drive-mcp.exe": "somewhere",
	}
	good := func() map[string]any {
		return map[string]any{
			"$schema": "s", "manifest_version": "0.3", "name": "n",
			"version": "1.0.0", "description": "d", "author": map[string]any{},
			"user_config": map[string]any{"client_secret": map[string]any{}},
			"server": map[string]any{
				"type":        "binary",
				"entry_point": "server/google-drive-mcp",
				"mcp_config": map[string]any{
					"command": "${__dirname}/server/google-drive-mcp",
					"env":     map[string]any{"GDRIVE_CLIENT_SECRET": "${user_config.client_secret}"},
					"platform_overrides": map[string]any{
						"linux": map[string]any{"command": "${__dirname}/server/linux-launch.sh"},
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
			name: "an override for a platform the bundle does not claim",
			break_: func(m map[string]any) {
				m["compatibility"] = map[string]any{"platforms": []any{"darwin", "win32"}}
			},
			want: "platform_overrides.linux is an override for a platform compatibility.platforms does not claim",
		},
		{
			name:   "a server that is not a binary",
			break_: func(m map[string]any) { m["server"].(map[string]any)["type"] = "node" },
			want:   "this bundle ships binaries",
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
		"server/google-drive-mcp": src,
		"LICENSE":                 src,
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
	if modes["server/google-drive-mcp"] != 0o755 {
		t.Errorf("the server binary is %o, want 755", modes["server/google-drive-mcp"])
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
	launcher := filepath.Join(dir, "linux-launch.sh")
	body, err := os.ReadFile(filepath.Join("..", "..", alongside["server/linux-launch.sh"]))
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
		{"x86_64", "google-drive-mcp-amd64"},
		{"amd64", "google-drive-mcp-amd64"},
		{"aarch64", "google-drive-mcp-arm64"},
		{"arm64", "google-drive-mcp-arm64"},
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
	if err := os.Remove(filepath.Join(dir, "google-drive-mcp-amd64")); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code = run("x86_64")
	if code == 0 {
		t.Error("a missing binary exited 0")
	}
	if stdout != "" {
		t.Errorf("the launcher wrote %q to stdout for a missing binary", stdout)
	}
	if !strings.Contains(stderr, "google-drive-mcp-amd64") {
		t.Errorf("the complaint does not name the missing binary: %q", stderr)
	}
}
