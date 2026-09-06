package render

import (
	"strings"
	"time"

	"github.com/mmedum/google-drive-mcp/internal/model"
)

// ApprovalsOptions tune a listing of a file's approvals.
type ApprovalsOptions struct {
	// Subject heads the listing: the file the approvals are on.
	Subject string
	// Location is where that file sits.
	Location string
	Now      time.Time
	// NextPageToken means this page did not exhaust the approvals.
	NextPageToken string
	Note          string
}

// Approvals renders a file's approvals, newest first.
//
// The one thing a reader needs out of this list is whether it is waiting
// on them, so that is said in words at the top of each entry rather than
// left to be worked out from a column of responses.
func Approvals(approvals []*model.Approval, o ApprovalsOptions) string {
	var b buf
	if o.Subject != "" {
		b.line(o.Subject)
	}
	b.field("location", o.Location)
	if len(approvals) == 0 {
		b.line("no approvals: nobody has asked for a review of this file")
		writeApprovalNotes(&b, o)
		return b.String()
	}
	for i, a := range approvals {
		if a == nil {
			continue
		}
		if i > 0 {
			b.line("")
		}
		writeApproval(&b, a, o.Now)
	}
	writeApprovalNotes(&b, o)
	return b.String()
}

// writeApproval writes one approval and its reviewers.
func writeApproval(b *buf, a *model.Approval, now time.Time) {
	b.line(approvalHead(a) + " — " + a.ID)
	if a.Initiator != "" {
		b.field("asked by", a.Initiator)
	}
	if !a.Created.IsZero() {
		b.field("asked", model.Ago(a.Created, now))
	}
	if !a.Due.IsZero() {
		b.field("due", model.HumanTime(a.Due, now))
	}
	if !a.Completed.IsZero() {
		b.field("finished", model.Ago(a.Completed, now))
	}
	for _, r := range a.Reviewers {
		b.field("  "+responseWords(r.Response), r.Person)
	}
	if a.LocksOnApproval {
		// The consequence nobody expects: this is not just a record of
		// agreement, it stops the file being edited.
		b.line("Changing the content while this is open clears the approvals already given, " +
			"and once it is approved the file is LOCKED.")
	}
}

// approvalHead says what state the approval is in, leading with the one
// that asks the reader to act.
func approvalHead(a *model.Approval) string {
	switch {
	case a.WaitingOnMe():
		return "WAITING ON YOU"
	case a.Status == model.ApprovalInProgress:
		return "in progress, waiting on " + model.Plural(a.Outstanding(), "1 reviewer", "reviewers")
	case a.Status == model.ApprovalApproved:
		return "approved"
	case a.Status == model.ApprovalDeclined:
		return "declined"
	case a.Status == model.ApprovalCancelled:
		return "cancelled"
	}
	return strings.ToLower(a.Status)
}

// responseWords labels one reviewer's answer.
func responseWords(response string) string {
	switch response {
	case model.ResponseApproved:
		return "approved by"
	case model.ResponseDeclined:
		return "declined by"
	default:
		return "waiting on"
	}
}

func writeApprovalNotes(b *buf, o ApprovalsOptions) {
	if o.Note != "" {
		b.line(o.Note)
	}
	if o.NextPageToken != "" {
		b.field("next_page_token", o.NextPageToken)
	}
}
