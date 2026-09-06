package model

import (
	"time"

	"github.com/mmedum/google-drive-mcp/internal/gdrive"
)

// Approval statuses, as Drive spells them.
const (
	ApprovalInProgress = "IN_PROGRESS"
	ApprovalApproved   = "APPROVED"
	ApprovalCancelled  = "CANCELLED"
	ApprovalDeclined   = "DECLINED"
)

// Reviewer responses, as Drive spells them.
const (
	ResponseNone     = "NO_RESPONSE"
	ResponseApproved = "APPROVED"
	ResponseDeclined = "DECLINED"
)

// Approval is a review on a file as this server reports it.
type Approval struct {
	ID string
	// Status is one of the Approval* constants.
	Status    string
	Initiator string
	Reviewers []Reviewer
	Created   time.Time
	Due       time.Time
	Completed time.Time
	// LocksOnApproval is true when a content change resets the answers
	// and an approved file is locked. It is the consequence of this
	// resource nobody expects, so it is carried rather than derived at
	// the point of printing.
	LocksOnApproval bool
}

// Reviewer is one person's answer, or the absence of one.
type Reviewer struct {
	Person string
	// Response is one of the Response* constants.
	Response string
	// Me marks the signed-in person, because the one question a listing
	// has to answer is whether it is waiting on you.
	Me bool
}

// NewApproval converts one wire approval.
func NewApproval(a *gdrive.Approval) *Approval {
	if a == nil || a.ApprovalID == "" {
		return nil
	}
	out := &Approval{
		ID:              a.ApprovalID,
		Status:          a.Status,
		Initiator:       userWords(a.Initiator),
		Created:         parseTime(a.CreateTime),
		Due:             parseTime(a.DueTime),
		Completed:       parseTime(a.CompleteTime),
		LocksOnApproval: a.FileContentChangeBehavior == "RESET_APPROVAL",
	}
	for _, r := range a.ReviewerResponses {
		if r == nil {
			continue
		}
		response := r.Response
		if response == "" || response == "RESPONSE_UNSPECIFIED" {
			response = ResponseNone
		}
		out.Reviewers = append(out.Reviewers, Reviewer{
			Person:   userWords(r.Reviewer),
			Response: response,
			Me:       r.Reviewer != nil && r.Reviewer.Me,
		})
	}
	return out
}

// Open reports whether the approval is still waiting on somebody.
func (a *Approval) Open() bool { return a != nil && a.Status == ApprovalInProgress }

// WaitingOnMe reports whether the signed-in person still owes an answer.
// It is the only thing in a listing that asks the reader to do
// something, so it is a question the model answers rather than a shape
// the renderer infers.
func (a *Approval) WaitingOnMe() bool {
	if !a.Open() {
		return false
	}
	for _, r := range a.Reviewers {
		if r.Me && r.Response == ResponseNone {
			return true
		}
	}
	return false
}

// Outstanding counts the reviewers who have not answered.
func (a *Approval) Outstanding() int {
	n := 0
	for _, r := range a.Reviewers {
		if r.Response == ResponseNone {
			n++
		}
	}
	return n
}
