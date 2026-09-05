package drivetest

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mmedum/google-drive-mcp/internal/gdrive"
)

// handleListRevisions serves revisions.list, oldest first as Drive sends
// them.
func (s *Server) handleListRevisions(w http.ResponseWriter, fileID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.fileLocked(fileID)
	if f == nil {
		s.errorJSON(w, http.StatusNotFound, "notFound", "File not found: "+fileID+".")
		return
	}
	revs := append([]*gdrive.Revision(nil), s.Revisions[f.ID]...)
	writeJSON(w, gdrive.RevisionList{Revisions: revs})
}

// handleDeleteRevision serves revisions.delete with the two refusals
// Drive states: no revision of a Docs editors file, and never the one
// the file is currently at.
func (s *Server) handleDeleteRevision(w http.ResponseWriter, fileID, revisionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.fileLocked(fileID)
	if f == nil {
		s.errorJSON(w, http.StatusNotFound, "notFound", "File not found: "+fileID+".")
		return
	}
	if f.IsWorkspaceDoc() {
		s.errorJSON(w, http.StatusForbidden, "revisionDeletionNotSupported",
			"Revision deletion is not supported for this file.")
		return
	}
	revs := s.Revisions[f.ID]
	for i, rev := range revs {
		if rev.ID != revisionID {
			continue
		}
		if rev.ID == f.HeadRevisionID {
			s.errorJSON(w, http.StatusForbidden, "revisionDeletionNotSupported",
				"The last remaining file version cannot be deleted.")
			return
		}
		s.Revisions[f.ID] = append(append([]*gdrive.Revision(nil), revs[:i]...), revs[i+1:]...)
		delete(s.RevisionContent, rev.ID)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.errorJSON(w, http.StatusNotFound, "notFound", "Revision not found: "+revisionID+".")
}

// handleDeleteFile serves files.delete, taking a folder's contents with
// it as Drive does.
func (s *Server) handleDeleteFile(w http.ResponseWriter, fileID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.fileLocked(fileID)
	if f == nil {
		s.errorJSON(w, http.StatusNotFound, "notFound", "File not found: "+fileID+".")
		return
	}
	s.deleteLocked(f)
	w.WriteHeader(http.StatusNoContent)
}

// deleteLocked removes a file and everything under it, recording the
// change so the feed reports the removal. The caller holds the lock.
func (s *Server) deleteLocked(f *gdrive.File) {
	for _, child := range s.childrenLocked(f.ID) {
		s.deleteLocked(child)
	}
	delete(s.Files, f.ID)
	delete(s.Content, f.ID)
	for _, rev := range s.Revisions[f.ID] {
		delete(s.RevisionContent, rev.ID)
	}
	delete(s.Revisions, f.ID)
	delete(s.Permissions, f.ID)
	s.recordRemovalLocked(f)
}

// handleEmptyTrash serves files.emptyTrash, for this account or for one
// shared drive.
func (s *Server) handleEmptyTrash(w http.ResponseWriter, r *http.Request) {
	driveID := r.URL.Query().Get("driveId")
	s.mu.Lock()
	defer s.mu.Unlock()
	var doomed []*gdrive.File
	for _, f := range s.Files {
		if f.Trashed && f.DriveID == driveID {
			doomed = append(doomed, f)
		}
	}
	for _, f := range doomed {
		// A folder's contents go with it, so a child already removed by
		// its parent is not there to remove again.
		if _, still := s.Files[f.ID]; still {
			s.deleteLocked(f)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDrive serves drives.get, drives.update and drives.delete.
func (s *Server) handleDrive(w http.ResponseWriter, r *http.Request, driveID string) {
	s.mu.Lock()
	d := s.Drives[driveID]
	s.mu.Unlock()
	if d == nil {
		s.errorJSON(w, http.StatusNotFound, "notFound", "Shared drive not found: "+driveID+".")
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, d)
	case http.MethodPatch:
		s.patchDrive(w, r, d)
	case http.MethodDelete:
		s.deleteDrive(w, d)
	default:
		s.errorJSON(w, http.StatusNotFound, "notFound", "the fake does not implement "+r.Method+" on a drive")
	}
}

func (s *Server) patchDrive(w http.ResponseWriter, r *http.Request, d *gdrive.Drive) {
	var meta gdrive.DriveMeta
	if !s.decodeJSON(w, r, &meta) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if meta.Name != "" {
		d.Name = meta.Name
		// A shared drive's root folder is the drive, so it is renamed too.
		if root := s.Files[d.ID]; root != nil {
			root.Name = meta.Name
		}
	}
	if meta.ColorRgb != "" {
		d.ColorRgb = meta.ColorRgb
	}
	if meta.Hidden != nil {
		d.Hidden = *meta.Hidden
	}
	if meta.Restrictions != nil {
		// Drive replaces the whole object, which is why the client sends
		// the restrictions it is keeping along with the one it changes.
		copied := *meta.Restrictions
		d.Restrictions = &copied
	}
	s.recordDriveChangeLocked(d)
	writeJSON(w, d)
}

// deleteDrive refuses a drive that still holds anything untrashed, which
// is the safeguard the reference names.
func (s *Server) deleteDrive(w http.ResponseWriter, d *gdrive.Drive) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, f := range s.Files {
		if f.DriveID == d.ID && f.ID != d.ID && !f.Trashed {
			s.errorJSON(w, http.StatusForbidden, "cannotDeleteResource",
				"A shared drive cannot be deleted while it still contains items.")
			return
		}
	}
	for _, f := range s.Files {
		if f.DriveID == d.ID {
			delete(s.Files, f.ID)
			delete(s.Permissions, f.ID)
		}
	}
	delete(s.Drives, d.ID)
	delete(s.Permissions, d.ID)
	w.WriteHeader(http.StatusNoContent)
}

// handleCreateDrive serves drives.create, with the requestId that makes
// it idempotent: a repeat carrying one Drive has already seen answers
// with the drive it made rather than making a second.
func (s *Server) handleCreateDrive(w http.ResponseWriter, r *http.Request) {
	requestID := r.URL.Query().Get("requestId")
	if requestID == "" {
		s.errorJSON(w, http.StatusBadRequest, "invalid", "The requestId parameter is required.")
		return
	}
	var meta gdrive.DriveMeta
	if !s.decodeJSON(w, r, &meta) {
		return
	}
	if strings.TrimSpace(meta.Name) == "" {
		s.errorJSON(w, http.StatusBadRequest, "invalid", "A shared drive must have a name.")
		return
	}
	s.mu.Lock()
	if id, seen := s.driveRequests[requestID]; seen {
		d := s.Drives[id]
		s.mu.Unlock()
		writeJSON(w, d)
		return
	}
	s.nextID++
	id := fmt.Sprintf("id-drive-fixture-%d", s.nextID)
	s.driveRequests[requestID] = id
	s.mu.Unlock()

	d := s.AddDrive(id, meta.Name)
	s.mu.Lock()
	if meta.Restrictions != nil {
		copied := *meta.Restrictions
		d.Restrictions = &copied
	}
	// The creator is the drive's organizer, which is what makes every
	// item in it inherit a grant.
	s.nextID++
	s.Permissions[id] = append(s.Permissions[id], &gdrive.Permission{
		ID: fmt.Sprintf("id-permission-%d", s.nextID), Type: "user", Role: "organizer",
		EmailAddress: AccountEmail, DisplayName: AccountName,
	})
	s.recordDriveChangeLocked(d)
	s.mu.Unlock()
	writeJSON(w, d)
}

// handleDriveVisibility serves drives.hide and drives.unhide, which the
// reference gives their own endpoints rather than a field on a patch.
func (s *Server) handleDriveVisibility(w http.ResponseWriter, driveID string, hidden bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.Drives[driveID]
	if d == nil {
		s.errorJSON(w, http.StatusNotFound, "notFound", "Shared drive not found: "+driveID+".")
		return
	}
	d.Hidden = hidden
	s.recordDriveChangeLocked(d)
	writeJSON(w, d)
}

// handleStartPageToken serves changes.getStartPageToken: the point in
// the feed that "from now on" means, which here is how many changes have
// been recorded.
func (s *Server) handleStartPageToken(w http.ResponseWriter, r *http.Request) {
	driveID := r.URL.Query().Get("driveId")
	s.mu.Lock()
	defer s.mu.Unlock()
	if driveID != "" && s.Drives[driveID] == nil {
		s.errorJSON(w, http.StatusNotFound, "notFound", "Shared drive not found: "+driveID+".")
		return
	}
	writeJSON(w, gdrive.StartPageToken{StartPageToken: strconv.Itoa(len(s.Changes)), Kind: "drive#startPageToken"})
}

// handleListChanges serves changes.list. The token is an offset into the
// recorded changes, which is enough to exercise what the client has to
// get right: a token is opaque, a page carries the next one, and the
// last page carries the token for next time.
func (s *Server) handleListChanges(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	token := q.Get("pageToken")
	if token == "" {
		s.errorJSON(w, http.StatusBadRequest, "invalid", "The pageToken parameter is required.")
		return
	}
	start, err := strconv.Atoi(token)
	if err != nil || start < 0 {
		s.errorJSON(w, http.StatusBadRequest, "invalid", "Invalid Value for pageToken: "+token)
		return
	}
	driveID := q.Get("driveId")
	includeRemoved := q.Get("includeRemoved") != "false"
	restrictToMyDrive := q.Get("restrictToMyDrive") == "true"
	pageSize := 100
	if n, err := strconv.Atoi(q.Get("pageSize")); err == nil && n > 0 {
		pageSize = n
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if start > len(s.Changes) {
		s.errorJSON(w, http.StatusBadRequest, "invalid", "The pageToken is from a different feed or has expired.")
		return
	}
	out := gdrive.ChangeList{Changes: []*gdrive.Change{}}
	at := start
	for ; at < len(s.Changes) && len(out.Changes) < pageSize; at++ {
		c := s.Changes[at]
		if !includeRemoved && c.Removed {
			continue
		}
		if driveID != "" && c.DriveID != driveID {
			continue
		}
		if restrictToMyDrive && c.DriveID != "" {
			continue
		}
		// The feed reports the item as it is now, not as it was: a change
		// is a pointer to something that moved, and reading it is a
		// separate call.
		entry := *c
		if entry.FileID != "" {
			entry.File = s.Files[entry.FileID]
			entry.Removed = entry.File == nil
		}
		out.Changes = append(out.Changes, &entry)
	}
	if at < len(s.Changes) {
		out.NextPageToken = strconv.Itoa(at)
	} else {
		out.NewStartPageToken = strconv.Itoa(len(s.Changes))
	}
	writeJSON(w, out)
}

// recordChangeLocked notes that a file changed. The caller holds the
// lock.
func (s *Server) recordChangeLocked(f *gdrive.File) {
	if f == nil {
		return
	}
	s.Changes = append(s.Changes, &gdrive.Change{
		ChangeType: "file", FileID: f.ID, DriveID: f.DriveID,
		Time: s.now().UTC().Format(time.RFC3339),
	})
}

// recordRemovalLocked notes that a file is gone. The caller holds the
// lock.
func (s *Server) recordRemovalLocked(f *gdrive.File) {
	if f == nil {
		return
	}
	s.Changes = append(s.Changes, &gdrive.Change{
		ChangeType: "file", FileID: f.ID, DriveID: f.DriveID, Removed: true,
		Time: s.now().UTC().Format(time.RFC3339),
	})
}

// recordDriveChangeLocked notes that a shared drive changed. The caller
// holds the lock.
func (s *Server) recordDriveChangeLocked(d *gdrive.Drive) {
	if d == nil {
		return
	}
	s.Changes = append(s.Changes, &gdrive.Change{
		ChangeType: "drive", DriveID: d.ID, Drive: d,
		Time: s.now().UTC().Format(time.RFC3339),
	})
}
