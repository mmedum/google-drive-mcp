package drivetest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
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

	if upload, ok := strings.CutPrefix(r.URL.Path, "/upload/drive/v3"); ok {
		s.serveUpload(w, r, upload)
		return
	}
	if rev, ok := strings.CutPrefix(r.URL.Path, "/export/"); ok && r.Method == http.MethodGet {
		s.handleRevisionExport(w, r, rev)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/drive/v3")
	switch {
	case path == "/about" && r.Method == http.MethodGet:
		s.handleAbout(w)
	case path == "/changes/startPageToken" && r.Method == http.MethodGet:
		s.handleStartPageToken(w, r)
	case path == "/changes" && r.Method == http.MethodGet:
		s.handleListChanges(w, r)
	case path == "/drives" && r.Method == http.MethodGet:
		s.handleListDrives(w, r)
	case path == "/drives" && r.Method == http.MethodPost:
		s.handleCreateDrive(w, r)
	case strings.HasPrefix(path, "/drives/"):
		s.serveDrives(w, r, strings.TrimPrefix(path, "/drives/"))
	case path == "/files" || strings.HasPrefix(path, "/files/"):
		s.serveFiles(w, r, path)
	default:
		s.errorJSON(w, http.StatusNotFound, "notFound", "the fake does not implement "+r.Method+" "+path)
	}
}

// serveDrives routes the per-drive endpoints. Hiding has two of its own,
// which is why they are matched before the drive id.
func (s *Server) serveDrives(w http.ResponseWriter, r *http.Request, rest string) {
	switch {
	case strings.HasSuffix(rest, "/hide") && r.Method == http.MethodPost:
		s.handleDriveVisibility(w, strings.TrimSuffix(rest, "/hide"), true)
	case strings.HasSuffix(rest, "/unhide") && r.Method == http.MethodPost:
		s.handleDriveVisibility(w, strings.TrimSuffix(rest, "/unhide"), false)
	default:
		s.handleDrive(w, r, rest)
	}
}

// serveFiles routes everything under /files, which is most of the API.
// The collection endpoints and the ones addressing a file as a whole are
// here; a file's sub-resources are in serveFileChild, because one switch
// covering both grew past the point where a reader could see either.
func (s *Server) serveFiles(w http.ResponseWriter, r *http.Request, path string) {
	media := r.URL.Query().Get("alt") == "media"
	rest := strings.TrimPrefix(path, "/files/")
	switch {
	case path == "/files/generateIds" && r.Method == http.MethodGet:
		s.handleGenerateIDs(w, r)
	case path == "/files" && r.Method == http.MethodGet:
		s.handleList(w, r)
	case path == "/files" && r.Method == http.MethodPost:
		s.handleCreate(w, r)
	case path == "/files/trash" && r.Method == http.MethodDelete:
		s.handleEmptyTrash(w, r)
	case strings.Contains(rest, "/"):
		s.serveFileChild(w, r, path, rest, media)
	case r.Method == http.MethodGet && media:
		s.handleDownload(w, r, rest, "")
	case r.Method == http.MethodGet:
		s.handleGet(w, r, rest)
	case r.Method == http.MethodPatch:
		s.handleUpdate(w, r, rest)
	case r.Method == http.MethodDelete:
		s.handleDeleteFile(w, rest)
	default:
		s.errorJSON(w, http.StatusNotFound, "notFound", "the fake does not implement "+r.Method+" "+path)
	}
}

// serveFileChild routes the sub-resources of one file: its permissions,
// its revisions, its export and its copy.
func (s *Server) serveFileChild(w http.ResponseWriter, r *http.Request, path, rest string, media bool) {
	switch {
	case strings.HasSuffix(path, "/permissions") && r.Method == http.MethodGet:
		s.handleListPermissions(w, strings.TrimSuffix(rest, "/permissions"))
	case strings.HasSuffix(path, "/permissions") && r.Method == http.MethodPost:
		s.handleCreatePermission(w, r, strings.TrimSuffix(rest, "/permissions"))
	case permissionPath.MatchString(path):
		s.servePermission(w, r, permissionPath.FindStringSubmatch(path))
	case strings.HasSuffix(path, "/revisions") && r.Method == http.MethodGet:
		s.handleListRevisions(w, strings.TrimSuffix(rest, "/revisions"))
	case strings.HasSuffix(path, "/export") && r.Method == http.MethodGet:
		s.handleExport(w, r, strings.TrimSuffix(rest, "/export"))
	case strings.HasSuffix(path, "/copy") && r.Method == http.MethodPost:
		s.handleCopy(w, r, strings.TrimSuffix(rest, "/copy"))
	case commentPath.MatchString(path):
		m := commentPath.FindStringSubmatch(path)
		s.serveComments(w, r, m[1], m[2])
	case proposalPath.MatchString(path):
		m := proposalPath.FindStringSubmatch(path)
		s.serveProposals(w, r, m[1], m[2])
	case revisionPath.MatchString(path):
		m := revisionPath.FindStringSubmatch(path)
		switch {
		case media:
			s.handleDownload(w, r, m[1], m[2])
		case r.Method == http.MethodDelete:
			s.handleDeleteRevision(w, m[1], m[2])
		default:
			s.handleRevision(w, r, m[1], m[2])
		}
	default:
		s.errorJSON(w, http.StatusNotFound, "notFound", "the fake does not implement "+r.Method+" "+path)
	}
}

// revisionPath matches /files/{fileId}/revisions/{revisionId}.
var revisionPath = regexp.MustCompile(`^/files/([^/]+)/revisions/([^/]+)$`)

// commentPath matches everything under one file's comments: the
// collection, one thread, its replies and one reply. The tail is passed
// on whole, because the comment routing has to tell four shapes apart
// and doing it with four regexps here would put half of that decision in
// this file and half in the other.
var commentPath = regexp.MustCompile(`^/files/([^/]+)/comments(?:/(.*))?$`)

// proposalPath matches a file's access proposals, including the
// colon-suffixed :resolve verb, which is not a path segment of its own.
var proposalPath = regexp.MustCompile(`^/files/([^/]+)/accessproposals(?:/(.*))?$`)

// permissionPath matches /files/{fileId}/permissions/{permissionId}.
var permissionPath = regexp.MustCompile(`^/files/([^/]+)/permissions/([^/]+)$`)

// servePermission routes the three methods on one grant.
func (s *Server) servePermission(w http.ResponseWriter, r *http.Request, m []string) {
	switch r.Method {
	case http.MethodGet:
		s.handleGetPermission(w, m[1], m[2])
	case http.MethodPatch:
		s.handleUpdatePermission(w, r, m[1], m[2])
	case http.MethodDelete:
		s.handleDeletePermission(w, m[1], m[2])
	default:
		s.errorJSON(w, http.StatusNotFound, "notFound", "the fake does not implement "+r.Method+" on a permission")
	}
}

// serveUpload routes the media-upload endpoints, which Drive serves
// under /upload with the same version path.
func (s *Server) serveUpload(w http.ResponseWriter, r *http.Request, path string) {
	switch {
	case path == "/files" && r.Method == http.MethodPost:
		s.handleUpload(w, r, "")
	case strings.HasPrefix(path, "/files/") && r.Method == http.MethodPatch:
		s.handleUpload(w, r, strings.TrimPrefix(path, "/files/"))
	case strings.HasPrefix(path, "/sessions/") && r.Method == http.MethodPut:
		s.handleSession(w, r, strings.TrimPrefix(path, "/sessions/"))
	default:
		s.errorJSON(w, http.StatusNotFound, "notFound", "the fake does not implement "+r.Method+" "+path)
	}
}

// decodeJSON reads a JSON request body, bounded, and answers the 400
// itself so that the size limit and the shape of a malformed-body reply
// live in one place rather than in every handler.
func (s *Server) decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(io.LimitReader(r.Body, maxRequestBytes)).Decode(v); err != nil {
		s.errorJSON(w, http.StatusBadRequest, "invalid", "Invalid request body: "+err.Error())
		return false
	}
	return true
}

// fileLocked returns a file by id, resolving the "root" alias Drive
// accepts everywhere. Every handler needs it, and one that forgets it
// answers 404 for `root` with nothing to say why. The caller holds the
// lock.
func (s *Server) fileLocked(id string) *gdrive.File {
	if id == "root" {
		return s.Files[s.RootID]
	}
	return s.Files[id]
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

// errorJSON writes Google's error envelope, with one condition spelled
// BOTH ways it really arrives: the legacy errors[] entry keeps the
// camelCase reason a caller asked for, and the google.rpc.ErrorInfo
// detail carries the UPPER_SNAKE_CASE form. They used to be identical
// here, which made the fake agree with any client that compared the
// camelCase spelling exactly — including one that would have missed
// every modern response from Drive itself.
func (s *Server) errorJSON(w http.ResponseWriter, status int, reason, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body := map[string]any{"error": map[string]any{
		"code": status, "message": message, "status": rpcStatus(status),
		"errors": []map[string]string{{"reason": reason, "message": message}},
		"details": []map[string]string{{
			"@type": "type.googleapis.com/google.rpc.ErrorInfo", "reason": upperSnake(reason),
		}},
	}}
	_ = json.NewEncoder(w).Encode(body)
}

// upperSnake turns a camelCase reason into the ErrorInfo spelling.
func upperSnake(reason string) string {
	var b strings.Builder
	for i, r := range reason {
		if r >= 'A' && r <= 'Z' && i > 0 {
			b.WriteByte('_')
		}
		b.WriteRune(r)
	}
	return strings.ToUpper(b.String())
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
	f := s.fileLocked(id)
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
	if s.permissionSubjectLocked(id) == "" {
		s.errorJSON(w, http.StatusNotFound, "notFound", "File not found: "+id+".")
		return
	}
	writeJSON(w, gdrive.PermissionList{Permissions: s.grantsLocked(id)})
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

	start, end, ok := s.pageWindow(w, q, len(matched), 100, MaxPageSize)
	if !ok {
		return
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

// MaxPageSize is the largest page the fake will serve, which is Drive's
// own ceiling for a file listing.
const MaxPageSize = 1000

// pageWindow reads pageSize and pageToken and returns the slice bounds
// of the page they ask for, answering the 400 itself for a token it
// cannot read.
//
// The token is an offset, which is enough to exercise what a client has
// to get right about an opaque one. It lives here rather than in each
// handler because two copies of it had already drifted: one clamped the
// start before computing the end and the other after, which is the kind
// of difference that is correct in both spellings until it is not.
func (s *Server) pageWindow(w http.ResponseWriter, q url.Values, total, size, maxSize int) (start, end int, ok bool) {
	if n, err := strconv.Atoi(q.Get("pageSize")); err == nil && n > 0 {
		size = n
	}
	if size > maxSize {
		s.errorJSON(w, http.StatusBadRequest, "invalid", "Invalid Value for pageSize")
		return 0, 0, false
	}
	if tok := q.Get("pageToken"); tok != "" {
		n, err := strconv.Atoi(strings.TrimPrefix(tok, "offset-"))
		if err != nil || n < 0 {
			s.errorJSON(w, http.StatusBadRequest, "invalid", "Invalid page token")
			return 0, 0, false
		}
		start = n
	}
	start = min(start, total)
	return start, min(start+size, total), true
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
	n, _ := f.SizeBytes()
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
