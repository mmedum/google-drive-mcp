package service_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/mmedum/google-drive-mcp/v3/internal/gapi/drivetest"
	"github.com/mmedum/google-drive-mcp/v3/internal/service"
)

// emptyFirstPage makes the first files.list answer whose query names
// parent, and asks for a name or not as byName says, an empty page that
// still carries a token, which Drive may send before the end of a list
// (D-16).
func emptyFirstPage(fake *drivetest.Server, parent string, byName bool) {
	sent := false
	fake.Rewrite = func(r *http.Request, status int, body []byte) (int, []byte) {
		if !sent && r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/files") &&
			strings.Contains(r.URL.Query().Get("q"), parent) &&
			strings.Contains(r.URL.Query().Get("q"), "name =") == byName {
			sent = true
			return http.StatusOK, []byte(`{"nextPageToken":"offset-0","files":[]}`)
		}
		return status, body
	}
}

// A page with nothing on it is not the end of a folder: a listing, the
// duplicate check before a create and the names a not-found offers all
// read on to the page after it.
func TestAnEmptyPageIsNotTheEndOfAFolder(t *testing.T) {
	t.Run("a folder listing", func(t *testing.T) {
		svc, fake := setup(t, service.Options{})
		emptyFirstPage(fake, "id-2026-fixture", false)
		out, err := svc.ListFolder(t.Context(), service.ListFolderInput{Folder: "id-2026-fixture", Recursive: true})
		if err != nil || !strings.Contains(out, "Budget.xlsx") {
			t.Errorf("ListFolder = %v:\n%s\nwant the items on the page after the empty one", err, out)
		}
	})
	t.Run("the duplicate check", func(t *testing.T) {
		svc, fake := setup(t, service.Options{})
		emptyFirstPage(fake, "id-2026-fixture", true)
		_, err := svc.CreateFolder(t.Context(), service.CreateFolderInput{Name: "Budget.xlsx", Parent: "id-2026-fixture"})
		if err == nil || !strings.HasPrefix(err.Error(), "[exists]") {
			t.Errorf("a second item named Budget.xlsx: err = %v, want [exists]", err)
		}
	})
	t.Run("a name that is not there", func(t *testing.T) {
		svc, fake := setup(t, service.Options{})
		emptyFirstPage(fake, "id-2026-fixture", false)
		_, err := svc.Resolve(t.Context(), "/Projects/2026/Nothing here", service.ResolveOptions{})
		if err == nil || strings.Contains(err.Error(), "That folder is empty") || !strings.Contains(err.Error(), "Budget.xlsx") {
			t.Errorf("err = %v, want the names on the page after the empty one", err)
		}
	})
}

// A path whose second match is on a later page is [ambiguous], not the
// first match: the lookup reads every page before it decides (D-12).
func TestAMatchOnALaterPageMakesAPathAmbiguous(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddFile("id-budget-second-fixture", "Budget.xlsx",
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "id-2026-fixture")
	split := false
	fake.Rewrite = func(r *http.Request, status int, body []byte) (int, []byte) {
		if split || r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/files") ||
			!strings.Contains(r.URL.Query().Get("q"), "name = 'Budget.xlsx'") {
			return status, body
		}
		split = true
		var page map[string]any
		if err := json.Unmarshal(body, &page); err != nil {
			return status, body
		}
		files, _ := page["files"].([]any)
		if len(files) < 2 {
			return status, body
		}
		page["files"], page["nextPageToken"] = files[:1], "offset-1"
		out, _ := json.Marshal(page)
		return status, out
	}
	_, err := svc.Resolve(t.Context(), "/Projects/2026/Budget.xlsx", service.ResolveOptions{})
	if err == nil || !strings.HasPrefix(err.Error(), "[ambiguous]") ||
		!strings.Contains(err.Error(), "id-budget-fixture") || !strings.Contains(err.Error(), "id-budget-second-fixture") {
		t.Errorf("err = %v, want [ambiguous] naming both", err)
	}
	if !split {
		t.Fatal("the lookup was never split across pages, so this tested nothing")
	}
}
