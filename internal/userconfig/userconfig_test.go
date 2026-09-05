package userconfig

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	t.Setenv(EnvDir, t.TempDir())
	if _, err := Load("default"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Load before Save = %v, want ErrNotFound", err)
	}
	want := Config{
		ClientSecretPath: "/somewhere/client_secret.json",
		AccountEmail:     "person@example.com",
		TokenStore:       "keyring",
		Scopes:           []string{"https://www.googleapis.com/auth/drive"},
	}
	if err := Save("default", want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load("default")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.AccountEmail != want.AccountEmail || got.TokenStore != want.TokenStore || len(got.Scopes) != 1 {
		t.Fatalf("round trip lost data: %+v", got)
	}
	if got.UpdatedAt.IsZero() {
		t.Error("Save did not stamp UpdatedAt")
	}
}

func TestSaveIsOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits differ on Windows")
	}
	t.Setenv(EnvDir, t.TempDir())
	if err := Save("default", Config{AccountEmail: "person@example.com"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	p, _ := Path("default")
	info, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("config file mode = %o, want 600", perm)
	}
}

func TestProfilesAreSeparate(t *testing.T) {
	base := t.TempDir()
	t.Setenv(EnvDir, base)
	if err := Save("default", Config{AccountEmail: "one@example.com"}); err != nil {
		t.Fatalf("Save default: %v", err)
	}
	if err := Save("work", Config{AccountEmail: "two@example.com"}); err != nil {
		t.Fatalf("Save work: %v", err)
	}
	d, _ := Load("default")
	w, _ := Load("work")
	if d.AccountEmail == w.AccountEmail {
		t.Fatal("profiles share a config file")
	}
	dir, _ := ProfileDir("work")
	if dir != filepath.Join(base, "profiles", "work") {
		t.Errorf("work profile dir = %q", dir)
	}
	if dir, _ := ProfileDir(""); dir != base {
		t.Errorf("empty profile should mean the base dir, got %q", dir)
	}
}

func TestRemove(t *testing.T) {
	t.Setenv(EnvDir, t.TempDir())
	if err := Remove("default"); err != nil {
		t.Fatalf("Remove of a missing file should be fine: %v", err)
	}
	if err := Save("default", Config{}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := Remove("default"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := Load("default"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after Remove, Load = %v", err)
	}
}

func TestLoadRejectsBrokenJSON(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvDir, dir)
	p, _ := Path("default")
	if err := os.WriteFile(p, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := Load("default"); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("Load of broken JSON = %v, want a parse error", err)
	}
}

func TestPathHelpers(t *testing.T) {
	base := t.TempDir()
	t.Setenv(EnvDir, base)
	cs, _ := DefaultClientSecretPath("default")
	if cs != filepath.Join(base, "client_secret.json") {
		t.Errorf("client secret path = %q", cs)
	}
	tok, _ := TokenFilePath("work")
	if tok != filepath.Join(base, "profiles", "work", "token.json") {
		t.Errorf("token path = %q", tok)
	}
}
