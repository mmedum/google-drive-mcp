//go:build integration

// Live tests against a real Google account. They are the only check that
// the fake and Drive agree, and spike A showed why that matters: two of
// this design's stated rules were wrong, and drivetest had implemented
// them faithfully, so the whole suite agreed with itself.
//
//	GDRIVE_INTEGRATION=1 go test -tags=integration ./internal/service/ -v
//
// They read and never write: phase 0 registers no write tools, so there
// is nothing to clean up. Nothing here prints a file name, a folder
// name, an address or an id — a failing test says what shape was wrong,
// not whose data it was.
package service_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mmedum/google-drive-mcp/internal/auth"
	"github.com/mmedum/google-drive-mcp/internal/credentials"
	"github.com/mmedum/google-drive-mcp/internal/gapi"
	"github.com/mmedum/google-drive-mcp/internal/service"
	"github.com/mmedum/google-drive-mcp/internal/userconfig"
)

// live builds a service against the account the profile is logged in to,
// exactly as the server does.
func live(t *testing.T) (*service.Service, *gapi.Client) {
	t.Helper()
	if os.Getenv("GDRIVE_INTEGRATION") != "1" {
		t.Skip("set GDRIVE_INTEGRATION=1 to run against a real account")
	}
	profile := os.Getenv("GDRIVE_PROFILE")
	if profile == "" {
		profile = userconfig.DefaultProfile
	}

	user, err := userconfig.Load(profile)
	if err != nil {
		t.Fatalf("no profile %q; run `google-drive-mcp login` first: %v", profile, err)
	}
	secret := user.ClientSecretPath
	if secret == "" {
		if secret, err = userconfig.DefaultClientSecretPath(profile); err != nil {
			t.Fatalf("locate client secret: %v", err)
		}
	}
	oc, err := auth.LoadClientSecret(secret, auth.Scopes(auth.Access{}))
	if err != nil {
		t.Fatalf("load client secret: %v", err)
	}
	tokenFile, err := userconfig.TokenFilePath(profile)
	if err != nil {
		t.Fatalf("token path: %v", err)
	}
	store := &credentials.Store{Profile: profile, Keyring: credentials.OSKeyring(), FilePath: tokenFile}
	token, _, err := store.Resolve()
	if err != nil {
		t.Fatalf("no credentials; run `google-drive-mcp login`: %v", err)
	}

	ctx := context.Background()
	api := gapi.New(auth.TokenSource(ctx, oc, token, 60*time.Second), gapi.Options{
		Timeout: 60 * time.Second, UserAgent: "google-drive-mcp/integration-test",
	})
	return service.New(api, service.Options{}), api
}

func TestLiveAccount(t *testing.T) {
	_, api := live(t)
	about, err := api.About(context.Background())
	if err != nil {
		t.Fatalf("about.get: %v", err)
	}
	if about.User == nil || about.User.EmailAddress == "" {
		t.Fatal("about.get returned no account")
	}
	if about.StorageQuota == nil || about.StorageQuota.Usage == "" {
		t.Error("about.get returned no storage figures")
	}
	t.Logf("signed in; can create shared drives: %t", about.CanCreateDrives)
}

// TestLiveSearchIsPrefixNotSubstring is the assertion the whole search
// design rests on, made against Drive rather than against our model of
// it. It probes with a name the account actually has, discovered at run
// time, and never prints it.
func TestLiveSearchIsPrefixNotSubstring(t *testing.T) {
	svc, api := live(t)
	ctx := context.Background()

	word := probeWord(t, api)
	hits := func(q service.SearchInput) int {
		t.Helper()
		out, err := svc.Search(ctx, q)
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		return countHits(out)
	}

	whole := hits(service.SearchInput{Name: word, Limit: 100})
	if whole == 0 {
		t.Fatalf("the probe word matched nothing; the fixture discovery is wrong")
	}
	if got := hits(service.SearchInput{Name: word[:3], Limit: 100}); got < whole {
		t.Errorf("a prefix matched %d, fewer than the whole word's %d: prefix matching is broken", got, whole)
	}
	// The claim: a substring that does not start a word matches nothing.
	if got := hits(service.SearchInput{Name: word[2:], Limit: 100}); got != 0 {
		t.Errorf("the middle of a word matched %d items; `contains` is a substring match after all, "+
			"and every query this server builds is wrong about it", got)
	}
	// Case is ignored (spike A, 2026-09-05).
	if got := hits(service.SearchInput{Name: strings.ToUpper(word), Limit: 100}); got != whole {
		t.Errorf("upper case matched %d against %d: `contains` is case-sensitive after all", got, whole)
	}
}

// TestLiveNameEqualsIgnoresCase pins the rule path resolution depends on.
// If Drive ever made `name =` case-sensitive, two siblings differing only
// in case would stop being ambiguous and one would silently win.
func TestLiveNameEqualsIgnoresCase(t *testing.T) {
	svc, api := live(t)
	ctx := context.Background()
	word := probeWord(t, api)

	exact, err := svc.Search(ctx, service.SearchInput{RawQuery: "name = '" + word + "'", Limit: 100})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	folded, err := svc.Search(ctx, service.SearchInput{RawQuery: "name = '" + strings.ToUpper(word) + "'", Limit: 100})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if countHits(exact) != countHits(folded) {
		t.Errorf("`name =` matched %d exactly and %d upper-cased: it is case-sensitive, and "+
			"internal/gapi/drivetest disagrees with Drive", countHits(exact), countHits(folded))
	}
}

func TestLiveRootListingAndPathResolution(t *testing.T) {
	svc, _ := live(t)
	ctx := context.Background()

	out, err := svc.ListFolder(ctx, service.ListFolderInput{Folder: "root", PageSize: 10})
	if err != nil {
		t.Fatalf("list root: %v", err)
	}
	if !strings.HasPrefix(out, "My Drive — ") {
		t.Errorf("the root listing is headed %q; the root is not being recognised as itself",
			strings.SplitN(out, "\n", 2)[0])
	}
	// Folders first is what makes a listing readable; assert the shape
	// rather than the contents.
	lines := strings.Split(strings.TrimSpace(out), "\n")
	seenNonFolder := false
	for _, line := range lines[1:] {
		if strings.HasPrefix(line, "more results:") || strings.TrimSpace(line) == "" {
			continue
		}
		isFolder := strings.HasPrefix(line, "folder ")
		if isFolder && seenNonFolder {
			t.Error("a folder came after a non-folder: the listing is not folders-first")
			break
		}
		if !isFolder {
			seenNonFolder = true
		}
	}
}

func TestLiveSharedDrives(t *testing.T) {
	svc, api := live(t)
	ctx := context.Background()

	about, err := api.About(ctx)
	if err != nil {
		t.Fatalf("about.get: %v", err)
	}
	if !about.CanCreateDrives {
		t.Skip("this account has no shared drives (not a Workspace account)")
	}
	drives, err := svc.Drives(ctx)
	if err != nil {
		t.Fatalf("list drives: %v", err)
	}
	if len(drives) == 0 {
		t.Skip("this account can see no shared drives")
	}
	t.Logf("%d shared drives visible", len(drives))

	// A drive: reference must resolve to the drive's own root, whose id
	// is the drive id.
	res, err := svc.Resolve(ctx, "drive:"+drives[0].ID, service.ResolveOptions{})
	if err != nil {
		t.Fatalf("resolve a shared drive by id: %v", err)
	}
	if res.File.ID != drives[0].ID {
		t.Error("a shared drive's root folder id should be the drive id")
	}
	if loc := svc.Location(ctx, res.File).String(); !strings.Contains(loc, "(shared drive)") {
		t.Errorf("a shared drive's location should say so, got %q", markSafe(loc))
	}
}

func TestLiveMissingIDIsActionable(t *testing.T) {
	svc, _ := live(t)
	_, err := svc.Resolve(context.Background(), "1SyntheticFixtureFileIdAAAAAAAAAAAA", service.ResolveOptions{})
	if err == nil {
		t.Fatal("a synthetic id should not resolve")
	}
	if !strings.HasPrefix(err.Error(), "[not_found]") {
		t.Errorf("class = %q, want not_found", strings.SplitN(err.Error(), " ", 2)[0])
	}
	if !strings.Contains(err.Error(), "resourcekey") {
		t.Error("the message should mention resource keys, the non-obvious reason a real id fails")
	}
}

func TestLiveGenerateIDsAreUsableAndUnique(t *testing.T) {
	_, api := live(t)
	ids, err := api.GenerateIDs(context.Background(), 5)
	if err != nil {
		t.Fatalf("generateIds: %v", err)
	}
	if len(ids) != 5 {
		t.Fatalf("got %d ids, want 5", len(ids))
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			t.Error("generateIds returned a duplicate; idempotent writes depend on it not doing that")
		}
		seen[id] = true
		if len(id) < 15 {
			t.Errorf("a generated id is %d characters, shorter than internal/ref accepts as an id", len(id))
		}
	}
}

// probeWord finds a word from a folder name in this account, to search
// with. It is never printed: a failing test reports shapes and counts.
func probeWord(t *testing.T, api *gapi.Client) string {
	t.Helper()
	list, err := api.ListFiles(context.Background(), gapi.ListQuery{
		Q: "mimeType = 'application/vnd.google-apps.folder' and trashed = false", PageSize: 100,
	})
	if err != nil {
		t.Fatalf("list folders: %v", err)
	}
	for _, f := range list.Files {
		for _, w := range strings.Fields(f.Name) {
			// Long enough to have a middle, and plain enough that its
			// first three letters are a meaningful prefix.
			if len(w) >= 8 && isPlainWord(w) {
				return w
			}
		}
	}
	t.Skip("no folder name in this account is suitable to probe with")
	return ""
}

func isPlainWord(w string) bool {
	for _, r := range w {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return false
		}
	}
	return true
}

// countHits reads the count out of a rendered listing without keeping any
// of the names in it.
func countHits(out string) int {
	head := strings.SplitN(out, "\n", 2)[0]
	fields := strings.Fields(head)
	for i, f := range fields {
		if (f == "hits" || f == "hit") && i > 0 {
			n := 0
			for _, r := range fields[i-1] {
				if r < '0' || r > '9' {
					return 0
				}
				n = n*10 + int(r-'0')
			}
			return n
		}
	}
	return 0
}

// markSafe keeps a failing assertion from printing a name.
func markSafe(s string) string {
	if i := strings.Index(s, "(shared drive)"); i >= 0 {
		return s[i:]
	}
	return "(redacted)"
}
