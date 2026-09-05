package credentials

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

// fakeKeyring is an in-memory Backend. A non-nil failWith makes every
// operation fail the way a machine without a secret service does.
type fakeKeyring struct {
	items    map[string]string
	failWith error
}

func newFakeKeyring() *fakeKeyring { return &fakeKeyring{items: map[string]string{}} }

func (f *fakeKeyring) key(service, account string) string { return service + "\x00" + account }

func (f *fakeKeyring) Get(service, account string) (string, error) {
	if f.failWith != nil {
		return "", f.failWith
	}
	v, ok := f.items[f.key(service, account)]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return v, nil
}

func (f *fakeKeyring) Set(service, account, secret string) error {
	if f.failWith != nil {
		return f.failWith
	}
	f.items[f.key(service, account)] = secret
	return nil
}

func (f *fakeKeyring) Delete(service, account string) error {
	if f.failWith != nil {
		return f.failWith
	}
	k := f.key(service, account)
	if _, ok := f.items[k]; !ok {
		return keyring.ErrNotFound
	}
	delete(f.items, k)
	return nil
}

func newStore(t *testing.T, kr Backend) (*Store, *[]string) {
	t.Helper()
	var warnings []string
	s := &Store{
		Profile:  "default",
		Keyring:  kr,
		FilePath: filepath.Join(t.TempDir(), "token.json"),
		Env:      func(string) string { return "" },
		Warn:     func(m string) { warnings = append(warnings, m) },
	}
	return s, &warnings
}

func TestKeyringIsPreferred(t *testing.T) {
	kr := newFakeKeyring()
	s, warnings := newStore(t, kr)
	src, err := s.Save("refresh-token-value")
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if src != SourceKeyring {
		t.Fatalf("Save stored in %q, want keyring", src)
	}
	if _, err := os.Stat(s.FilePath); !errors.Is(err, os.ErrNotExist) {
		t.Error("a keyring save should not leave a plaintext file")
	}
	tok, src, err := s.Resolve()
	if err != nil || tok != "refresh-token-value" || src != SourceKeyring {
		t.Fatalf("Resolve = %q/%q/%v", tok, src, err)
	}
	if len(*warnings) != 0 {
		t.Errorf("unexpected warnings: %v", *warnings)
	}
}

func TestFallsBackToFileWhenKeyringIsBroken(t *testing.T) {
	kr := newFakeKeyring()
	kr.failWith = errors.New("no secret service")
	s, warnings := newStore(t, kr)
	src, err := s.Save("refresh-token-value")
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if src != SourceFile {
		t.Fatalf("Save stored in %q, want file", src)
	}
	if len(*warnings) == 0 || !strings.Contains((*warnings)[0], "plaintext") {
		t.Errorf("a plaintext fallback must warn, got %v", *warnings)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(s.FilePath)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("token file mode = %o, want 600", perm)
		}
	}
	tok, src, err := s.Resolve()
	if err != nil || tok != "refresh-token-value" || src != SourceFile {
		t.Fatalf("Resolve = %q/%q/%v", tok, src, err)
	}
}

func TestEnvironmentOverridesEverything(t *testing.T) {
	kr := newFakeKeyring()
	s, _ := newStore(t, kr)
	if _, err := s.Save("stored"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	s.Env = func(k string) string {
		if k == EnvVar {
			return "from-env"
		}
		return ""
	}
	tok, src, err := s.Resolve()
	if err != nil || tok != "from-env" || src != SourceEnv {
		t.Fatalf("Resolve = %q/%q/%v", tok, src, err)
	}
	// ResolveStored deliberately ignores the override, so logout revokes
	// the token it can actually delete.
	tok, src, err = s.ResolveStored()
	if err != nil || tok != "stored" || src != SourceKeyring {
		t.Fatalf("ResolveStored = %q/%q/%v", tok, src, err)
	}
}

func TestResolveWithNothingStored(t *testing.T) {
	s, _ := newStore(t, newFakeKeyring())
	if _, _, err := s.Resolve(); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Resolve = %v, want ErrNotFound", err)
	}
}

func TestBrokenKeyringAndNoFileReportsBoth(t *testing.T) {
	kr := newFakeKeyring()
	kr.failWith = errors.New("dbus: no session")
	s, _ := newStore(t, kr)
	_, _, err := s.Resolve()
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Resolve = %v, want ErrNotFound", err)
	}
	if !strings.Contains(err.Error(), "dbus") {
		t.Errorf("the keyring failure should be reported too: %v", err)
	}
}

func TestSaveRefusesAnEmptyToken(t *testing.T) {
	s, _ := newStore(t, newFakeKeyring())
	if _, err := s.Save(""); err == nil {
		t.Fatal("an empty token should be refused")
	}
}

func TestSaveToKeyringClearsAStalePlaintextFile(t *testing.T) {
	kr := newFakeKeyring()
	kr.failWith = errors.New("keyring down")
	s, _ := newStore(t, kr)
	if _, err := s.Save("old"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	kr.failWith = nil
	if _, err := s.Save("new"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(s.FilePath); !errors.Is(err, os.ErrNotExist) {
		t.Error("the stale plaintext token file survived a keyring save")
	}
	tok, src, _ := s.Resolve()
	if tok != "new" || src != SourceKeyring {
		t.Errorf("Resolve = %q/%q", tok, src)
	}
}

func TestDeleteRemovesBothStores(t *testing.T) {
	kr := newFakeKeyring()
	s, _ := newStore(t, kr)
	if _, err := s.Save("in-keyring"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := os.WriteFile(s.FilePath, []byte(`{"refresh_token":"in-file"}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := s.Delete(); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, _, err := s.Resolve(); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after Delete, Resolve = %v", err)
	}
	if err := s.Delete(); err != nil {
		t.Fatalf("a second Delete should be fine: %v", err)
	}
}

func TestResolveRejectsABrokenTokenFile(t *testing.T) {
	s, _ := newStore(t, newFakeKeyring())
	if err := os.WriteFile(s.FilePath, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, _, err := s.Resolve()
	if err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("Resolve = %v, want a parse error", err)
	}
}

func TestSaveWithNoStoreConfigured(t *testing.T) {
	s := &Store{Profile: "default", Env: func(string) string { return "" }}
	if _, err := s.Save("token"); err == nil {
		t.Fatal("a store with neither keyring nor file should refuse to save")
	}
}

func TestOSKeyringIsWired(t *testing.T) {
	// The production backend is a thin adapter; this only proves the
	// wiring exists and reports a miss the way the fallback expects.
	kr := OSKeyring()
	if kr == nil {
		t.Fatal("OSKeyring returned nil")
	}
	_, err := kr.Get(ServiceName, "profile-that-does-not-exist-in-any-keyring")
	if err == nil {
		t.Skip("a keyring entry unexpectedly exists on this machine")
	}
}

func TestIsKeyringNotFound(t *testing.T) {
	if !IsKeyringNotFound(keyring.ErrNotFound) {
		t.Error("keyring.ErrNotFound should be recognised")
	}
	if IsKeyringNotFound(errors.New("other")) {
		t.Error("an unrelated error should not be a not-found")
	}
}

func TestEnvDefaultsToTheProcessEnvironment(t *testing.T) {
	t.Setenv(EnvVar, "from-process-env")
	s := &Store{Profile: "default", Keyring: newFakeKeyring()}
	tok, src, err := s.Resolve()
	if err != nil || tok != "from-process-env" || src != SourceEnv {
		t.Fatalf("Resolve = %q/%q/%v", tok, src, err)
	}
}
