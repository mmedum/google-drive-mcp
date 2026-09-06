package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
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
