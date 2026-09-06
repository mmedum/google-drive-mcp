package render

import (
	"strings"
	"time"

	"github.com/mmedum/google-drive-mcp/internal/model"
)

// ActivityOptions tune a listing of activity.
type ActivityOptions struct {
	// Subject heads the listing: what was asked about.
	Subject string
	// Location is where that item sits.
	Location string
	Now      time.Time
	// NextPageToken means this page did not exhaust the activity.
	NextPageToken string
	// Note carries what the caller should know about the listing.
	Note string
}

// Activity renders what happened to a file or a folder, newest first.
//
// Every line says who as far as the API will say — "you" or "somebody
// else" — and the note at the end says why it will not say more. A
// reader who was not told would reasonably conclude the names were lost
// somewhere in this server.
func Activity(events []*model.Activity, o ActivityOptions) string {
	var b buf
	if o.Subject != "" {
		b.line(o.Subject)
	}
	b.field("location", o.Location)
	if len(events) == 0 {
		b.line("no activity recorded in the window asked about")
		writeActivityNotes(&b, o, false)
		return b.String()
	}
	named := false
	for _, e := range events {
		if e == nil {
			continue
		}
		if e.Who != "" {
			named = true
		}
		b.line(activityLine(e, o.Now))
	}
	writeActivityNotes(&b, o, named)
	return b.String()
}

// activityLine writes one event: when, who, what, and what it was about.
func activityLine(e *model.Activity, now time.Time) string {
	when := model.Ago(e.When, now)
	if !e.Over.IsZero() && !e.Over.Equal(e.When) {
		when += " (over " + model.Ago(e.Over, e.When) + ")"
	}
	parts := []string{when}
	sentence := e.Who + " " + e.What
	if e.Detail != "" {
		sentence += " " + e.Detail
	}
	parts = append(parts, sentence)
	if len(e.Items) > 0 {
		parts = append(parts, "— "+strings.Join(e.Items, ", "))
	}
	return strings.Join(parts, "  ")
}

// writeActivityNotes says why nobody is named, then closes the listing
// the way every other one closes.
func writeActivityNotes(b *buf, o ActivityOptions, named bool) {
	if named {
		// Said once at the end rather than on every line: the API's limit,
		// not this server's choice, and a reader who is not told will
		// assume the names went missing here.
		b.line("Drive's activity feed identifies people only as the signed-in account or not, " +
			"so this cannot say who anybody else was.")
	}
	writeFooter(b, o.Note, o.NextPageToken, "activity")
}
