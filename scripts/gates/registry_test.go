package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTheCommittedEntryObeysTheRegistrysOwnRules runs the gate over the
// file this repository ships.
func TestTheCommittedEntryObeysTheRegistrysOwnRules(t *testing.T) {
	t.Chdir("../..")
	var out bytes.Buffer
	if err := registry(&out, nil); err != nil {
		t.Fatalf("%s: %v\n%s", registryFile, err, out.String())
	}
}

// TestEveryRuleTheRegistryEnforcesInCodeIsRefusedHere.
//
// None of these is in the published schema: they live in the registry's
// own validator, and a document that satisfies the schema and breaks any
// of them is accepted by a linter and rejected at publish time — at the
// end of a release, which is the most expensive place to find out.
func TestEveryRuleTheRegistryEnforcesInCodeIsRefusedHere(t *testing.T) {
	t.Chdir("../..")
	raw, err := os.ReadFile(registryFile)
	if err != nil {
		t.Fatal(err)
	}
	var good map[string]any
	if err := json.Unmarshal(raw, &good); err != nil {
		t.Fatal(err)
	}
	pkg := func(m map[string]any) map[string]any {
		return m["packages"].([]any)[0].(map[string]any)
	}
	cases := []struct {
		name   string
		break_ func(map[string]any)
		want   string
	}{
		{
			name:   "a registryBaseUrl beside the download URL",
			break_: func(m map[string]any) { pkg(m)["registryBaseUrl"] = "https://example.com" },
			want:   "must not have one",
		},
		{
			name:   "a URL with no \"mcp\" anywhere in it",
			break_: func(m map[string]any) { pkg(m)["identifier"] = "https://github.com/a/b/releases/download/v1/x.bundle" },
			want:   `must contain "mcp"`,
		},
		{
			name:   "an http URL",
			break_: func(m map[string]any) { pkg(m)["identifier"] = "http://github.com/a/mcp/releases/download/v1/x.mcpb" },
			want:   "must use https",
		},
		{
			name:   "a URL that is not a release asset",
			break_: func(m map[string]any) { pkg(m)["identifier"] = "https://github.com/a/mcp/blob/main/x.mcpb" },
			want:   "not a release asset URL",
		},
		{
			name:   "a URL somewhere other than github.com",
			break_: func(m map[string]any) { pkg(m)["identifier"] = "https://example.com/a/mcp/releases/download/v1/x.mcpb" },
			want:   "host is",
		},
		{
			name:   "no hash for a client to check",
			break_: func(m map[string]any) { pkg(m)["fileSha256"] = "" },
			want:   "not 64 hex characters",
		},
		{
			name:   "somebody else's namespace",
			break_: func(m map[string]any) { m["name"] = "io.github.someone/google-drive-mcp" },
			want:   "the io.github. namespace is what GitHub OIDC proves",
		},
		{
			name:   "a real version committed to the tree",
			break_: func(m map[string]any) { m["version"] = "1.0.1" },
			want:   `version is "1.0.1"`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var broken map[string]any
			if err := json.Unmarshal(raw, &broken); err != nil {
				t.Fatal(err)
			}
			c.break_(broken)
			body, err := json.Marshal(broken)
			if err != nil {
				t.Fatal(err)
			}
			_, problems := checkRegistryEntry(body, registryPlaceholder)
			if len(problems) == 0 {
				t.Fatal("a broken entry was accepted")
			}
			if !strings.Contains(strings.Join(problems, "\n"), c.want) {
				t.Errorf("the report does not say %q:\n%s", c.want, strings.Join(problems, "\n"))
			}
		})
	}
	_ = good
}

// TestThePublishedEntryTakesItsHashFromTheSignedChecksums, and refuses to
// invent one. A client checks this number before installing, so a wrong
// one is an install that fails for everybody — and a bundle missing from
// checksums.txt is the packing step having silently not run, which is the
// defect a sibling repository shipped and this repository documented
// wrongly for a whole morning.
func TestThePublishedEntryTakesItsHashFromTheSignedChecksums(t *testing.T) {
	t.Chdir("../..")
	dir := t.TempDir()
	const sum = "c7600766c6a4f3125dc9dd5180da6cb50749dbbe883b5b15a0234a85e11f0ec2"
	body := "0000000000000000000000000000000000000000000000000000000000000000  other.tar.gz\n" +
		sum + "  google-drive-mcp_9.9.9.mcpb\n"
	if err := os.WriteFile(filepath.Join(dir, "checksums.txt"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := registryPublish(&out, []string{"v9.9.9", dir}); err != nil {
		t.Fatalf("registry-publish: %v", err)
	}
	var entry map[string]any
	if err := json.Unmarshal(out.Bytes(), &entry); err != nil {
		t.Fatalf("the published entry is not JSON: %v", err)
	}
	if got := entry["version"]; got != "9.9.9" {
		t.Errorf("version = %v, want 9.9.9", got)
	}
	pkg := entry["packages"].([]any)[0].(map[string]any)
	if got := pkg["fileSha256"]; got != sum {
		t.Errorf("fileSha256 = %v, want the sum from checksums.txt", got)
	}
	want := "https://github.com/mmedum/google-drive-mcp/releases/download/v9.9.9/google-drive-mcp_9.9.9.mcpb"
	if got := pkg["identifier"]; got != want {
		t.Errorf("identifier = %v, want %s", got, want)
	}
	// And the committed file is untouched: the release writes its entry
	// to stdout, so a half-finished release cannot leave a real version
	// behind in the tree.
	raw, err := os.ReadFile(registryFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), registryPlaceholder) {
		t.Error("the committed entry no longer carries the placeholder")
	}
}

// TestABundleMissingFromTheChecksumsIsRefused. That file is what cosign
// signs; a bundle absent from it is one the packing step did not produce
// or one nothing covers, and publishing a hash of zeroes for it would
// break every client that checks.
func TestABundleMissingFromTheChecksumsIsRefused(t *testing.T) {
	t.Chdir("../..")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "checksums.txt"),
		[]byte("abc  google-drive-mcp_9.9.9_linux_amd64.tar.gz\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := registryPublish(&bytes.Buffer{}, []string{"9.9.9", dir})
	if err == nil {
		t.Fatal("an entry was published for a bundle nothing had checksummed")
	}
	if !strings.Contains(err.Error(), "not under the signature") {
		t.Errorf("the refusal does not say what it means: %v", err)
	}
}
