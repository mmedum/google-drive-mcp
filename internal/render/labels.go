package render

import (
	"strconv"
	"strings"

	"github.com/mmedum/google-drive-mcp/internal/model"
)

// LabelsOptions tune a listing of label definitions.
type LabelsOptions struct {
	// NextPageToken means this page did not exhaust the definitions.
	NextPageToken string
	// Note carries anything the caller should know about the listing as
	// a whole.
	Note string
}

// Labels renders the label definitions this account may use. A listing
// exists to answer two questions before a manage_labels call is made:
// which label, and what may be set on it. So every field is named with
// the id that has to be typed AND the words a person reads, and a
// selection field's choices are listed rather than summarised — an
// unlisted choice id is unguessable, and a wrong one is a failed call.
func Labels(defs []*model.LabelDefinition, o LabelsOptions) string {
	var b buf
	shown := 0
	for _, d := range defs {
		if d != nil {
			shown++
		}
	}
	if shown == 0 {
		b.line("no labels: this account has no published label available to apply")
		b.line("A Workspace administrator publishes labels in the admin console; " +
			"a label that exists only as a draft cannot be put on a file.")
		writeFooter(&b, o.Note, o.NextPageToken, "labels")
		return b.String()
	}
	b.line(model.Plural(shown, "label", "labels") + " available")
	for _, d := range defs {
		if d == nil {
			continue
		}
		b.line("")
		writeLabelDefinition(&b, d)
	}
	writeFooter(&b, o.Note, o.NextPageToken, "labels")
	return b.String()
}

// writeLabelDefinition writes one definition and its fields.
func writeLabelDefinition(b *buf, d *model.LabelDefinition) {
	title := d.Title
	if title == "" {
		title = "(untitled)"
	}
	b.line(title + " — " + d.ID)
	if d.Description != "" {
		b.field("about", d.Description)
	}
	b.field("you can", labelCapabilityWords(d))
	if len(d.Fields) == 0 {
		b.field("fields", "none: this label is applied on its own, with no values to set")
		return
	}
	for _, f := range d.Fields {
		writeLabelField(b, f)
	}
}

// labelCapabilityWords says what this account may do with the label, in
// Drive's own answer rather than in a guess from a role. A label that can
// be read but not applied is a real state, and a listing that did not say
// so would invite a refused write.
func labelCapabilityWords(d *model.LabelDefinition) string {
	switch {
	case d.CanApply && d.CanRemove:
		return "apply this label and remove it"
	case d.CanApply:
		return "apply this label, but not remove it once it is on"
	case d.CanRemove:
		return "remove this label, but not apply it"
	default:
		return "read this label only: applying and removing are both refused"
	}
}

// writeLabelField writes one field: what to type, what it means, what it
// takes, and whether this account may set it.
func writeLabelField(b *buf, f model.LabelFieldDefinition) {
	name := f.DisplayName
	if name == "" {
		name = f.ID
	}
	parts := []string{fieldTypeWords(f)}
	if f.Required {
		parts = append(parts, "required")
	}
	if !f.CanWrite {
		parts = append(parts, "you cannot set this one")
	}
	b.field("field "+f.ID, name+" — "+strings.Join(parts, ", "))
	if len(f.Choices) > 0 {
		choices := make([]string, 0, len(f.Choices))
		for _, c := range f.Choices {
			if c.DisplayName != "" && c.DisplayName != c.ID {
				choices = append(choices, c.ID+" ("+c.DisplayName+")")
				continue
			}
			choices = append(choices, c.ID)
		}
		b.field("  choices", strings.Join(choices, ", "))
	}
}

// fieldTypeWords says what a field takes, in the words the tool's own
// arguments use.
func fieldTypeWords(f model.LabelFieldDefinition) string {
	switch f.ValueType {
	case model.FieldText:
		return "text"
	case model.FieldInteger:
		return "a whole number"
	case model.FieldDate:
		return "a date, YYYY-MM-DD"
	case model.FieldUser:
		return "an email address"
	case model.FieldSelection:
		if f.MaxEntries > 1 {
			return "up to " + strconv.Itoa(f.MaxEntries) + " of the choices below, by id"
		}
		return "one of the choices below, by id"
	}
	return "a type this server does not know; setting it may be refused"
}
