package drivetest

import (
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/mmedum/google-drive-mcp/internal/gdrive"
)

// handleDownload serves alt=media: a blob's own bytes, honouring a byte
// range so a caller that wants the head of a large file pays for the
// head of a large file.
func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request, id, revisionID string) {
	s.mu.Lock()
	f := s.Files[id]
	content := s.Content[id]
	if revisionID != "" {
		content = ""
		for _, rev := range s.Revisions[id] {
			if rev.ID == revisionID {
				content = s.RevisionContent[rev.ID]
			}
		}
	}
	s.mu.Unlock()
	if f == nil {
		s.errorJSON(w, http.StatusNotFound, "notFound", "File not found: "+id+".")
		return
	}
	if f.IsWorkspaceDoc() {
		s.errorJSON(w, http.StatusForbidden, "fileNotDownloadable",
			"Only files with binary content can be downloaded. Use Export with Docs Editors files.")
		return
	}
	s.mu.Lock()
	size, generated := s.Generated[id]
	s.mu.Unlock()
	if generated && revisionID == "" {
		s.serveGenerated(w, r, size, f.MimeType)
		return
	}
	s.serveBytes(w, r, []byte(content), f.MimeType)
}

// serveGenerated streams a file whose bytes are made up as they go, so a
// benchmark can pull a gigabyte through the client without a gigabyte
// existing anywhere. A fake that had to hold the file it serves could
// only prove the client streams up to whatever the test machine can
// spare.
func (s *Server) serveGenerated(w http.ResponseWriter, r *http.Request, total int64, contentType string) {
	start, end, ranged := parseRange(r.Header.Get("Range"), total)
	if !ranged {
		start, end = 0, total-1
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
	if ranged {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, total))
		w.WriteHeader(http.StatusPartialContent)
	} else {
		w.WriteHeader(http.StatusOK)
	}
	// One block, written over and over: the pattern repeats every 251
	// bytes, which is prime, so a client that dropped or duplicated a
	// chunk would not land back on the same bytes by accident.
	block := make([]byte, 64<<10)
	for i := range block {
		block[i] = byte((int64(i) + start) % 251)
	}
	for left := end - start + 1; left > 0; {
		n := int64(len(block))
		if left < n {
			n = left
		}
		if _, err := w.Write(block[:n]); err != nil {
			return
		}
		left -= n
	}
}

// handleExport serves files.export: a Google-native document converted
// to another format. Export takes no Range, which is why read_file
// windows an export after the fact rather than before.
func (s *Server) handleExport(w http.ResponseWriter, r *http.Request, id string) {
	format := r.URL.Query().Get("mimeType")
	s.mu.Lock()
	f := s.Files[id]
	content := s.Content[id]
	s.mu.Unlock()
	if f == nil {
		s.errorJSON(w, http.StatusNotFound, "notFound", "File not found: "+id+".")
		return
	}
	formats, ok := exportFormatsFor[f.MimeType]
	if !ok {
		s.errorJSON(w, http.StatusBadRequest, "invalid", "Export only supports Docs Editors files.")
		return
	}
	if !slices.Contains(formats, format) {
		s.errorJSON(w, http.StatusBadRequest, "invalid", "Export format is not supported: "+format)
		return
	}
	s.serveBytes(w, r, exportBytes(f, content, format), format)
}

// exportBytes is what the fake hands back for one export format. Text
// formats return the file's own content, so a test can assert on what it
// put there; binary formats return a marker, because a real docx would
// be a fixture nobody could check by reading it.
func exportBytes(f *gdrive.File, content, format string) []byte {
	switch format {
	case "text/plain", "text/markdown", "text/html", "text/csv", "text/tab-separated-values",
		"application/vnd.google-apps.script+json":
		return []byte(content)
	}
	return []byte("(" + format + " export of " + f.Name + ")")
}

// serveBytes writes content, honouring a Range request the way a media
// download does.
func (s *Server) serveBytes(w http.ResponseWriter, r *http.Request, content []byte, contentType string) {
	total := int64(len(content))
	start, end, ok := parseRange(r.Header.Get("Range"), total)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	if !ok {
		w.Header().Set("Content-Length", strconv.FormatInt(total, 10))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(content)
		return
	}
	w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
	w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, total))
	w.WriteHeader(http.StatusPartialContent)
	_, _ = w.Write(content[start : end+1])
}

// parseRange reads "bytes=0-99" and "bytes=100-", clamped to the content.
func parseRange(v string, total int64) (start, end int64, ok bool) {
	spec, found := strings.CutPrefix(strings.TrimSpace(v), "bytes=")
	if !found || total == 0 {
		return 0, 0, false
	}
	startText, endText, found := strings.Cut(spec, "-")
	if !found {
		return 0, 0, false
	}
	start, err := strconv.ParseInt(strings.TrimSpace(startText), 10, 64)
	if err != nil || start < 0 {
		return 0, 0, false
	}
	end = total - 1
	if t := strings.TrimSpace(endText); t != "" {
		if end, err = strconv.ParseInt(t, 10, 64); err != nil {
			return 0, 0, false
		}
	}
	if start >= total {
		return 0, 0, false
	}
	return start, min(end, total-1), true
}

// handleRevision serves revisions.get and revisions.update.
func (s *Server) handleRevision(w http.ResponseWriter, r *http.Request, fileID, revisionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.Files[fileID]
	if f == nil {
		s.errorJSON(w, http.StatusNotFound, "notFound", "File not found: "+fileID+".")
		return
	}
	var rev *gdrive.Revision
	for _, candidate := range s.Revisions[fileID] {
		if candidate.ID == revisionID {
			rev = candidate
		}
	}
	if rev == nil {
		s.errorJSON(w, http.StatusNotFound, "notFound", "Revision not found: "+revisionID+".")
		return
	}
	if r.Method == http.MethodPatch {
		var body struct {
			KeepForever *bool `json:"keepForever"`
		}
		if !s.decodeJSON(w, r, &body) {
			return
		}
		if body.KeepForever != nil {
			rev.KeepForever = *body.KeepForever
		}
	}
	out := *rev
	if f.IsWorkspaceDoc() {
		// A Docs editors revision has no bytes; its old content is
		// reached through these links.
		links := map[string]string{}
		for _, format := range exportFormatsFor[f.MimeType] {
			links[format] = s.URL + "/export/" + rev.ID + "?mimeType=" + format
		}
		out.ExportLinks = links
	}
	writeJSON(w, out)
}

// handleRevisionExport serves the export links a revision hands out.
func (s *Server) handleRevisionExport(w http.ResponseWriter, r *http.Request, revisionID string) {
	s.mu.Lock()
	content, ok := s.RevisionContent[revisionID]
	s.mu.Unlock()
	if !ok {
		s.errorJSON(w, http.StatusNotFound, "notFound", "Revision not found: "+revisionID+".")
		return
	}
	s.serveBytes(w, r, []byte(content), r.URL.Query().Get("mimeType"))
}
