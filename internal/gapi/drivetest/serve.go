package drivetest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/mmedum/google-drive-mcp/internal/gdrive"
)

// serve routes one request. Paths are the Drive v3 ones under
// /drive/v3, so the client under test builds its URLs exactly as it
// would against Google.
func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.Requests = append(s.Requests, Recorded{
		Method: r.Method, Path: r.URL.Path, Query: r.URL.Query(),
		ResourceKeys: r.Header.Get("X-Goog-Drive-Resource-Keys"),
	})
	fail := s.Fail
	s.mu.Unlock()

	if fail != nil {
		if f := fail(r); f != nil {
			s.injectFailure(w, f)
			return
		}
	}

	path := strings.TrimPrefix(r.URL.Path, "/drive/v3")
	switch {
	case path == "/about" && r.Method == http.MethodGet:
		s.handleAbout(w)
	case path == "/files/generateIds" && r.Method == http.MethodGet:
		s.handleGenerateIDs(w, r)
	case path == "/files" && r.Method == http.MethodGet:
		s.handleList(w, r)
	case strings.HasPrefix(path, "/files/") && strings.HasSuffix(path, "/permissions") && r.Method == http.MethodGet:
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/files/"), "/permissions")
		s.handleListPermissions(w, id)
	case strings.HasPrefix(path, "/files/") && r.Method == http.MethodGet:
		s.handleGet(w, r, strings.TrimPrefix(path, "/files/"))
	case path == "/drives" && r.Method == http.MethodGet:
		s.handleListDrives(w, r)
	default:
		s.errorJSON(w, http.StatusNotFound, "notFound", "the fake does not implement "+r.Method+" "+path)
	}
}

func (s *Server) injectFailure(w http.ResponseWriter, f *Failure) {
	if f.Hijack {
		// Cutting the connection is how a dropped network looks to the
		// client: no status line, no body.
		hj, ok := w.(http.Hijacker)
		if ok {
			if conn, _, err := hj.Hijack(); err == nil {
				_ = conn.Close()
				return
			}
		}
	}
	if f.RetryAfter != "" {
		w.Header().Set("Retry-After", f.RetryAfter)
	}
	msg := f.Message
	if msg == "" {
		msg = "injected failure"
	}
	s.errorJSON(w, f.Status, f.Reason, msg)
}

func (s *Server) errorJSON(w http.ResponseWriter, status int, reason, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body := map[string]any{"error": map[string]any{
		"code": status, "message": message, "status": rpcStatus(status),
		"errors":  []map[string]string{{"reason": reason, "message": message}},
		"details": []map[string]string{{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "reason": reason}},
	}}
	_ = json.NewEncoder(w).Encode(body)
}

func rpcStatus(code int) string {
	switch code {
	case 400:
		return "INVALID_ARGUMENT"
	case 401:
		return "UNAUTHENTICATED"
	case 403:
		return "PERMISSION_DENIED"
	case 404:
		return "NOT_FOUND"
	case 409:
		return "ALREADY_EXISTS"
	case 429:
		return "RESOURCE_EXHAUSTED"
	case 500:
		return "INTERNAL"
	case 503:
		return "UNAVAILABLE"
	}
	return "UNKNOWN"
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) handleAbout(w http.ResponseWriter) {
	s.mu.Lock()
	about := *s.About
	s.mu.Unlock()
	writeJSON(w, about)
}

// maxGeneratedIDs is Drive's own ceiling for files.generateIds. The fake
// enforces it because a fake that accepts what Drive refuses lets a bug
// through, and because sizing an allocation from a query parameter is
// how a test server becomes a way to exhaust memory.
const maxGeneratedIDs = 1000

func (s *Server) handleGenerateIDs(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("count")
	count, err := strconv.Atoi(raw)
	if raw != "" && err != nil {
		s.errorJSON(w, http.StatusBadRequest, "invalid", "Invalid Value for count")
		return
	}
	if count <= 0 {
		count = 10
	}
	if count > maxGeneratedIDs {
		s.errorJSON(w, http.StatusBadRequest, "invalid",
			fmt.Sprintf("count must be at most %d", maxGeneratedIDs))
		return
	}
	s.mu.Lock()
	ids := make([]string, count)
	for i := range ids {
		s.nextID++
		ids[i] = fmt.Sprintf("id-generated-fixture-%d", s.nextID)
	}
	s.mu.Unlock()
	writeJSON(w, gdrive.GeneratedIDs{IDs: ids, Space: "drive"})
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request, id string) {
	s.mu.Lock()
	f := s.Files[id]
	if id == "root" {
		f = s.Files[s.RootID]
	}
	s.mu.Unlock()
	if f == nil {
		s.errorJSON(w, http.StatusNotFound, "notFound", "File not found: "+id+".")
		return
	}
	writeJSON(w, s.project(f, r.URL.Query().Get("fields"), r.URL.Query().Get("includeLabels") != ""))
}

func (s *Server) handleListPermissions(w http.ResponseWriter, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.Files[id]; !ok {
		if _, ok := s.Drives[id]; !ok {
			s.errorJSON(w, http.StatusNotFound, "notFound", "File not found: "+id+".")
			return
		}
	}
	perms := append([]*gdrive.Permission(nil), s.Permissions[id]...)
	writeJSON(w, gdrive.PermissionList{Permissions: perms})
}

func (s *Server) handleListDrives(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Drive returns hidden drives like any other; hiding is a sidebar
	// setting, not a filter on the API.
	out := make([]*gdrive.Drive, 0, len(s.Drives))
	for _, d := range s.Drives {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	writeJSON(w, gdrive.DriveList{Drives: out})
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	pred, err := parseQuery(q.Get("q"))
	if err != nil {
		s.errorJSON(w, http.StatusBadRequest, "invalid", "Invalid Value: "+err.Error())
		return
	}
	corpora := q.Get("corpora")
	driveID := q.Get("driveId")
	includeAll := q.Get("includeItemsFromAllDrives") == "true"

	s.mu.Lock()
	var matched []*gdrive.File
	for _, f := range s.Files {
		if f.ID == s.RootID {
			continue
		}
		if driveID != "" && f.DriveID != driveID {
			continue
		}
		// Without includeItemsFromAllDrives a shared-drive item is
		// invisible, which is exactly the bug this fake exists to catch.
		if !includeAll && f.DriveID != "" {
			continue
		}
		if corpora == "user" && f.DriveID != "" {
			continue
		}
		if !pred(f, s) {
			continue
		}
		matched = append(matched, f)
	}
	s.mu.Unlock()

	sortFiles(matched, q.Get("orderBy"))

	pageSize := 100
	if n, err := strconv.Atoi(q.Get("pageSize")); err == nil && n > 0 {
		pageSize = n
	}
	start := 0
	if tok := q.Get("pageToken"); tok != "" {
		n, err := strconv.Atoi(strings.TrimPrefix(tok, "offset-"))
		if err != nil {
			s.errorJSON(w, http.StatusBadRequest, "invalid", "Invalid page token")
			return
		}
		start = n
	}
	end := min(start+pageSize, len(matched))
	if start > len(matched) {
		start = len(matched)
	}
	page := gdrive.FileList{Files: []*gdrive.File{}}
	for _, f := range matched[start:end] {
		page.Files = append(page.Files, s.project(f, q.Get("fields"), false))
	}
	if end < len(matched) {
		page.NextPageToken = "offset-" + strconv.Itoa(end)
	}
	writeJSON(w, page)
}

// sortFiles applies the orderBy keys Drive supports, so a listing's
// order is deterministic here for the same reason it must be in
// production: an unordered listing cannot be paged honestly.
func sortFiles(files []*gdrive.File, orderBy string) {
	keys := strings.Split(orderBy, ",")
	sort.SliceStable(files, func(i, j int) bool {
		for _, key := range keys {
			key = strings.TrimSpace(key)
			desc := strings.HasSuffix(key, " desc")
			key = strings.TrimSuffix(strings.TrimSuffix(key, " desc"), " asc")
			a, b := files[i], files[j]
			var less, equal bool
			switch key {
			case "folder":
				af, bf := a.MimeType == gdrive.MimeFolder, b.MimeType == gdrive.MimeFolder
				less, equal = af && !bf, af == bf
			case "name", "name_natural":
				less, equal = strings.ToLower(a.Name) < strings.ToLower(b.Name), strings.EqualFold(a.Name, b.Name)
			case "modifiedTime", "recency":
				less, equal = a.ModifiedTime < b.ModifiedTime, a.ModifiedTime == b.ModifiedTime
			case "createdTime":
				less, equal = a.CreatedTime < b.CreatedTime, a.CreatedTime == b.CreatedTime
			case "starred":
				less, equal = a.Starred && !b.Starred, a.Starred == b.Starred
			case "quotaBytesUsed":
				less, equal = sizeOf(a) < sizeOf(b), sizeOf(a) == sizeOf(b)
			default:
				continue
			}
			if equal {
				continue
			}
			if desc {
				return !less
			}
			return less
		}
		// A stable tie-break keeps paging honest when the sort key repeats.
		return files[i].ID < files[j].ID
	})
}

func sizeOf(f *gdrive.File) int64 {
	n, _ := strconv.ParseInt(f.Size, 10, 64)
	return n
}

// project returns the file with only the requested fields, the way Drive
// does: asking for a narrow field list and reading a field that was not
// requested is a bug the fake will surface.
func (s *Server) project(f *gdrive.File, fields string, includeLabels bool) *gdrive.File {
	out := *f
	if formats := exportFormatsFor[f.MimeType]; len(formats) > 0 {
		links := make(map[string]string, len(formats))
		for _, m := range formats {
			links[m] = "https://docs.google.com/feeds/download/" + f.ID
		}
		out.ExportLinks = links
	}
	s.mu.Lock()
	if perms := s.Permissions[f.ID]; len(perms) > 0 {
		out.Permissions = append([]*gdrive.Permission(nil), perms...)
		ids := make([]string, 0, len(perms))
		for _, p := range perms {
			ids = append(ids, p.ID)
		}
		out.PermissionIDs = ids
	}
	s.mu.Unlock()
	if !includeLabels {
		out.LabelInfo = nil
	}
	if fields == "" || strings.Contains(fields, "*") {
		return &out
	}
	return projectFields(&out, fields)
}

// projectFields drops every field the request did not ask for. The wire
// struct's json tags are exactly the names a fields expression uses, so
// this goes through JSON rather than a hand-written copy per field: a
// list of 46 assignments is a list that falls behind gdrive.File.
func projectFields(f *gdrive.File, fields string) *gdrive.File {
	want := map[string]bool{"id": true} // the id always comes back
	for _, name := range splitFields(fields) {
		want[name] = true
	}
	raw, err := json.Marshal(f)
	if err != nil {
		return f
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(raw, &all); err != nil {
		return f
	}
	for k := range all {
		if !want[k] {
			delete(all, k)
		}
	}
	kept, err := json.Marshal(all)
	if err != nil {
		return f
	}
	out := &gdrive.File{}
	if err := json.Unmarshal(kept, out); err != nil {
		return f
	}
	return out
}

// splitFields pulls the field names out of a Drive fields expression.
// Depth 0 is the response envelope (nextPageToken, files), depth 1 the
// file's own fields; a deeper sub-selection like owners(displayName)
// names a field this projection keeps whole, so only its head matters.
func splitFields(fields string) []string {
	var out []string
	depth := 0
	start := 0
	emit := func(end int) {
		name := strings.TrimSpace(fields[start:end])
		if name != "" && name != "files" {
			out = append(out, name)
		}
	}
	for i := 0; i < len(fields); i++ {
		switch fields[i] {
		case '(':
			if depth <= 1 {
				emit(i)
			}
			depth++
			start = i + 1
		case ')':
			if depth <= 1 {
				emit(i)
			}
			depth--
			start = i + 1
		case ',':
			if depth <= 1 {
				emit(i)
			}
			start = i + 1
		}
	}
	emit(len(fields))
	return out
}
