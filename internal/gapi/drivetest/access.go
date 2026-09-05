package drivetest

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/mmedum/google-drive-mcp/internal/gdrive"
)

// handleCreatePermission serves permissions.create with the rules that
// bite. Each of them is here because a fake that accepts what Drive
// refuses lets the bug through:
//
//   - one permission per principal, so a second create is a 400 and the
//     caller has to update instead;
//   - sendNotificationEmail only for a user or a group;
//   - transferOwnership required for the owner role;
//   - an expiry only on a user or group grant, in the future, within a
//     year.
func (s *Server) handleCreatePermission(w http.ResponseWriter, r *http.Request, fileID string) {
	var meta gdrive.PermissionMeta
	if !s.decodeJSON(w, r, &meta) {
		return
	}
	q := r.URL.Query()
	s.mu.Lock()
	subject := s.permissionSubjectLocked(fileID)
	s.mu.Unlock()
	if subject == "" {
		s.errorJSON(w, http.StatusNotFound, "notFound", "File not found: "+fileID+".")
		return
	}
	if err := validatePermission(&meta, q); err != nil {
		s.writeAPIError(w, err)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	want := &gdrive.Permission{
		Type: meta.Type, Role: meta.Role, EmailAddress: meta.EmailAddress,
		Domain: meta.Domain, ExpirationTime: meta.ExpirationTime,
	}
	for _, existing := range s.Permissions[subject] {
		if samePrincipal(existing, want) {
			s.errorJSON(w, http.StatusBadRequest, "invalidSharingRequest",
				"The user already has access. To change the role, update the existing permission.")
			return
		}
	}
	if meta.AllowFileDiscovery != nil {
		want.AllowFileDiscovery = *meta.AllowFileDiscovery
	}
	// A consumer-account transfer waits to be accepted; in a shared drive
	// there is no ownership to hand over at all.
	f := s.fileLocked(subject)
	if meta.Role == "owner" && (f == nil || f.DriveID == "") {
		want.PendingOwner = true
	}
	s.nextID++
	want.ID = fmt.Sprintf("id-permission-%d", s.nextID)
	want.DisplayName = displayNameFor(want)
	s.Permissions[subject] = append(s.Permissions[subject], want)
	if f != nil {
		f.Shared = true
		s.recordChangeLocked(f)
	}
	writeJSON(w, want)
}

// validatePermission is the request-shape half of what Drive checks,
// with Drive's own statuses and reasons so the client's mapping is
// exercised rather than assumed.
func validatePermission(meta *gdrive.PermissionMeta, q map[string][]string) error {
	get := func(key string) string {
		if v := q[key]; len(v) > 0 {
			return v[0]
		}
		return ""
	}
	switch meta.Type {
	case "user", "group":
		if meta.EmailAddress == "" {
			return &apiFailure{http.StatusBadRequest, "invalid",
				"emailAddress is required for a permission of type " + meta.Type + "."}
		}
	case "domain":
		if meta.Domain == "" {
			return &apiFailure{http.StatusBadRequest, "invalid", "domain is required for a domain permission."}
		}
	case "anyone":
	default:
		return &apiFailure{http.StatusBadRequest, "invalid", "Invalid permission type: " + meta.Type + "."}
	}
	notifiable := meta.Type == "user" || meta.Type == "group"
	if get("sendNotificationEmail") != "" && !notifiable {
		return &apiFailure{http.StatusBadRequest, "invalid",
			"sendNotificationEmail is not allowed for permissions of type " + meta.Type + "."}
	}
	if meta.Role == "owner" {
		if get("transferOwnership") != "true" {
			return &apiFailure{http.StatusBadRequest, "invalid",
				"The transferOwnership parameter must be enabled when the permission role is 'owner'."}
		}
		if get("sendNotificationEmail") == "false" {
			return &apiFailure{http.StatusBadRequest, "invalid",
				"Notification emails cannot be disabled for ownership transfers."}
		}
	}
	if meta.ExpirationTime == "" {
		return nil
	}
	if !notifiable {
		return &apiFailure{http.StatusBadRequest, "invalid",
			"Expiration dates can only be set on user and group permissions."}
	}
	if _, err := time.Parse(time.RFC3339, meta.ExpirationTime); err != nil {
		return &apiFailure{http.StatusBadRequest, "invalid", "Invalid value for expirationTime."}
	}
	return nil
}

// handleUpdatePermission serves permissions.update: the role, the
// expiry, and whether a link grant is findable by search.
func (s *Server) handleUpdatePermission(w http.ResponseWriter, r *http.Request, fileID, permissionID string) {
	var meta gdrive.PermissionMeta
	if !s.decodeJSON(w, r, &meta) {
		return
	}
	q := r.URL.Query()
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.permissionLocked(fileID, permissionID)
	if p == nil {
		s.errorJSON(w, http.StatusNotFound, "notFound", "Permission not found: "+permissionID+".")
		return
	}
	if meta.Role == "owner" && q.Get("transferOwnership") != "true" {
		s.errorJSON(w, http.StatusBadRequest, "invalid",
			"The transferOwnership parameter must be enabled when the permission role is 'owner'.")
		return
	}
	if inherited, from := p.Inherited(); inherited {
		s.errorJSON(w, http.StatusForbidden, "insufficientFilePermissions",
			"The permission is inherited from "+from+" and cannot be changed here.")
		return
	}
	if meta.Role != "" {
		p.Role = meta.Role
	}
	if meta.AllowFileDiscovery != nil {
		p.AllowFileDiscovery = *meta.AllowFileDiscovery
	}
	if meta.ExpirationTime != "" {
		p.ExpirationTime = meta.ExpirationTime
	}
	if q.Get("removeExpiration") == "true" {
		p.ExpirationTime = ""
	}
	if f := s.fileLocked(fileID); f != nil {
		s.recordChangeLocked(f)
	}
	writeJSON(w, p)
}

// handleDeletePermission serves permissions.delete, refusing an
// inherited grant the way Drive does: it can only go where it was given.
func (s *Server) handleDeletePermission(w http.ResponseWriter, fileID, permissionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	subject := s.permissionSubjectLocked(fileID)
	perms := s.Permissions[subject]
	for i, p := range perms {
		if p.ID != permissionID {
			continue
		}
		if inherited, from := p.Inherited(); inherited {
			s.errorJSON(w, http.StatusForbidden, "insufficientFilePermissions",
				"The permission is inherited from "+from+" and cannot be removed here.")
			return
		}
		s.Permissions[subject] = append(append([]*gdrive.Permission(nil), perms[:i]...), perms[i+1:]...)
		if f := s.fileLocked(subject); f != nil {
			f.Shared = len(s.Permissions[subject]) > 0
			s.recordChangeLocked(f)
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.errorJSON(w, http.StatusNotFound, "notFound", "Permission not found: "+permissionID+".")
}

// handleGetPermission serves permissions.get.
func (s *Server) handleGetPermission(w http.ResponseWriter, fileID, permissionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.permissionLocked(fileID, permissionID)
	if p == nil {
		s.errorJSON(w, http.StatusNotFound, "notFound", "Permission not found: "+permissionID+".")
		return
	}
	writeJSON(w, p)
}

// permissionLocked finds one grant on a file, inherited ones included.
// The caller holds the lock.
func (s *Server) permissionLocked(fileID, permissionID string) *gdrive.Permission {
	for _, p := range s.grantsLocked(fileID) {
		if p.ID == permissionID {
			return p
		}
	}
	return nil
}

// permissionSubjectLocked resolves the id a permission is stored under,
// which is the "root" alias's real id when that is what was passed.
func (s *Server) permissionSubjectLocked(id string) string {
	if f := s.fileLocked(id); f != nil {
		return f.ID
	}
	if _, ok := s.Drives[id]; ok {
		return id
	}
	return ""
}

// grantsLocked is every grant that reaches a file: its own, plus the
// ones a shared drive passes down. Drive reports an inherited grant on
// the item with permissionDetails saying where it came from, and it can
// only be removed there — which is the case list_permissions and
// unshare_file both have to get right. The caller holds the lock.
func (s *Server) grantsLocked(fileID string) []*gdrive.Permission {
	f := s.fileLocked(fileID)
	if f == nil {
		return s.Permissions[fileID]
	}
	own := s.Permissions[f.ID]
	// The drive's own root carries its membership directly; only the
	// items inside it inherit.
	if f.DriveID == "" || f.DriveID == f.ID {
		return own
	}
	out := make([]*gdrive.Permission, 0, len(own))
	for _, p := range s.Permissions[f.DriveID] {
		inherited := *p
		inherited.Details = []*gdrive.PermissionDetails{{
			PermissionType: "member", Role: p.Role, Inherited: true, InheritedFrom: f.DriveID,
		}}
		out = append(out, &inherited)
	}
	return append(out, own...)
}

func displayNameFor(p *gdrive.Permission) string {
	switch p.Type {
	case "user", "group":
		if name, _, ok := strings.Cut(p.EmailAddress, "@"); ok {
			return strings.ToUpper(name[:1]) + name[1:]
		}
	}
	return ""
}
