package service

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mmedum/google-drive-mcp/internal/gapi"
	"github.com/mmedum/google-drive-mcp/internal/gdrive"
	"github.com/mmedum/google-drive-mcp/internal/model"
	"github.com/mmedum/google-drive-mcp/internal/render"
)

// Labels arrive in this server through two APIs and one flag.
//
// GDRIVE_LABELS registers the two tools and adds the labels scopes at
// login. Applying a label needs neither — files.modifyLabels runs on the
// ordinary drive scope — but knowing WHICH label and WHICH field id to
// set does, because that lives in the definitions. A server that offered
// manage_labels without list_labels would be asking a model to guess a
// generated id, so the flag governs both.

// labelActions are what manage_labels does, in the order they read. The
// sibling tables in this package (roles, orderByKeys, activityActions)
// map a caller's word onto a different word Google uses; these are the
// same word either way, so this is a list rather than a map pretending
// to be a translation.
var labelActions = []string{"apply", "set_field", "unset_field", "remove"}

// LabelActions lists the actions manage_labels accepts, so the tool
// description and the error messages offer the words the code takes.
func LabelActions() []string { return labelActions }

// ListLabelsInput selects a page of label definitions.
type ListLabelsInput struct {
	PageSize  int
	PageToken string
	// Appliable narrows the listing to labels this account may actually
	// put on a file. The default is everything they can see, because a
	// label that is readable but not appliable still explains a value on
	// a file card.
	Appliable bool
}

// ListLabels reports the label definitions this account may use.
func (s *Service) ListLabels(ctx context.Context, in ListLabelsInput) (string, error) {
	if err := s.labelsOn(); err != nil {
		return "", err
	}
	size := in.PageSize
	if size > gapi.MaxLabelPageSize {
		return "", Errorf(ClassInvalid, "page_size %d is above the Drive Labels API's ceiling of %d",
			size, gapi.MaxLabelPageSize)
	}
	role := ""
	if in.Appliable {
		role = "APPLIER"
	}
	page, err := s.api.ListLabelDefinitions(ctx, gapi.ListLabelDefinitionsOptions{
		PageSize: size, PageToken: in.PageToken, MinimumRole: role,
	})
	if err != nil {
		return "", s.labelAPIError(err, "reading the label definitions")
	}
	defs := make([]*model.LabelDefinition, 0, len(page.Labels))
	dropped := 0
	for _, d := range page.Labels {
		if def := model.NewLabelDefinition(d); def != nil {
			defs = append(defs, def)
			continue
		}
		dropped++
	}
	note := ""
	if dropped > 0 {
		// Saying so matters: the count a caller sees would otherwise
		// disagree with the count an administrator sees in the console,
		// with nothing to explain the difference.
		note = fmt.Sprintf("%s on this page not published and cannot be applied, so left out of the list.",
			model.Plural(dropped, "label", "labels"))
	}
	return render.Labels(defs, render.LabelsOptions{NextPageToken: page.NextPageToken, Note: note}), nil
}

// ManageLabelsInput is one change to one label on one file.
type ManageLabelsInput struct {
	File   string
	Label  string
	Action string
	// Field is the field id to set or unset, for those two actions.
	Field string
	// Values are what to set the field to. Every setter the API offers
	// REPLACES the field's values, so this is the whole new value of the
	// field and not an addition to it.
	Values []string
}

// ManageLabels applies a label to a file, sets or clears one of its
// fields, or takes the label off.
//
// One label and one field per call, which is the same rule the rest of
// this surface follows: a change a person has to approve should be a
// change they can read in one line. The API would take a batch; a tool
// that took one would make "set three fields" a single approval whose
// text nobody checks.
func (s *Service) ManageLabels(ctx context.Context, in ManageLabelsInput) (*Result, error) {
	if err := s.labelsOn(); err != nil {
		return nil, err
	}
	if err := s.writable("manage_labels"); err != nil {
		return nil, err
	}
	action := strings.TrimSpace(in.Action)
	if !slices.Contains(labelActions, action) {
		return nil, Errorf(ClassInvalid, "action must be one of %s; got %q",
			strings.Join(LabelActions(), ", "), in.Action)
	}
	labelID := strings.TrimSpace(in.Label)
	if labelID == "" {
		return nil, Errorf(ClassInvalid, "label is required: the id of the label to change, from list_labels")
	}
	res, err := s.Resolve(ctx, in.File, ResolveOptions{FollowShortcut: true, Fresh: true})
	if err != nil {
		return nil, err
	}
	f := res.File
	// canModifyLabels rather than canEdit: Drive computes a capability for
	// exactly this, and the two come apart. A label can be locked so that
	// even an editor may not change it, and an organiser of a shared drive
	// can label a file they cannot edit.
	if f.Capabilities == nil || !f.Capabilities.CanModifyLabels {
		return nil, Errorf(ClassForbidden,
			"you cannot change the labels on %s: Drive's own canModifyLabels says no. "+
				"Labelling is a permission of its own, so this can be refused on a file you can otherwise edit",
			f.Name)
	}

	// Only set_field needs the definition, and it needs it enough to pay
	// a listing for: the API has one setter per field type and the type
	// is only there. apply, remove and unset_field need the label id and
	// nothing else, so they use the definitions when those are already in
	// hand — a wrong id is worth answering with the real ones — and go
	// straight to Drive when they are not.
	def, defErr := s.definitionFor(ctx, action, labelID)
	mod, err := s.labelModification(action, labelID, in, def, defErr)
	if err != nil {
		return nil, err
	}
	if _, err := s.api.ModifyLabels(ctx, f.ID, gdrive.ModifyLabelsRequest{
		LabelModifications: []gdrive.LabelModification{mod},
	}); err != nil {
		return nil, s.modifyLabelsError(err, f, labelID, def)
	}
	s.forget(f, false)

	// Read the whole file back with its labels rather than believing the
	// response. modifyLabels answers with only the labels it changed,
	// which says nothing about the ones it did not — and the question
	// after a change is what is on the file now, not what this call
	// touched. A removal makes the point: it comes back empty either way.
	after, err := s.Resolve(ctx, f.ID, ResolveOptions{FollowShortcut: false, Fresh: true, IncludeLabels: true})
	if err != nil {
		return nil, wrap(err, "reading "+f.Name+" back after the change")
	}
	return s.report(ctx, after, outcome{
		Action: render.ActionUpdated,
		Note:   labelActionWords(action, labelID, def) + " on " + f.Name + ".",
	}), nil
}

// labelModification turns one input into the modification to send. The
// validation that needs the definition is done here rather than left to
// Drive, because Drive's refusal names neither the field's type nor the
// choices that would have worked.
func (s *Service) labelModification(action, labelID string, in ManageLabelsInput,
	def *model.LabelDefinition, defErr error) (gdrive.LabelModification, error) {
	mod := gdrive.LabelModification{LabelID: labelID}
	switch action {
	case "apply":
		// An apply with no field modifications is what the API calls a
		// label with no values set. Required fields are not checked here:
		// the API decides, and a guess at its rule would be a second
		// opinion that can disagree.
		return mod, nil
	case "remove":
		mod.RemoveLabel = true
		return mod, nil
	}

	field := strings.TrimSpace(in.Field)
	if field == "" {
		return mod, Errorf(ClassInvalid,
			"field is required for %s: the id of the field to change, from list_labels", action)
	}
	if action == "unset_field" {
		mod.FieldModifications = []gdrive.LabelFieldModification{{FieldID: field, UnsetValues: true}}
		return mod, nil
	}

	values := nonEmpty(in.Values)
	if len(values) == 0 {
		return mod, Errorf(ClassInvalid,
			"values is required for set_field; to clear a field use action: unset_field")
	}
	fm, err := fieldModification(field, values, def, defErr)
	if err != nil {
		return mod, err
	}
	mod.FieldModifications = []gdrive.LabelFieldModification{fm}
	return mod, nil
}

// fieldModification chooses the setter that matches the field's type.
// The type comes from the definition, because the API has one setter per
// type and picking the wrong one is a refusal that does not say which
// one was right.
func fieldModification(field string, values []string,
	def *model.LabelDefinition, defErr error) (gdrive.LabelFieldModification, error) {
	fm := gdrive.LabelFieldModification{FieldID: field}
	if def == nil {
		return fm, Errorf(ClassInvalid,
			"the definition of label field %q could not be read, so the type of value it takes is unknown "+
				"and this server will not guess: %v", field, defErr)
	}
	fd, ok := def.Field(field)
	if !ok {
		known := make([]string, 0, len(def.Fields))
		for _, f := range def.Fields {
			known = append(known, f.ID)
		}
		if len(known) == 0 {
			return fm, Errorf(ClassNotFound,
				"label %q has no field %q; it has no fields at all, so it is applied on its own",
				def.ID, field)
		}
		return fm, Errorf(ClassNotFound, "label %q has no field %q; its fields are %s",
			def.ID, field, strings.Join(known, ", "))
	}
	if !fd.CanWrite {
		return fm, Errorf(ClassForbidden,
			"you may not set the field %q on label %q: the label's own permissions refuse it",
			field, def.ID)
	}
	switch fd.ValueType {
	case model.FieldText:
		fm.SetTextValues = values
	case model.FieldInteger:
		for _, v := range values {
			if _, err := strconv.ParseInt(v, 10, 64); err != nil {
				return fm, Errorf(ClassInvalid,
					"field %q takes a whole number; %q is not one", field, v)
			}
		}
		fm.SetIntegerValues = values
	case model.FieldDate:
		for _, v := range values {
			if !isCalendarDate(v) {
				return fm, Errorf(ClassInvalid,
					"field %q takes a date as YYYY-MM-DD; %q is not one", field, v)
			}
		}
		fm.SetDateValues = values
	case model.FieldUser:
		fm.SetUserValues = values
	case model.FieldSelection:
		if err := checkChoices(field, values, fd); err != nil {
			return fm, err
		}
		fm.SetSelectionValues = values
	default:
		return fm, Errorf(ClassUnsupported,
			"field %q is of a type this server does not know how to set", field)
	}
	return fm, nil
}

// checkChoices holds a selection field to the values its definition
// permits. A choice id is generated and unguessable, so a wrong one is
// answered with the list rather than with a refusal.
func checkChoices(field string, values []string, fd model.LabelFieldDefinition) error {
	max := fd.MaxEntries
	if max <= 0 {
		max = 1
	}
	if len(values) > max {
		return Errorf(ClassInvalid, "field %q takes %s; %d were given",
			field, model.Plural(max, "value", "values"), len(values))
	}
	for _, v := range values {
		if _, ok := fd.Choice(v); ok {
			continue
		}
		return Errorf(ClassInvalid, "field %q has no choice %q; its choices are %s",
			field, v, strings.Join(fd.ChoiceIDs(), ", "))
	}
	return nil
}

// isCalendarDate reports whether a value is the YYYY-MM-DD the API asks
// for. time.Parse with that layout rejects trailing text, so it is as
// strict as picking the string apart by hand — and stricter where it
// counts: it refuses 2026-13-45, which a digit-by-digit check accepts
// and forwards to Drive to fail one call later with a worse message.
func isCalendarDate(v string) bool {
	_, err := time.Parse("2006-01-02", v)
	return err == nil
}

// definitionFor finds the definition an action needs, paying for it only
// when the action cannot proceed without it.
//
// A failure is carried rather than raised. Applying and removing a label
// need no definition at all, and refusing those because the definitions
// API is out of reach would be refusing work that would have succeeded —
// only set_field is stuck, because the setter follows from the field's
// type.
func (s *Service) definitionFor(ctx context.Context, action, id string) (*model.LabelDefinition, error) {
	if action == "set_field" {
		return s.labelDefinition(ctx, id)
	}
	defs, ok := s.cachedLabelDefinitions()
	if !ok {
		return nil, nil
	}
	return pickDefinition(defs, id)
}

// labelDefinition reads one definition, fetching the listing if it is
// not in hand.
func (s *Service) labelDefinition(ctx context.Context, id string) (*model.LabelDefinition, error) {
	defs, err := s.allLabelDefinitions(ctx)
	if err != nil {
		return nil, err
	}
	return pickDefinition(defs, id)
}

// pickDefinition names the one failure a caller can act on: an id that
// no published label has.
func pickDefinition(defs map[string]*model.LabelDefinition, id string) (*model.LabelDefinition, error) {
	if def, ok := defs[id]; ok {
		return def, nil
	}
	return nil, Errorf(ClassNotFound,
		"no published label has the id %q; list_labels shows the ones this account can use", id)
}

// cachedLabelDefinitions returns the definitions already in hand, and
// whether there are any. It never reaches the network.
//
// It exists so that a call which does not NEED the definitions can still
// use them when they are free. Applying and removing a label need only
// its id, but a wrong id is worth catching with the list of real ones
// rather than with Drive's refusal — and that is worth a lookup, not a
// listing.
func (s *Service) cachedLabelDefinitions() (map[string]*model.LabelDefinition, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.labelDefs.value == nil || s.now().Sub(s.labelDefs.at) > s.opts.LabelTTL {
		return nil, false
	}
	return s.labelDefs.value, true
}

// labelDefsFailure returns the remembered failure, if the last attempt to
// read the definitions failed and the memory of it is still fresh.
//
// A failure and an empty listing are NOT the same answer, and caching
// them as the same value was a way to turn one diagnosis into a much
// worse one: with the Labels API not enabled, the first set_field said
// "the Drive Labels API refused the token, enable it and log in again"
// and every one for the next ten minutes said "no published label has
// that id" — sending a deployer to look for a label that exists.
func (s *Service) labelDefsFailure() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.labelDefsErr == nil || s.now().Sub(s.labelDefsErrAt) > s.opts.LabelTTL {
		return nil
	}
	return s.labelDefsErr
}

// allLabelDefinitions reads every page of definitions and keeps them.
//
// The listing is per account and changes when an administrator
// republishes a label, which does not happen between two calls of one
// conversation — so it has a TTL of its own rather than borrowing
// FileTTL, which is five seconds and exists to coalesce a burst of reads
// of the same file. Under FileTTL this cache expired between almost
// every pair of calls, and the comment defending it said the opposite of
// what the code did.
//
// The page size is the API's maximum for the same reason: this is the
// one caller that wants every definition, and asking 50 at a time makes
// a large domain four times as many round trips.
func (s *Service) allLabelDefinitions(ctx context.Context) (map[string]*model.LabelDefinition, error) {
	if defs, ok := s.cachedLabelDefinitions(); ok {
		return defs, nil
	}
	if err := s.labelDefsFailure(); err != nil {
		return nil, err
	}
	out := map[string]*model.LabelDefinition{}
	token := ""
	for {
		page, err := s.api.ListLabelDefinitions(ctx, gapi.ListLabelDefinitionsOptions{
			PageSize: gapi.MaxLabelPageSize, PageToken: token,
		})
		if err != nil {
			// The failure is remembered AS a failure, so it keeps saying
			// what went wrong instead of turning into an empty listing.
			// The case this is for is a deployer who turned labels on
			// without enabling the Labels API: without it every labelled
			// file card pays a fresh failing listing for as long as that
			// lasts.
			wrapped := s.labelAPIError(err, "reading the label definitions")
			s.mu.Lock()
			s.labelDefsErr, s.labelDefsErrAt = wrapped, s.now()
			s.mu.Unlock()
			return nil, wrapped
		}
		for _, d := range page.Labels {
			if def := model.NewLabelDefinition(d); def != nil {
				out[def.ID] = def
			}
		}
		if page.NextPageToken == "" || len(page.Labels) == 0 {
			break
		}
		token = page.NextPageToken
	}
	s.mu.Lock()
	s.labelDefs = cached[map[string]*model.LabelDefinition]{value: out, at: s.now()}
	s.labelDefsErr = nil
	s.mu.Unlock()
	return out, nil
}

// labelsOn refuses when the deployer has not turned labels on. The tools
// are unregistered in that case, so this is the belt to that braces: a
// service used directly, or a tool list that grows a caller.
func (s *Service) labelsOn() error { return labelsAPI.enabled(s.opts.Labels) }

// labelAPIError names the setup step behind the one failure a deployer
// can fix, which is the same failure everybody hits first.
func (s *Service) labelAPIError(err error, doing string) error {
	return labelsAPI.refused(err, doing)
}

// modifyLabelsError says which half of the setup a refusal points at.
func (s *Service) modifyLabelsError(err error, f *gdrive.File, labelID string, def *model.LabelDefinition) error {
	if gapi.Class(err) == ClassForbidden && def != nil && !def.CanApply {
		return &Error{Class: ClassForbidden, Message: fmt.Sprintf(
			"you may not apply the label %q to %s: the label's own permissions say so, whatever your "+
				"access to the file. Google said: %s", labelID, f.Name, gapi.Message(err)), Err: err}
	}
	return wrap(err, "changing the label "+labelID+" on "+f.Name)
}

// labelActionWords says what was done, for the head of the result.
func labelActionWords(action, labelID string, def *model.LabelDefinition) string {
	name := labelID
	if def != nil && def.Title != "" {
		name = def.Title
	}
	switch action {
	case "apply":
		return "applied " + name
	case "remove":
		return "removed " + name
	case "unset_field":
		return "cleared a field of " + name
	default:
		return "set a field of " + name
	}
}
