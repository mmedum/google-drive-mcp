package render

import (
	"github.com/mmedum/google-drive-mcp/internal/model"
)

// Change is one field a write altered, before and after. A write that
// reports only its result leaves the caller to remember what it replaced.
type Change struct {
	Field string `json:"field" jsonschema:"the field that changed"`
	From  string `json:"from" jsonschema:"its value before the change"`
	To    string `json:"to" jsonschema:"its value after the change"`
}

// WriteJSON is the structured half of a write tool's result. It carries
// the summary text as well: a client may show the model either form, and
// Claude Code shows only this one when both are present, so anything
// missing here is missing altogether.
type WriteJSON struct {
	Summary string `json:"summary" jsonschema:"the same report as the text result"`
	// Action is one of render.Actions(); a test holds the schema's list
	// and that set in step.
	Action string `json:"action" jsonschema:"what happened: created, uploaded, updated, moved, copied, trashed, restored, shared, unshared, deleted, emptied, commented, replied, resolved, reopened, denied, or unchanged when the call found nothing to do"`
	Note   string `json:"note,omitempty" jsonschema:"anything about the result that the fields do not carry"`
	// DryRun marks a result that describes what would happen and changed
	// nothing.
	DryRun  bool      `json:"dry_run,omitempty" jsonschema:"true when nothing was changed because this was a dry run"`
	Changes []Change  `json:"changes,omitempty" jsonschema:"the fields this call changed, before and after"`
	File    *FileJSON `json:"file,omitempty" jsonschema:"the file as it is now"`
	// SharingBefore and SharingAfter are the exposure a sharing call
	// found and the exposure it left. They are separate fields rather
	// than a Change entry because a client reading the structured result
	// has to be able to find them without parsing prose, and because the
	// one question worth asking after a share is who can reach the file
	// now.
	SharingBefore string `json:"sharing_before,omitempty" jsonschema:"who could see it before this call"`
	SharingAfter  string `json:"sharing_after,omitempty" jsonschema:"who can see it after this call"`
	// Drive is the shared drive a manage_drive call acted on.
	Drive *DriveJSON `json:"drive,omitempty" jsonschema:"the shared drive as it is now"`
}

// DriveJSON is a shared drive in structured form.
type DriveJSON struct {
	ID           string   `json:"id" jsonschema:"the shared drive id, which is what to pass to other tools"`
	Name         string   `json:"name"`
	Hidden       bool     `json:"hidden,omitempty" jsonschema:"true when it is out of the sidebar's default view"`
	Can          []string `json:"you_can,omitempty" jsonschema:"what the signed-in person may do in this drive"`
	Restrictions []string `json:"restrictions,omitempty" jsonschema:"the restrictions in force, in plain words"`
}

// FileJSON is the file card in structured form.
type FileJSON struct {
	ID       string `json:"id" jsonschema:"the file id, which is what to pass to other tools"`
	Name     string `json:"name"`
	Kind     string `json:"kind" jsonschema:"what kind of thing it is, in plain words"`
	MimeType string `json:"mime_type,omitempty"`
	Location string `json:"location,omitempty" jsonschema:"the drive and folders it sits in"`
	Link     string `json:"link,omitempty" jsonschema:"the Drive link a person can open"`
	Size     int64  `json:"size_bytes,omitempty"`
	MD5      string `json:"md5,omitempty"`
	// HeadRevision identifies the version the content is now at.
	HeadRevision string `json:"head_revision,omitempty"`
	Sharing      string `json:"sharing,omitempty" jsonschema:"who can see it"`
	Starred      bool   `json:"starred,omitempty"`
	Trashed      bool   `json:"trashed,omitempty"`
	// ShortcutTarget is the id a shortcut points at.
	ShortcutTarget string `json:"shortcut_target,omitempty"`
}

// NewDriveJSON builds the structured form of a shared drive from the
// same model the text was rendered from.
func NewDriveJSON(d *model.Drive) *DriveJSON {
	if d == nil {
		return nil
	}
	return &DriveJSON{ID: d.ID, Name: d.Name, Hidden: d.Hidden,
		Can: d.Can, Restrictions: d.Restrictions}
}

// NewWriteJSON builds the structured result from the same model the text
// was rendered from, so the two cannot disagree.
func NewWriteJSON(f *model.File, action Action, note, summary string, changes []Change) *WriteJSON {
	out := &WriteJSON{Summary: summary, Action: string(action), Note: note, Changes: changes}
	if f == nil {
		return out
	}
	out.File = &FileJSON{
		ID: f.ID, Name: f.Name, Kind: f.Kind, MimeType: f.MimeType,
		Location: f.Location.String(), Link: f.Link, MD5: f.MD5,
		HeadRevision: f.HeadRevisionID, Sharing: f.Sharing.Summary(),
		Starred: f.Starred, Trashed: f.Trashed, ShortcutTarget: f.ShortcutTargetID,
	}
	if f.HasSize {
		out.File.Size = f.Size
	}
	return out
}

// Action is what a write did, as a closed set. It is typed because the
// same word goes into the prose and into the JSON schema's list of what
// a client may see: a free-form string let "would move" and "dry run"
// reach the output while the schema promised seven other words.
type Action string

// Every action a write can report.
const (
	ActionCreated   Action = "created"
	ActionUploaded  Action = "uploaded"
	ActionUpdated   Action = "updated"
	ActionMoved     Action = "moved"
	ActionCopied    Action = "copied"
	ActionTrashed   Action = "trashed"
	ActionRestored  Action = "restored"
	ActionShared    Action = "shared"
	ActionUnshared  Action = "unshared"
	ActionDeleted   Action = "deleted"
	ActionEmptied   Action = "emptied"
	ActionCommented Action = "commented"
	ActionReplied   Action = "replied"
	ActionResolved  Action = "resolved"
	ActionReopened  Action = "reopened"
	// ActionDenied is an access request refused. It is its own word
	// rather than "unchanged" because something did happen: the person
	// waiting has been answered, and the request is gone.
	ActionDenied    Action = "denied"
	ActionUnchanged Action = "unchanged"
)

// Actions lists every action in the order the schema names them, so the
// description a model reads is generated from the set the code can
// actually produce.
func Actions() []string {
	all := []Action{ActionCreated, ActionUploaded, ActionUpdated, ActionMoved,
		ActionCopied, ActionTrashed, ActionRestored, ActionShared, ActionUnshared,
		ActionDeleted, ActionEmptied, ActionCommented, ActionReplied, ActionResolved,
		ActionReopened, ActionDenied, ActionUnchanged}
	out := make([]string, 0, len(all))
	for _, a := range all {
		out = append(out, string(a))
	}
	return out
}

// Checksum renders the verdict on a downloaded file. The comparison is
// made in internal/service; the sentence is chosen here, so that "there
// was nothing to compare against" can never be shortened into "verified"
// by a caller composing its own prose.
func Checksum(c model.Checksum) string {
	switch c.State {
	case model.ChecksumMatch:
		return "verified against Drive's md5"
	case model.ChecksumMismatch:
		return "MISMATCH: Drive's md5 is " + c.Expected + " and the bytes written hash to " + c.Actual +
			". Treat the file as damaged and download it again."
	case model.ChecksumNotComparable:
		return "not compared: " + c.Why
	default:
		return "Drive publishes no checksum for this file, so there is nothing to compare against"
	}
}
