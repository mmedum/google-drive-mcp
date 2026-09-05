package drivetest

import (
	"crypto/md5" //nolint:gosec // Drive's own checksum, matched here so the client can verify it
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mmedum/google-drive-mcp/internal/gapi"
	"github.com/mmedum/google-drive-mcp/internal/gdrive"
)

// uploadSession is one resumable upload in progress.
type uploadSession struct {
	fileID string
	meta   *gdrive.FileMeta
	// contentType is what the bytes are, from X-Upload-Content-Type.
	contentType string
	total       int64
	data        []byte
	done        bool
	fields      string
}

// handleCreate serves files.create for a metadata-only file: a folder, a
// shortcut, or an empty Workspace document.
func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	var meta gdrive.FileMeta
	if !s.decodeJSON(w, r, &meta) {
		return
	}
	f, err := s.createFile(&meta, "", nil)
	if err != nil {
		s.writeAPIError(w, err)
		return
	}
	writeJSON(w, s.project(f, r.URL.Query().Get("fields"), false))
}

// maxRequestBytes bounds a request body the fake will read. It is a test
// server, but one that grows its allocation from a request is still a
// way to run the test host out of memory.
const maxRequestBytes = 64 << 20

// apiFailure is an error the fake answers with, carrying Google's own
// status and reason so the client's mapping is exercised.
type apiFailure struct {
	status  int
	reason  string
	message string
}

func (e *apiFailure) Error() string { return e.message }

func (s *Server) writeAPIError(w http.ResponseWriter, err error) {
	if f, ok := err.(*apiFailure); ok { //nolint:errorlint // the fake raises exactly this type
		s.errorJSON(w, f.status, f.reason, f.message)
		return
	}
	s.errorJSON(w, http.StatusInternalServerError, "internalError", err.Error())
}

// createFile adds a file the way files.create does, with content when
// the caller uploaded some.
func (s *Server) createFile(meta *gdrive.FileMeta, contentType string, content []byte) (*gdrive.File, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	parent := s.RootID
	if len(meta.Parents) > 0 && meta.Parents[0] != "" {
		parent = meta.Parents[0]
	}
	p := s.fileLocked(parent)
	if p == nil {
		return nil, &apiFailure{http.StatusNotFound, "notFound", "File not found: " + parent + "."}
	}
	parent = p.ID
	if !p.IsFolder() {
		return nil, &apiFailure{http.StatusBadRequest, "invalid", "The specified parent is not a folder."}
	}

	mimeType := meta.MimeType
	if mimeType == "" {
		mimeType = contentType
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	// Drive's own words, observed live on 2026-09-05, and there are two
	// of them: the Docs Editors formats and a shortcut refuse a
	// pre-generated id with different messages and different statuses. A
	// fake that accepts what Drive refuses lets the bug through, and this
	// one accepted both until a real account said no — twice.
	if meta.ID != "" && !gapi.AcceptsGeneratedID(mimeType) {
		if mimeType == gdrive.MimeShortcut {
			return nil, &apiFailure{http.StatusBadRequest, "invalid",
				"The provided file ID is not usable."}
		}
		return nil, &apiFailure{http.StatusForbidden, "insufficientFilePermissions",
			"Generated IDs are not supported for Docs Editors formats."}
	}

	id := meta.ID
	if id == "" {
		s.nextID++
		id = fmt.Sprintf("id-created-fixture-%d", s.nextID)
	}
	if _, taken := s.Files[id]; taken {
		return nil, &apiFailure{http.StatusConflict, "duplicate", "A file with that id already exists."}
	}
	ts := s.now().UTC().Format(time.RFC3339)
	f := &gdrive.File{
		ID: id, Name: meta.Name, MimeType: mimeType, Parents: []string{parent},
		CreatedTime: ts, ModifiedTime: ts,
		Owners: []*gdrive.User{s.me()}, LastModifyingUser: s.me(), OwnedByMe: true,
		WebViewLink: webViewLink(id, mimeType), Capabilities: defaultCapabilities(mimeType),
		DriveID:         p.DriveID,
		ShortcutDetails: meta.ShortcutDetails,
	}
	if f.IsShortcut() && f.ShortcutDetails != nil {
		if target := s.Files[f.ShortcutDetails.TargetID]; target != nil {
			f.ShortcutDetails.TargetMimeType = target.MimeType
		}
	}
	applyMeta(f, meta)
	s.Files[id] = f
	if content != nil {
		s.setContentLocked(f, content)
	}
	return f, nil
}

// setContentLocked stores a file's bytes and the metadata Drive derives
// from them. The caller holds the lock.
func (s *Server) setContentLocked(f *gdrive.File, content []byte) {
	s.Content[f.ID] = string(content)
	if f.IsWorkspaceDoc() {
		// A converted file has no size and no checksum of its own, which
		// is exactly the case an upload result has to describe honestly.
		f.Size, f.MD5Checksum, f.QuotaBytesUsed = "", "", ""
	} else {
		sum := md5.Sum(content) //nolint:gosec // matching Drive's md5Checksum
		f.Size = strconv.Itoa(len(content))
		f.QuotaBytesUsed = f.Size
		f.MD5Checksum = hex.EncodeToString(sum[:])
	}
	s.nextID++
	rev := &gdrive.Revision{
		ID: fmt.Sprintf("id-revision-fixture-%d", s.nextID), MimeType: f.MimeType,
		ModifiedTime: s.now().UTC().Format(time.RFC3339), Size: f.Size,
		MD5Checksum: f.MD5Checksum, LastModifyingUser: s.me(),
	}
	s.Revisions[f.ID] = append(s.Revisions[f.ID], rev)
	s.RevisionContent[rev.ID] = string(content)
	f.HeadRevisionID = rev.ID
}

// importable reports whether the fake offers this conversion, from the
// same table about.get hands out. Drive answers "The requested
// conversion is not supported" for the rest, which is what caught a csv
// being asked to become a Doc.
func (s *Server) importable(from, to string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.About == nil || s.About.ImportFormats == nil {
		return true
	}
	for _, target := range s.About.ImportFormats[from] {
		if target == to {
			return true
		}
	}
	return false
}

// applyMeta applies a create or patch body to a file, changing only the
// fields the body carried.
func applyMeta(f *gdrive.File, meta *gdrive.FileMeta) {
	if meta.Name != "" {
		f.Name = meta.Name
	}
	if meta.Description != nil {
		f.Description = *meta.Description
	}
	if meta.Starred != nil {
		f.Starred = *meta.Starred
	}
	if meta.FolderColorRgb != nil {
		f.FolderColorRgb = *meta.FolderColorRgb
	}
	if meta.WritersCanShare != nil {
		f.WritersCanShare = *meta.WritersCanShare
	}
	if meta.CopyRequiresWriterPermission != nil {
		f.CopyRequiresWriterPermission = *meta.CopyRequiresWriterPermission
	}
	for k, v := range meta.Properties {
		if v == nil {
			delete(f.Properties, k)
			continue
		}
		if f.Properties == nil {
			f.Properties = map[string]string{}
		}
		f.Properties[k] = *v
	}
}

// handleUpdate serves files.update: a metadata patch, a move, or both.
func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request, id string) {
	var meta gdrive.FileMeta
	if !s.decodeJSON(w, r, &meta) {
		return
	}
	q := r.URL.Query()
	s.mu.Lock()
	f := s.fileLocked(id)
	if f == nil {
		s.mu.Unlock()
		s.errorJSON(w, http.StatusNotFound, "notFound", "File not found: "+id+".")
		return
	}
	if err := s.moveLocked(f, q.Get("addParents"), q.Get("removeParents")); err != nil {
		s.mu.Unlock()
		s.writeAPIError(w, err)
		return
	}
	applyMeta(f, &meta)
	if meta.Trashed != nil {
		s.setTrashedLocked(f, *meta.Trashed, true)
	}
	f.ModifiedTime = s.now().UTC().Format(time.RFC3339)
	s.mu.Unlock()
	writeJSON(w, s.project(f, q.Get("fields"), q.Get("includeLabels") != ""))
}

// moveLocked applies addParents and removeParents with the rules that
// bite: one parent, and no My Drive folder into a shared drive.
func (s *Server) moveLocked(f *gdrive.File, add, remove string) error {
	if add == "" {
		return nil
	}
	target := s.fileLocked(add)
	if target == nil {
		return &apiFailure{http.StatusNotFound, "notFound", "File not found: " + add + "."}
	}
	if !target.IsFolder() {
		return &apiFailure{http.StatusBadRequest, "invalid", "The specified parent is not a folder."}
	}
	if f.IsFolder() && f.DriveID == "" && target.DriveID != "" {
		return &apiFailure{http.StatusForbidden, "teamDrivesFolderMoveInNotSupported",
			"Moving folders into shared drives is not supported."}
	}
	if remove != "" && remove != f.Parent() && remove != "root" {
		return &apiFailure{http.StatusBadRequest, "invalid",
			"The parent to remove is not a parent of this file."}
	}
	f.Parents = []string{target.ID}
	s.setDriveLocked(f, target.DriveID)
	return nil
}

// setDriveLocked moves a file and everything under it between My Drive
// and a shared drive, which is what Drive does when the parent changes.
func (s *Server) setDriveLocked(f *gdrive.File, driveID string) {
	if f.DriveID == driveID {
		return
	}
	f.DriveID = driveID
	for _, child := range s.childrenLocked(f.ID) {
		s.setDriveLocked(child, driveID)
	}
}

// setTrashedLocked trashes or restores a file. Trashing a folder trashes
// what is inside it, which is why trash_file says "folder with its
// contents"; only the item the caller named is explicitly trashed.
func (s *Server) setTrashedLocked(f *gdrive.File, trashed, explicit bool) {
	f.Trashed = trashed
	f.ExplicitlyTrashed = trashed && explicit
	if trashed {
		f.TrashedTime = s.now().UTC().Format(time.RFC3339)
		f.TrashingUser = s.me()
	} else {
		f.TrashedTime, f.TrashingUser = "", nil
	}
	for _, child := range s.childrenLocked(f.ID) {
		s.setTrashedLocked(child, trashed, false)
	}
}

func (s *Server) childrenLocked(parent string) []*gdrive.File {
	var out []*gdrive.File
	for _, f := range s.Files {
		if f.Parent() == parent {
			out = append(out, f)
		}
	}
	return out
}

// handleCopy serves files.copy, converting when the body names a
// different mimeType, and refusing folders as Drive does.
func (s *Server) handleCopy(w http.ResponseWriter, r *http.Request, id string) {
	var meta gdrive.FileMeta
	if !s.decodeJSON(w, r, &meta) {
		return
	}
	s.mu.Lock()
	src := s.Files[id]
	content, hasContent := s.Content[id]
	s.mu.Unlock()
	if src == nil {
		s.errorJSON(w, http.StatusNotFound, "notFound", "File not found: "+id+".")
		return
	}
	if src.IsFolder() {
		s.errorJSON(w, http.StatusForbidden, "fileNotCopyable", "Folders cannot be copied.")
		return
	}
	if meta.MimeType != "" && meta.MimeType != src.MimeType && !s.importable(src.MimeType, meta.MimeType) {
		s.errorJSON(w, http.StatusBadRequest, "invalid", "The requested conversion is not supported.")
		return
	}
	body := meta
	if body.Name == "" {
		body.Name = "Copy of " + src.Name
	}
	if len(body.Parents) == 0 && src.Parent() != "" {
		body.Parents = []string{src.Parent()}
	}
	if body.MimeType == "" {
		body.MimeType = src.MimeType
	}
	var bytesIn []byte
	if hasContent {
		bytesIn = []byte(content)
	}
	f, err := s.createFile(&body, src.MimeType, bytesIn)
	if err != nil {
		s.writeAPIError(w, err)
		return
	}
	writeJSON(w, s.project(f, r.URL.Query().Get("fields"), false))
}

// handleUpload serves both upload types. The multipart form carries the
// metadata and the bytes together; the resumable form opens a session
// and answers with its URI.
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request, fileID string) {
	q := r.URL.Query()
	switch q.Get("uploadType") {
	case "multipart":
		s.handleMultipartUpload(w, r, fileID)
	case "resumable":
		s.startSession(w, r, fileID)
	default:
		s.errorJSON(w, http.StatusBadRequest, "invalid", "Unsupported uploadType")
	}
}

func (s *Server) handleMultipartUpload(w http.ResponseWriter, r *http.Request, fileID string) {
	meta, contentType, content, err := readMultipart(r)
	if err != nil {
		s.errorJSON(w, http.StatusBadRequest, "invalid", err.Error())
		return
	}
	s.finishUpload(w, fileID, meta, contentType, content, r.URL.Query().Get("fields"))
}

// readMultipart pulls the metadata and the bytes out of a
// multipart/related body, which is the shape files.create takes.
func readMultipart(r *http.Request) (*gdrive.FileMeta, string, []byte, error) {
	_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return nil, "", nil, fmt.Errorf("bad content type: %w", err)
	}
	boundary := params["boundary"]
	if boundary == "" {
		return nil, "", nil, fmt.Errorf("multipart body without a boundary")
	}
	mr := multipart.NewReader(io.LimitReader(r.Body, maxRequestBytes), boundary)
	var meta gdrive.FileMeta
	var content []byte
	contentType := ""
	for i := 0; ; i++ {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, "", nil, err
		}
		data, err := io.ReadAll(part)
		if err != nil {
			return nil, "", nil, err
		}
		if i == 0 {
			if err := json.Unmarshal(data, &meta); err != nil {
				return nil, "", nil, fmt.Errorf("bad metadata part: %w", err)
			}
			continue
		}
		contentType = gdrive.MimeOnly(part.Header.Get("Content-Type"))
		content = data
	}
	return &meta, contentType, content, nil
}

// finishUpload creates or replaces a file from uploaded bytes.
func (s *Server) finishUpload(w http.ResponseWriter, fileID string,
	meta *gdrive.FileMeta, contentType string, content []byte, fields string,
) {
	if fileID == "" {
		f, err := s.createFile(meta, contentType, content)
		if err != nil {
			s.writeAPIError(w, err)
			return
		}
		writeJSON(w, s.project(f, fields, false))
		return
	}
	s.mu.Lock()
	f := s.Files[fileID]
	if f == nil {
		s.mu.Unlock()
		s.errorJSON(w, http.StatusNotFound, "notFound", "File not found: "+fileID+".")
		return
	}
	applyMeta(f, meta)
	if contentType != "" && !f.IsWorkspaceDoc() {
		f.MimeType = contentType
	}
	s.setContentLocked(f, content)
	f.ModifiedTime = s.now().UTC().Format(time.RFC3339)
	s.mu.Unlock()
	writeJSON(w, s.project(f, fields, false))
}

// startSession opens a resumable upload session and answers with its
// URI in the Location header, as the protocol requires.
func (s *Server) startSession(w http.ResponseWriter, r *http.Request, fileID string) {
	var meta gdrive.FileMeta
	if !s.decodeJSON(w, r, &meta) {
		return
	}
	total, err := strconv.ParseInt(r.Header.Get("X-Upload-Content-Length"), 10, 64)
	if err != nil || total < 0 {
		s.errorJSON(w, http.StatusBadRequest, "invalid", "X-Upload-Content-Length is required")
		return
	}
	s.mu.Lock()
	s.nextID++
	id := fmt.Sprintf("session-%d", s.nextID)
	s.sessions[id] = &uploadSession{
		fileID: fileID, meta: &meta, total: total,
		contentType: r.Header.Get("X-Upload-Content-Type"),
		fields:      r.URL.Query().Get("fields"),
	}
	s.mu.Unlock()
	w.Header().Set("Location", s.URL+"/upload/drive/v3/sessions/"+id)
	w.WriteHeader(http.StatusOK)
}

// handleSession serves one chunk of a resumable upload, or a query for
// how much of it has been stored.
func (s *Server) handleSession(w http.ResponseWriter, r *http.Request, id string) {
	s.mu.Lock()
	sess := s.sessions[id]
	limit := s.ChunkLimit
	stall := s.StallUploads
	s.mu.Unlock()
	if sess == nil {
		s.errorJSON(w, http.StatusNotFound, "notFound", "Upload session not found.")
		return
	}
	rng := r.Header.Get("Content-Range")
	start, end, total, query, err := parseContentRange(rng)
	if err != nil {
		s.errorJSON(w, http.StatusBadRequest, "invalid", err.Error())
		return
	}
	if total != sess.total {
		s.errorJSON(w, http.StatusBadRequest, "invalid", "Content-Range total does not match the session")
		return
	}
	if query {
		s.answerSession(w, sess)
		return
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBytes))
	if err != nil {
		s.errorJSON(w, http.StatusBadRequest, "invalid", "could not read the chunk")
		return
	}
	if int64(len(data)) != end-start+1 {
		s.errorJSON(w, http.StatusBadRequest, "invalid",
			fmt.Sprintf("Content-Range says %d bytes, body carried %d", end-start+1, len(data)))
		return
	}

	s.mu.Lock()
	stored := int64(len(sess.data))
	if start > stored {
		s.mu.Unlock()
		s.errorJSON(w, http.StatusBadRequest, "invalid",
			fmt.Sprintf("chunk starts at %d but the session holds %d bytes", start, stored))
		return
	}
	// A chunk that overlaps what is already stored is written where it
	// belongs, which is what recovering from an interruption looks like.
	// One that is entirely old — a client re-sending what the session
	// already has — adds nothing, and must not be read past its end.
	keep := data[min(stored-start, int64(len(data))):]
	if stall {
		keep = nil
	}
	if limit > 0 && int64(len(keep)) > limit {
		// The fake stored only part of the chunk, which Drive is allowed
		// to do and which the client has to notice.
		keep = keep[:limit]
	}
	sess.data = append(sess.data, keep...)
	stored = int64(len(sess.data))
	complete := stored >= sess.total
	s.mu.Unlock()

	if !complete {
		if stored > 0 {
			w.Header().Set("Range", fmt.Sprintf("bytes=0-%d", stored-1))
		}
		w.WriteHeader(http.StatusPermanentRedirect)
		return
	}
	s.completeSession(w, id, sess)
}

// answerSession replies to a "bytes */total" query: the finished file if
// the upload is done, and otherwise a 308 saying how much is stored.
func (s *Server) answerSession(w http.ResponseWriter, sess *uploadSession) {
	s.mu.Lock()
	stored := int64(len(sess.data))
	done := sess.done
	f := s.Files[sess.fileID]
	s.mu.Unlock()
	switch {
	case done:
		writeJSON(w, s.project(f, sess.fields, false))
	case stored >= sess.total:
		s.completeSession(w, "", sess)
	default:
		if stored > 0 {
			w.Header().Set("Range", fmt.Sprintf("bytes=0-%d", stored-1))
		}
		w.WriteHeader(http.StatusPermanentRedirect)
	}
}

// completeSession finishes an upload and closes the session. A later
// query still answers, from the file the session produced.
func (s *Server) completeSession(w http.ResponseWriter, id string, sess *uploadSession) {
	s.mu.Lock()
	sess.done = true
	// Deleting the empty key is a no-op, so the query path needs no
	// special case to avoid it.
	delete(s.sessions, id)
	fileID := sess.fileID
	if fileID == "" && sess.meta != nil {
		sess.fileID = sess.meta.ID
	}
	s.mu.Unlock()
	s.finishUpload(w, fileID, sess.meta, sess.contentType, sess.data, sess.fields)
}

// parseContentRange reads "bytes 0-8388607/12345678" and the query form
// "bytes */12345678".
func parseContentRange(v string) (start, end, total int64, query bool, err error) {
	v = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(v), "bytes"))
	v = strings.TrimSpace(v)
	spec, totalText, ok := strings.Cut(v, "/")
	if !ok {
		return 0, 0, 0, false, fmt.Errorf("Content-Range is required")
	}
	total, err = strconv.ParseInt(strings.TrimSpace(totalText), 10, 64)
	if err != nil {
		return 0, 0, 0, false, fmt.Errorf("Content-Range total is not a number")
	}
	if strings.TrimSpace(spec) == "*" {
		return 0, 0, total, true, nil
	}
	startText, endText, ok := strings.Cut(spec, "-")
	if !ok {
		return 0, 0, 0, false, fmt.Errorf("Content-Range is malformed")
	}
	if start, err = strconv.ParseInt(strings.TrimSpace(startText), 10, 64); err != nil {
		return 0, 0, 0, false, fmt.Errorf("Content-Range start is not a number")
	}
	if end, err = strconv.ParseInt(strings.TrimSpace(endText), 10, 64); err != nil {
		return 0, 0, 0, false, fmt.Errorf("Content-Range end is not a number")
	}
	return start, end, total, false, nil
}
