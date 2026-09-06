package drivetest

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/mmedum/google-drive-mcp/internal/gdrive"
)

// handleActivityQuery answers the Drive Activity API's one method. Like
// the Labels API it is a separate host stood in for by a prefix here.
//
// The fake filters on itemName and ancestorName and on the action kinds,
// because those are the parts the client builds and can get wrong. It
// does NOT implement the filter language in general: a fake that parsed
// an expression language would be asserting its own parser rather than
// the client's request.
func (s *Server) handleActivityQuery(w http.ResponseWriter, r *http.Request) {
	var q gdrive.ActivityQuery
	if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
		s.errorJSON(w, http.StatusBadRequest, "badRequest", "Invalid request body.")
		return
	}
	if q.ItemName == "" && q.AncestorName == "" {
		s.errorJSON(w, http.StatusBadRequest, "badRequest",
			"one of itemName and ancestorName is required")
		return
	}
	if q.ItemName != "" && q.AncestorName != "" {
		s.errorJSON(w, http.StatusBadRequest, "badRequest",
			"itemName and ancestorName are alternatives")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	want := strings.TrimPrefix(q.ItemName+q.AncestorName, "items/")
	ids := map[string]bool{want: true}
	if q.AncestorName != "" {
		for id := range s.Files {
			if s.descendsFromLocked(id, want) {
				ids[id] = true
			}
		}
	}
	var out []*gdrive.DriveActivity
	for _, a := range s.Activity {
		if !ids[activitySubjectID(a)] {
			continue
		}
		if !matchesActionFilter(a, q.Filter) {
			continue
		}
		out = append(out, a)
	}
	writeJSON(w, gdrive.ActivityResponse{Activities: out})
}

// activitySubjectID pulls the file id out of an activity's first target.
func activitySubjectID(a *gdrive.DriveActivity) string {
	for _, t := range a.Targets {
		if t != nil && t.DriveItem != nil {
			return strings.TrimPrefix(t.DriveItem.Name, "items/")
		}
	}
	return ""
}

// matchesActionFilter honours only the action-kind clause, which is the
// part the client composes.
func matchesActionFilter(a *gdrive.DriveActivity, filter string) bool {
	const key = "detail.action_detail_case:"
	i := strings.Index(filter, key)
	if i < 0 {
		return true
	}
	rest := filter[i+len(key):]
	if end := strings.Index(rest, " AND "); end >= 0 && !strings.HasPrefix(rest, "(") {
		rest = rest[:end]
	}
	rest = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(rest), "("), ")")
	for _, want := range strings.Fields(rest) {
		if activityKind(a.PrimaryActionDetail) == want {
			return true
		}
	}
	return false
}

// activityKind names which detail member is set, which is how the API
// itself says what an action was.
func activityKind(d *gdrive.ActionDetail) string {
	switch {
	case d == nil:
		return ""
	case d.Create != nil:
		return "CREATE"
	case d.Edit != nil:
		return "EDIT"
	case d.Rename != nil:
		return "RENAME"
	case d.Move != nil:
		return "MOVE"
	case d.Delete != nil:
		return "DELETE"
	case d.Restore != nil:
		return "RESTORE"
	case d.PermissionChange != nil:
		return "PERMISSION_CHANGE"
	case d.Comment != nil:
		return "COMMENT"
	case d.AppliedLabelChange != nil:
		return "APPLIED_LABEL_CHANGE"
	}
	return ""
}

// descendsFromLocked reports whether id sits under ancestor. The caller
// holds the lock.
func (s *Server) descendsFromLocked(id, ancestor string) bool {
	for depth := 0; depth < 20; depth++ {
		f := s.Files[id]
		if f == nil {
			return false
		}
		parent := f.Parent()
		if parent == "" {
			return false
		}
		if parent == ancestor {
			return true
		}
		id = parent
	}
	return false
}

// AddActivity records one event in the fake, as the given kind of action
// on a file. mine says the signed-in account did it.
func (s *Server) AddActivity(fileID, kind string, mine bool, at string) *gdrive.DriveActivity {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.Files[fileID]
	title := ""
	if f != nil {
		title = f.Name
	}
	a := &gdrive.DriveActivity{
		Timestamp: at,
		Targets: []*gdrive.ActivityTarget{{DriveItem: &gdrive.ActivityDriveItem{
			Name: "items/" + fileID, Title: title,
		}}},
		Actors: []*gdrive.ActivityActor{{User: &gdrive.ActivityUser{
			// A People API resource name and a flag, which is everything
			// this API will say about a person.
			KnownUser: &gdrive.ActivityKnownUser{PersonName: "people/1234567890", IsCurrentUser: mine},
		}}},
		PrimaryActionDetail: detailFor(kind),
	}
	s.Activity = append(s.Activity, a)
	return a
}

// detailFor builds the action detail for a kind name.
func detailFor(kind string) *gdrive.ActionDetail {
	switch kind {
	case "CREATE":
		return &gdrive.ActionDetail{Create: &gdrive.ActivityCreate{New: &struct{}{}}}
	case "EDIT":
		return &gdrive.ActionDetail{Edit: &struct{}{}}
	case "RENAME":
		return &gdrive.ActionDetail{Rename: &gdrive.ActivityRename{OldTitle: "Before", NewTitle: "After"}}
	case "MOVE":
		return &gdrive.ActionDetail{Move: &gdrive.ActivityMove{}}
	case "DELETE":
		return &gdrive.ActionDetail{Delete: &gdrive.ActivityTyped{Type: "TRASH"}}
	case "RESTORE":
		return &gdrive.ActionDetail{Restore: &gdrive.ActivityTyped{Type: "UNTRASH"}}
	case "PERMISSION_CHANGE":
		return &gdrive.ActionDetail{PermissionChange: &gdrive.ActivityPermission{
			AddedPermissions: []map[string]any{{"role": "READER"}},
		}}
	case "COMMENT":
		return &gdrive.ActionDetail{Comment: &gdrive.ActivityComment{Post: &struct{}{}}}
	case "APPLIED_LABEL_CHANGE":
		return &gdrive.ActionDetail{AppliedLabelChange: &struct{}{}}
	}
	return nil
}
