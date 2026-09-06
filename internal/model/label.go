package model

import (
	"sort"
	"strings"

	"github.com/mmedum/google-drive-mcp/internal/gdrive"
)

// A label definition says what a label is; the values on a file say what
// somebody set. This file holds the first. The second reaches a file card
// as gdrive.Label, because an applied value is a value and there is
// nothing for a model type to add to it.

// Field value types, spelled as the Drive API spells them in an applied
// field's valueType. The definitions API says which type a field is by
// which options object it carries, so these are what that is translated
// into: one vocabulary for both halves.
const (
	FieldText      = "text"
	FieldInteger   = "integer"
	FieldDate      = "dateString"
	FieldSelection = "selection"
	FieldUser      = "user"
)

// LabelDefinition is a label as this server describes it: enough to
// decide whether to apply it and what may be set on it, and nothing
// about who drafted it or how it is drawn.
type LabelDefinition struct {
	ID          string
	Title       string
	Description string
	// Fields are in the definition's own order, which is the order a
	// person sees them in.
	Fields []LabelFieldDefinition
	// CanApply and CanRemove are Drive's answer for this account, not a
	// guess from a role.
	CanApply  bool
	CanRemove bool
}

// LabelFieldDefinition is one field of a definition.
type LabelFieldDefinition struct {
	ID          string
	DisplayName string
	// ValueType is one of the Field* constants, or empty when the
	// definition carries an options object this server does not know —
	// a field type added to the API after this code was written.
	ValueType string
	Required  bool
	// CanWrite is whether this account may set the field on a file.
	CanWrite bool
	// Choices are the permitted values of a selection field, in order.
	Choices []LabelChoice
	// MaxEntries is how many values a selection field takes; 0 means the
	// definition did not say, which the API treats as one.
	MaxEntries int
}

// LabelChoice is one permitted value of a selection field.
type LabelChoice struct {
	ID          string
	DisplayName string
}

// NewLabelDefinition converts one wire definition. It returns nil for a
// definition that is not published: an unpublished draft cannot be
// applied to a file, so offering it would be offering a failure.
func NewLabelDefinition(d *gdrive.LabelDefinition) *LabelDefinition {
	if d == nil || d.ID == "" {
		return nil
	}
	if d.Lifecycle != nil && d.Lifecycle.State != "" && d.Lifecycle.State != "PUBLISHED" {
		return nil
	}
	out := &LabelDefinition{ID: d.ID}
	if d.Properties != nil {
		out.Title = d.Properties.Title
		out.Description = d.Properties.Description
	}
	if d.AppliedCapabilities != nil {
		out.CanApply = d.AppliedCapabilities.CanApply
		out.CanRemove = d.AppliedCapabilities.CanRemove
	}
	for _, f := range d.Fields {
		if field, ok := newLabelField(f); ok {
			out.Fields = append(out.Fields, field)
		}
	}
	return out
}

// newLabelField converts one field definition, dropping a field that is
// not published for the same reason a whole definition is dropped.
func newLabelField(f *gdrive.LabelFieldDefinition) (LabelFieldDefinition, bool) {
	if f == nil || f.ID == "" {
		return LabelFieldDefinition{}, false
	}
	if f.Lifecycle != nil && f.Lifecycle.State != "" && f.Lifecycle.State != "PUBLISHED" {
		return LabelFieldDefinition{}, false
	}
	out := LabelFieldDefinition{ID: f.ID, ValueType: fieldValueType(f)}
	if f.Properties != nil {
		out.DisplayName = f.Properties.DisplayName
		out.Required = f.Properties.Required
	}
	if f.AppliedCapabilities != nil {
		out.CanWrite = f.AppliedCapabilities.CanWrite
	}
	if f.SelectionOptions != nil {
		if f.SelectionOptions.ListOptions != nil {
			out.MaxEntries = f.SelectionOptions.ListOptions.MaxEntries
		}
		for _, c := range f.SelectionOptions.Choices {
			if c == nil || c.ID == "" {
				continue
			}
			if c.Lifecycle != nil && c.Lifecycle.State != "" && c.Lifecycle.State != "PUBLISHED" {
				continue
			}
			choice := LabelChoice{ID: c.ID}
			if c.Properties != nil {
				choice.DisplayName = c.Properties.DisplayName
			}
			out.Choices = append(out.Choices, choice)
		}
	}
	return out, true
}

// fieldValueType reads a field's type off the options object present.
// The definitions API has no type member: which options object is set is
// the type, and a field carrying one this code does not know comes back
// empty rather than guessed at.
func fieldValueType(f *gdrive.LabelFieldDefinition) string {
	switch {
	case f.TextOptions != nil:
		return FieldText
	case f.IntegerOptions != nil:
		return FieldInteger
	case f.DateOptions != nil:
		return FieldDate
	case f.SelectionOptions != nil:
		return FieldSelection
	case f.UserOptions != nil:
		return FieldUser
	}
	return ""
}

// Field returns the definition of one field by id.
func (d *LabelDefinition) Field(id string) (LabelFieldDefinition, bool) {
	for _, f := range d.Fields {
		if f.ID == id {
			return f, true
		}
	}
	return LabelFieldDefinition{}, false
}

// Choice returns a selection choice by id.
func (f LabelFieldDefinition) Choice(id string) (LabelChoice, bool) {
	for _, c := range f.Choices {
		if c.ID == id {
			return c, true
		}
	}
	return LabelChoice{}, false
}

// ChoiceIDs lists a selection field's permitted values, for an error
// message that says what would have worked.
func (f LabelFieldDefinition) ChoiceIDs() []string {
	ids := make([]string, 0, len(f.Choices))
	for _, c := range f.Choices {
		ids = append(ids, c.ID)
	}
	return ids
}

// AppliedLabel is a label's values on one file, named from a definition
// where one is known. A file can carry a label whose definition this
// account cannot read, and that case is reported rather than hidden: the
// id alone is still true.
type AppliedLabel struct {
	ID    string
	Title string
	// Fields are the set values, in the definition's order where the
	// definition is known and by id where it is not.
	Fields []AppliedField
}

// AppliedField is one set field of an applied label.
type AppliedField struct {
	ID          string
	DisplayName string
	ValueType   string
	// Values are rendered forms: a selection choice becomes its display
	// name where the definition names one, a user becomes the address
	// Drive reported.
	Values []string
}

// NewAppliedLabels turns the labels on a file into the reading order a
// person expects, using the definitions where they are available.
func NewAppliedLabels(labels []*gdrive.Label, defs map[string]*LabelDefinition) []AppliedLabel {
	out := make([]AppliedLabel, 0, len(labels))
	for _, l := range labels {
		if l == nil || l.ID == "" {
			continue
		}
		def := defs[l.ID]
		applied := AppliedLabel{ID: l.ID}
		if def != nil {
			applied.Title = def.Title
		}
		applied.Fields = appliedFields(l.Fields, def)
		out = append(out, applied)
	}
	sort.SliceStable(out, func(i, j int) bool { return labelSortKey(out[i]) < labelSortKey(out[j]) })
	return out
}

// labelSortKey orders labels by what a reader sees, falling back to the
// id when there is no title to read.
func labelSortKey(l AppliedLabel) string {
	if l.Title != "" {
		return strings.ToLower(l.Title)
	}
	return l.ID
}

// appliedFields renders one label's set fields, in the definition's order
// where it is known.
func appliedFields(fields map[string]gdrive.LabelField, def *LabelDefinition) []AppliedField {
	ids := make([]string, 0, len(fields))
	for id := range fields {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if def != nil {
		// The definition's own order is the order a person sees the
		// fields in. A field the definition does not know keeps its
		// alphabetical place at the end, which a stable sort gives for
		// free: ranking it last leaves the sort with nothing to say about
		// it, so the order it arrived in stands.
		rank := make(map[string]int, len(def.Fields))
		for i, f := range def.Fields {
			rank[f.ID] = i
		}
		rankOf := func(id string) int {
			if r, ok := rank[id]; ok {
				return r
			}
			return len(def.Fields)
		}
		sort.SliceStable(ids, func(i, j int) bool { return rankOf(ids[i]) < rankOf(ids[j]) })
	}
	out := make([]AppliedField, 0, len(ids))
	for _, id := range ids {
		f := fields[id]
		af := AppliedField{ID: id, ValueType: f.ValueType}
		// The zero LabelFieldDefinition is the "no definition" case
		// already: its DisplayName and ValueType are empty and its Choice
		// ranges over a nil slice, so every assignment below is a no-op
		// without a definition rather than needing a flag to skip it.
		var fd LabelFieldDefinition
		if def != nil {
			fd, _ = def.Field(id)
		}
		af.DisplayName = fd.DisplayName
		if af.ValueType == "" {
			af.ValueType = fd.ValueType
		}
		af.Values = fieldValues(f, fd)
		if len(af.Values) == 0 {
			continue
		}
		out = append(out, af)
	}
	return out
}

// fieldValues renders one field's values. A selection value is a choice
// id, which is a generated string nobody can read, so the display name
// replaces it wherever the definition supplies one.
func fieldValues(f gdrive.LabelField, def LabelFieldDefinition) []string {
	var out []string
	out = append(out, f.Text...)
	out = append(out, f.Integer...)
	out = append(out, f.Date...)
	for _, id := range f.Selection {
		if c, ok := def.Choice(id); ok && c.DisplayName != "" {
			out = append(out, c.DisplayName)
			continue
		}
		out = append(out, id)
	}
	for _, u := range f.User {
		if u == nil {
			continue
		}
		out = append(out, userWords(u))
	}
	return out
}
