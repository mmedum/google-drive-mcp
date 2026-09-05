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

// The paths below are the ones a person has to be able to trust: where a
// profile's files live, and that a write either replaces the file whole
// or leaves the old one. Each was reachable and untested until the
// coverage floor started deriving its own package list.

func TestBaseDirFallsBackToTheUserConfigDir(t *testing.T) {
	t.Setenv(EnvDir, "")
	got, err := BaseDir()
	if err != nil {
		t.Fatalf("BaseDir: %v", err)
	}
	want, err := os.UserConfigDir()
	if err != nil {
		t.Skipf("this machine has no user config dir: %v", err)
	}
	if got != filepath.Join(want, AppDir) {
		t.Errorf("BaseDir() = %q, want it under %q", got, want)
	}
}

func TestANamedProfileLivesBesideTheDefaultOne(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvDir, dir)

	base, err := ProfileDir(DefaultProfile)
	if err != nil {
		t.Fatalf("ProfileDir: %v", err)
	}
	if base != dir {
		t.Errorf("the default profile lives at %q, want %q", base, dir)
	}
	// The empty name is the default profile, so that a caller which never
	// set one does not get a directory called "".
	if empty, err := ProfileDir(""); err != nil || empty != dir {
		t.Errorf("ProfileDir(\"\") = %q (%v), want %q", empty, err, dir)
	}

	named, err := ProfileDir("work")
	if err != nil {
		t.Fatalf("ProfileDir: %v", err)
	}
	if named != filepath.Join(dir, "profiles", "work") {
		t.Errorf("a named profile lives at %q", named)
	}
	for name, fn := range map[string]func(string) (string, error){
		"config.json":        Path,
		"client_secret.json": DefaultClientSecretPath,
		"token.json":         TokenFilePath,
	} {
		got, err := fn("work")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got != filepath.Join(named, name) {
			t.Errorf("%s is at %q, want it in the profile's own directory", name, got)
		}
	}
}

func TestSaveReplacesTheFileWholeAndOwnerOnly(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvDir, dir)

	if err := Save("work", Config{AccountEmail: "person@example.com"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	p, _ := Path("work")
	info, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("config written %o, want 0600: it names the account", perm)
	}
	// The temporary file the write goes through must not survive it.
	if _, err := os.Stat(p + ".tmp"); !os.IsNotExist(err) {
		t.Error("the temporary file was left behind")
	}

	// A second save replaces the first rather than merging with it.
	if err := Save("work", Config{TokenStore: "keyring"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load("work")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.AccountEmail != "" || got.TokenStore != "keyring" {
		t.Errorf("the second save left %+v, want only what it wrote", got)
	}
	if got.UpdatedAt.IsZero() {
		t.Error("Save recorded no time, so nothing can tell a stale profile from a fresh one")
	}
}

func TestRemoveIsQuietAboutWhatIsNotThere(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvDir, dir)

	// Removing a profile that was never written is how logout behaves on
	// a machine that never logged in, and it is not a failure.
	if err := Remove("work"); err != nil {
		t.Errorf("Remove of a missing profile: %v", err)
	}
	if err := Save("work", Config{AccountEmail: "person@example.com"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := Remove("work"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	p, _ := Path("work")
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Error("the config file is still there")
	}
}

func TestEveryPathReportsAMachineWithNoConfigDirectory(t *testing.T) {
	// os.UserConfigDir fails when the environment names nowhere to put
	// configuration, which is a real state on a stripped-down container.
	// Every function here funnels through BaseDir, so the failure has to
	// come back from all of them rather than surfacing as an empty path
	// that later reads as the current directory.
	if runtime.GOOS == "windows" {
		t.Skip("the Windows config directory does not come from these variables")
	}
	t.Setenv(EnvDir, "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")
	if _, err := os.UserConfigDir(); err == nil {
		t.Skip("this machine still names a config directory")
	}

	if _, err := BaseDir(); err == nil {
		t.Error("BaseDir found a directory on a machine that has none")
	}
	for name, fn := range map[string]func(string) (string, error){
		"ProfileDir":              ProfileDir,
		"Path":                    Path,
		"DefaultClientSecretPath": DefaultClientSecretPath,
		"TokenFilePath":           TokenFilePath,
	} {
		if got, err := fn("default"); err == nil {
			t.Errorf("%s returned %q with no config directory", name, got)
		}
	}
	if _, err := Load("default"); err == nil {
		t.Error("Load succeeded with no config directory")
	}
	if err := Save("default", Config{}); err == nil {
		t.Error("Save succeeded with no config directory")
	}
	if err := Remove("default"); err == nil {
		t.Error("Remove succeeded with no config directory")
	}
}

func TestSaveReportsADirectoryItCannotCreate(t *testing.T) {
	// The profile directory is created on the way to the file. A path
	// that cannot hold one has to be reported, not written past.
	dir := t.TempDir()
	blocker := filepath.Join(dir, "base")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvDir, blocker)

	if err := Save("work", Config{}); err == nil {
		t.Error("Save reported success writing into a file")
	}
}
