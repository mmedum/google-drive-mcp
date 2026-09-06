package drivetest

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/mmedum/google-drive-mcp/internal/gapi"
	"github.com/mmedum/google-drive-mcp/internal/gdrive"
)

// The fake serves both halves of labels, because the split between them
// is the thing most likely to be got wrong: the values on a file come
// from Drive under the ordinary scope, and the definitions come from a
// separate API on a separate host. Serving them from one process is a
// convenience of the fake and not a claim about Google.

// handleListFileLabels answers files.listLabels: the labels applied to
// one file, which is the only way to ask without knowing their ids.
func (s *Server) handleListFileLabels(w http.ResponseWriter, r *http.Request, id string) {
	s.mu.Lock()
	_, ok := s.Files[id]
	labels := append([]*gdrive.Label(nil), s.FileLabels[id]...)
	s.mu.Unlock()
	if !ok {
		s.errorJSON(w, http.StatusNotFound, "notFound", "File not found: "+id+".")
		return
	}
	sort.Slice(labels, func(i, j int) bool { return labels[i].ID < labels[j].ID })
	// files.listLabels spells its page size maxResults rather than
	// pageSize, so the shared window is given a query that uses the name
	// it knows. Paging it at all is what gives AllFileLabels' loop a test
	// that can fail.
	q := r.URL.Query()
	if q.Get("pageSize") == "" && q.Get("maxResults") != "" {
		q.Set("pageSize", q.Get("maxResults"))
	}
	start, end, ok := s.pageWindow(w, q, len(labels), gapi.MaxFileLabelPageSize, gapi.MaxFileLabelPageSize)
	if !ok {
		return
	}
	page := gdrive.LabelList{Labels: append([]*gdrive.Label{}, labels[start:end]...)}
	if end < len(labels) {
		page.NextPageToken = "offset-" + strconv.Itoa(end)
	}
	writeJSON(w, page)
}

// handleModifyLabels answers files.modifyLabels. The reference promises
// the modifications apply together or not at all, so the fake validates
// every one of them before it changes anything.
func (s *Server) handleModifyLabels(w http.ResponseWriter, r *http.Request, id string) {
	var req gdrive.ModifyLabelsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.errorJSON(w, http.StatusBadRequest, "badRequest", "Invalid request body.")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.Files[id]; !ok {
		s.errorJSON(w, http.StatusNotFound, "notFound", "File not found: "+id+".")
		return
	}
	if len(req.LabelModifications) == 0 {
		s.errorJSON(w, http.StatusBadRequest, "badRequest", "No label modifications were given.")
		return
	}
	for _, m := range req.LabelModifications {
		if m.LabelID == "" {
			s.errorJSON(w, http.StatusBadRequest, "badRequest", "A label modification needs a labelId.")
			return
		}
		if m.RemoveLabel && len(m.FieldModifications) > 0 {
			s.errorJSON(w, http.StatusBadRequest, "badRequest",
				"A label modification cannot both remove the label and modify its fields.")
			return
		}
		for _, fm := range m.FieldModifications {
			if fm.FieldID == "" {
				s.errorJSON(w, http.StatusBadRequest, "badRequest", "A field modification needs a fieldId.")
				return
			}
		}
	}

	applied := s.FileLabels[id]
	var modified []*gdrive.Label
	for _, m := range req.LabelModifications {
		if m.RemoveLabel {
			kept := applied[:0:0]
			for _, l := range applied {
				if l.ID != m.LabelID {
					kept = append(kept, l)
				}
			}
			applied = kept
			continue
		}
		var target *gdrive.Label
		for _, l := range applied {
			if l.ID == m.LabelID {
				target = l
				break
			}
		}
		if target == nil {
			target = &gdrive.Label{ID: m.LabelID, RevisionID: "1", Fields: map[string]gdrive.LabelField{}}
			applied = append(applied, target)
		}
		if target.Fields == nil {
			target.Fields = map[string]gdrive.LabelField{}
		}
		for _, fm := range m.FieldModifications {
			if fm.UnsetValues {
				delete(target.Fields, fm.FieldID)
				continue
			}
			target.Fields[fm.FieldID] = fieldFromModification(fm)
		}
		modified = append(modified, target)
	}
	s.FileLabels[id] = applied
	writeJSON(w, gdrive.ModifyLabelsResponse{ModifiedLabels: modified})
}

// fieldFromModification turns one field modification into the applied
// field Drive would report back. Each setter replaces the field's values
// rather than adding to them, and the value type follows from which
// setter was used.
func fieldFromModification(fm gdrive.LabelFieldModification) gdrive.LabelField {
	f := gdrive.LabelField{ID: fm.FieldID}
	switch {
	case len(fm.SetTextValues) > 0:
		f.ValueType, f.Text = "text", fm.SetTextValues
	case len(fm.SetSelectionValues) > 0:
		f.ValueType, f.Selection = "selection", fm.SetSelectionValues
	case len(fm.SetIntegerValues) > 0:
		f.ValueType, f.Integer = "integer", fm.SetIntegerValues
	case len(fm.SetDateValues) > 0:
		f.ValueType, f.Date = "dateString", fm.SetDateValues
	case len(fm.SetUserValues) > 0:
		f.ValueType = "user"
		for _, address := range fm.SetUserValues {
			f.User = append(f.User, &gdrive.User{EmailAddress: address})
		}
	}
	return f
}

// handleListLabelDefinitions answers the Drive Labels API's labels.list.
// It is served from the same process on a path of its own, standing in
// for a second host.
func (s *Server) handleListLabelDefinitions(w http.ResponseWriter, r *http.Request) {
	if v := r.URL.Query().Get("view"); v != "LABEL_VIEW_FULL" {
		// A listing without the full view carries no fields, so a caller
		// that forgets it gets a label it cannot set anything on. The
		// fake refuses rather than returning the trap.
		s.errorJSON(w, http.StatusBadRequest, "badRequest",
			"the fake serves only view=LABEL_VIEW_FULL; a basic view omits the fields")
		return
	}
	s.mu.Lock()
	defs := append([]*gdrive.LabelDefinition(nil), s.LabelDefinitions...)
	s.mu.Unlock()
	start, end, ok := s.pageWindow(w, r.URL.Query(),
		len(defs), gapi.DefaultLabelPageSize, gapi.MaxLabelPageSize)
	if !ok {
		return
	}
	page := gdrive.LabelDefinitionList{Labels: append([]*gdrive.LabelDefinition{}, defs[start:end]...)}
	if end < len(defs) {
		page.NextPageToken = "offset-" + strconv.Itoa(end)
	}
	writeJSON(w, page)
}

// scopeDenied answers the way the Labels and Activity APIs answer a
// token that has not been granted their scope. The body is theirs and
// not Drive's: it carries no `errors` array at all, only the RPC status
// and an ErrorInfo detail. A fake that answered in Drive's shape here
// would let the client's mapping pass on a body it will never see.
func (s *Server) scopeDenied(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{
		"code":    http.StatusForbidden,
		"status":  "PERMISSION_DENIED",
		"message": "Request had insufficient authentication scopes.",
		"details": []map[string]any{{
			"@type":  "type.googleapis.com/google.rpc.ErrorInfo",
			"reason": "ACCESS_TOKEN_SCOPE_INSUFFICIENT",
			"domain": "googleapis.com",
		}},
	}})
}

// AddLabelDefinition puts a published label definition in the fake, with
// one text field and one selection field unless fields are given.
func (s *Server) AddLabelDefinition(id, title string, fields ...*gdrive.LabelFieldDefinition) *gdrive.LabelDefinition {
	s.mu.Lock()
	defer s.mu.Unlock()
	def := &gdrive.LabelDefinition{
		Name:       "labels/" + id + "@1",
		ID:         id,
		RevisionID: "1",
		LabelType:  "SHARED",
		Properties: &gdrive.LabelDefinitionProperties{Title: title},
		Lifecycle:  &gdrive.LabelLifecycle{State: "PUBLISHED"},
		Fields:     fields,
		AppliedCapabilities: &gdrive.LabelAppliedCapabilities{
			CanRead: true, CanApply: true, CanRemove: true,
		},
	}
	s.LabelDefinitions = append(s.LabelDefinitions, def)
	return def
}

// TextFieldDefinition builds a text field for a label definition.
func TextFieldDefinition(id, displayName string) *gdrive.LabelFieldDefinition {
	return &gdrive.LabelFieldDefinition{
		ID:                  id,
		QueryKey:            "labels/" + id,
		Properties:          &gdrive.LabelFieldProperties{DisplayName: displayName},
		Lifecycle:           &gdrive.LabelLifecycle{State: "PUBLISHED"},
		AppliedCapabilities: &gdrive.LabelFieldAppliedCapabilities{CanRead: true, CanWrite: true, CanSearch: true},
		TextOptions:         &struct{}{},
	}
}

// SelectionFieldDefinition builds a selection field whose choices are
// the id/display-name pairs given, in order.
func SelectionFieldDefinition(id, displayName string, choices ...[2]string) *gdrive.LabelFieldDefinition {
	f := &gdrive.LabelFieldDefinition{
		ID:                  id,
		QueryKey:            "labels/" + id,
		Properties:          &gdrive.LabelFieldProperties{DisplayName: displayName},
		Lifecycle:           &gdrive.LabelLifecycle{State: "PUBLISHED"},
		AppliedCapabilities: &gdrive.LabelFieldAppliedCapabilities{CanRead: true, CanWrite: true, CanSearch: true},
		SelectionOptions:    &gdrive.LabelSelectionOptions{},
	}
	for _, c := range choices {
		f.SelectionOptions.Choices = append(f.SelectionOptions.Choices, &gdrive.LabelChoice{
			ID:         c[0],
			Properties: &gdrive.LabelChoiceProperties{DisplayName: c[1]},
			Lifecycle:  &gdrive.LabelLifecycle{State: "PUBLISHED"},
		})
	}
	return f
}

// ApplyLabel puts a label's applied values on a file, as a fixture.
func (s *Server) ApplyLabel(fileID string, label *gdrive.Label) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.FileLabels[fileID] = append(s.FileLabels[fileID], label)
}

// labelIDsFrom reads the includeLabels parameter, which is a
// comma-separated list of label ids. A wildcard is NOT one: Drive answers
// 400 to `includeLabels=*`, and this server sent exactly that from phase
// 1 until phase 4 without a single test noticing, because no test could
// — the fake accepted anything. It refuses now, with Drive's own status.
func labelIDsFrom(raw string) ([]string, bool) {
	if raw == "" {
		return nil, true
	}
	ids := strings.Split(raw, ",")
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || id == "*" {
			return nil, false
		}
		out = append(out, id)
	}
	return out, true
}
