package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/mmedum/google-drive-mcp/internal/gapi"
	"github.com/mmedum/google-drive-mcp/internal/gdrive"
	"github.com/mmedum/google-drive-mcp/internal/model"
	"github.com/mmedum/google-drive-mcp/internal/render"
)

// ListRevisionsInput selects a file's history.
type ListRevisionsInput struct {
	File string
}

// ListRevisions shows a file's versions, newest first. These are Drive
// revision ids, not the Docs API's revision tokens, and the content of
// one comes through download_file with revision.
func (s *Service) ListRevisions(ctx context.Context, in ListRevisionsInput) (string, error) {
	res, err := s.Resolve(ctx, in.File, ResolveOptions{FollowShortcut: true})
	if err != nil {
		return "", err
	}
	f := res.File
	if f.IsFolder() {
		return "", Errorf(ClassInvalid, "%s is a folder, and a folder has no content and so no versions.", f.Name)
	}
	if f.Capabilities != nil && !f.Capabilities.CanReadRevisions {
		return "", Errorf(ClassForbidden, "this account cannot read the version history of %s; that needs "+
			"more than view access.", f.Name)
	}
	revs, err := s.api.ListRevisions(ctx, f.ID)
	if err != nil {
		return "", wrap(err, "reading the version history of "+f.Name)
	}
	// Drive sends revisions oldest first, and the useful end is the
	// recent one: a long history read from the top is a history nobody
	// reaches the interesting part of.
	out := make([]*model.Revision, 0, len(revs))
	for i := len(revs) - 1; i >= 0; i-- {
		if rev := model.NewRevision(revs[i], f.HeadRevisionID); rev != nil {
			out = append(out, rev)
		}
	}
	return render.Revisions(out, render.RevisionsOptions{
		Subject:   fmt.Sprintf("%s — %s: %s", f.Name, model.Kind(f), model.Plural(len(out), "revision", "revisions")),
		Now:       s.now(),
		Workspace: f.IsWorkspaceDoc(),
	}), nil
}

// Revision actions manage_revision accepts.
const (
	RevisionKeep   = "keep"
	RevisionUnkeep = "unkeep"
)

// RevisionActions lists what manage_revision accepts.
func RevisionActions() []string { return []string{RevisionKeep, RevisionUnkeep} }

// ManageRevisionInput pins or unpins one revision.
type ManageRevisionInput struct {
	File     string
	Revision string
	Action   string
}

// ManageRevision decides whether Drive keeps a revision for good. Without
// a pin a blob revision is discarded thirty days after it stops being
// current, which is the fact that makes this tool worth having.
func (s *Service) ManageRevision(ctx context.Context, in ManageRevisionInput) (*Result, error) {
	if err := s.writable("manage_revision"); err != nil {
		return nil, err
	}
	action := strings.ToLower(strings.TrimSpace(in.Action))
	if action != RevisionKeep && action != RevisionUnkeep {
		return nil, Errorf(ClassInvalid, "action %q is not one of %s", in.Action, strings.Join(RevisionActions(), ", "))
	}
	revisionID := strings.TrimSpace(in.Revision)
	if revisionID == "" {
		return nil, Errorf(ClassInvalid, "revision is required: list_revisions shows the ids")
	}
	res, err := s.Resolve(ctx, in.File, ResolveOptions{FollowShortcut: true})
	if err != nil {
		return nil, err
	}
	f := res.File
	before, err := s.api.GetRevision(ctx, f.ID, revisionID)
	if err != nil {
		return nil, s.revisionError(err, f, revisionID)
	}
	keep := action == RevisionKeep
	if before.KeepForever == keep {
		return s.report(ctx, res, outcome{Action: render.ActionUnchanged, Note: fmt.Sprintf(
			"revision %s of %s is already %s.", revisionID, f.Name, keptWords(keep))}), nil
	}
	updated, err := s.api.UpdateRevision(ctx, f.ID, revisionID, keep)
	if err != nil {
		return nil, wrap(err, fmt.Sprintf("%s revision %s of %s", action+"ing", revisionID, f.Name))
	}
	note := fmt.Sprintf("revision %s of %s is %s.", revisionID, f.Name, keptWords(updated.KeepForever))
	if !updated.KeepForever {
		note += " Drive discards it 30 days after it stopped being current, which may already be past."
	}
	// report rather than write, so the card carries what resolving found
	// — the shortcut that was followed, the shared drive it lives in.
	// Nothing about the file itself changed, so there is no cache to
	// drop: a revision's pin is not part of any file this server caches.
	return s.report(ctx, res, outcome{Action: render.ActionUpdated, Note: note,
		Changes: []render.Change{{Field: "revision " + revisionID + " kept forever",
			From: yesNo(before.KeepForever), To: yesNo(updated.KeepForever)}}}), nil
}

// revisionError explains the refusals a revision read gets, since
// Drive's own 404 does not say which of the two things was missing.
func (s *Service) revisionError(err error, f *gdrive.File, revisionID string) error {
	if gapi.Class(err) == ClassNotFound {
		return &Error{Class: ClassNotFound, Message: fmt.Sprintf(
			"%s has no revision %s. list_revisions shows the ids it does have; a blob revision that was "+
				"never pinned is gone 30 days after it stopped being current.", f.Name, revisionID), Err: err}
	}
	return wrap(err, fmt.Sprintf("reading revision %s of %s", revisionID, f.Name))
}

func keptWords(keep bool) string {
	if keep {
		return "kept forever"
	}
	return "not kept forever"
}

// ListChangesInput asks for a start token or for the changes since one.
type ListChangesInput struct {
	// PageToken is a token from an earlier call. Without one the feed has
	// no beginning to offer, so the call hands back a start token and
	// says to come back with it.
	PageToken string
	// Drive limits the feed to one shared drive. A shared drive's feed
	// has tokens of its own, and one from another feed means nothing in
	// it.
	Drive string
	// MyDriveOnly drops changes to anything outside My Drive.
	MyDriveOnly bool
	// IncludeRemoved keeps the entries for items that left this account's
	// view. Default true: "it is gone" is the change most worth hearing.
	IncludeRemoved *bool
	Limit          int
}

// ListChanges is the changes feed: with no token it returns the point to
// start from, and with one it lists what happened since, plus the token
// for next time. The tokens are opaque and the model keeps them for the
// session; search_files with modified_after is the stateless
// alternative.
func (s *Service) ListChanges(ctx context.Context, in ListChangesInput) (string, error) {
	driveID := ""
	driveName := ""
	if name := strings.TrimSpace(in.Drive); name != "" {
		d, err := s.findDrive(ctx, name)
		if err != nil {
			return "", err
		}
		driveID, driveName = d.ID, d.Name
	}

	token := strings.TrimSpace(in.PageToken)
	if token == "" {
		start, err := s.api.StartPageToken(ctx, driveID)
		if err != nil {
			return "", wrap(err, "asking Drive where the changes feed starts")
		}
		return render.Changes(nil, render.ChangesOptions{
			Title:         changesTitle(driveName) + ": nothing yet, this is the starting point",
			Starting:      true,
			NewStartToken: start,
			Note: "the feed has no beginning of its own, only a point to start from. Call again with that " +
				"page_token to see everything that changes from now on.",
		}), nil
	}

	includeRemoved := true
	if in.IncludeRemoved != nil {
		includeRemoved = *in.IncludeRemoved
	}
	page, err := s.api.ListChanges(ctx, gapi.ListChangesOptions{
		PageToken: token, PageSize: in.Limit, DriveID: driveID,
		IncludeRemoved: includeRemoved, RestrictToMyDrive: in.MyDriveOnly,
	})
	if err != nil {
		return "", s.changesError(err, token)
	}

	budget := maxParentLookups
	changes := make([]*model.Change, 0, len(page.Changes))
	for _, c := range page.Changes {
		// A null entry in the array is a thing JSON can carry and Drive
		// has no reason to send. Skipping it here rather than rendering
		// it is the difference between one missing row and a nil
		// dereference that takes the whole stdio server down.
		if c == nil {
			continue
		}
		location := ""
		if c.File != nil && !c.Removed {
			location = s.locationWithBudget(ctx, c.File, &budget).String()
		}
		if change := model.NewChange(c, location); change != nil {
			changes = append(changes, change)
		}
	}
	return render.Changes(changes, render.ChangesOptions{
		Title: fmt.Sprintf("%s: %s", changesTitle(driveName),
			model.Plural(len(changes), "change", "changes")),
		Now:           s.now(),
		NextPageToken: page.NextPageToken,
		NewStartToken: page.NewStartPageToken,
	}), nil
}

func changesTitle(driveName string) string {
	if driveName != "" {
		return "changes in the shared drive " + driveName
	}
	return "changes in this account's Drive"
}

// changesError names the one failure a caller can actually fix: a token
// from the wrong feed, or one so old Drive has forgotten it.
func (s *Service) changesError(err error, token string) error {
	if gapi.Class(err) == ClassInvalid || gapi.Class(err) == ClassNotFound {
		return &Error{Class: ClassInvalid, Message: fmt.Sprintf(
			"Drive will not read the page token %q. A token belongs to one feed — this account's, or one "+
				"shared drive's — and an old one expires. Call list_changes with no page_token to get a "+
				"fresh starting point. Google said: %s", token, gapi.Message(err)), Err: err}
	}
	return wrap(err, "reading the changes feed")
}
