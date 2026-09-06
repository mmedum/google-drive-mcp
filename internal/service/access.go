package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mmedum/google-drive-mcp/internal/config"
	"github.com/mmedum/google-drive-mcp/internal/gapi"
	"github.com/mmedum/google-drive-mcp/internal/gdrive"
	"github.com/mmedum/google-drive-mcp/internal/model"
	"github.com/mmedum/google-drive-mcp/internal/render"
)

// Principal types, as Drive names them.
const (
	principalUser   = "user"
	principalGroup  = "group"
	principalDomain = "domain"
	principalAnyone = "anyone"
)

// roles are the words share_file accepts, mapped to what Drive calls
// them. Only the spelling differs: this server writes snake_case
// everywhere else in its arguments, and file_organizer is the one role
// whose Drive name is not one word.
var roles = map[string]string{
	"reader":         model.RoleReader,
	"commenter":      model.RoleCommenter,
	"writer":         model.RoleWriter,
	"file_organizer": model.RoleFileOrganizer,
	"organizer":      model.RoleOrganizer,
	"owner":          model.RoleOwner,
}

// Roles lists the roles share_file accepts, for tool descriptions and
// error messages, so the words a model is offered are the words the code
// will accept.
func Roles() []string { return sortedKeys(roles) }

// maxExpiry is Drive's own ceiling on a permission's expiry: the
// reference says at most a year in the future.
const maxExpiry = 365 * 24 * time.Hour

// ListPermissionsInput selects what to describe.
type ListPermissionsInput struct {
	File string
}

// ListPermissions shows who has access to a file or a shared drive, how,
// and where each grant comes from. It is a read, so it stays registered
// when GDRIVE_SHARING is off: seeing exposure is not widening it.
func (s *Service) ListPermissions(ctx context.Context, in ListPermissionsInput) (string, error) {
	res, err := s.Resolve(ctx, in.File, ResolveOptions{FollowShortcut: false, Fresh: true})
	if err != nil {
		return "", err
	}
	f := res.File
	perms, err := s.api.ListPermissions(ctx, f.ID)
	if err != nil {
		return "", wrap(err, "reading who can see "+f.Name)
	}
	sharing := model.NewSharing(f.Shared, perms, true)
	isDrive := f.DriveID != "" && f.ID == f.DriveID
	if f.DriveID != "" {
		sharing.SharedDrive = orElse(res.DriveName, s.Location(ctx, f).Drive)
	}
	o := render.PermissionsOptions{
		Sharing: sharing, SharedDrive: isDrive,
		CanShare: model.CanShare(f),
	}
	if isDrive {
		o.Subject = f.Name + " — shared drive"
	} else {
		o.Subject = f.Name + " — " + model.Kind(f)
		o.Location = s.Location(ctx, f).String()
	}
	if s.opts.Sharing == config.SharingOff {
		o.Note = "this server was started with GDRIVE_SHARING=off, so nothing here can change any of it."
	}
	return render.Permissions(sharing.Grants, o), nil
}

// ShareFileInput is one grant to make or change.
type ShareFileInput struct {
	File string
	// Principal is someone@example.com, domain:example.com, or anyone.
	Principal string
	Role      string
	// Notify sends Google's own notification mail. Drive's default is
	// true; this server's is false, because a tool call is not a reason
	// to put mail in somebody's inbox. An ownership transfer forces it
	// on, which the result says.
	Notify  bool
	Message string
	// Expires is RFC 3339 or a duration like 30d, or the word "never" to
	// remove an expiry a grant already has. Users and groups only, at
	// most a year, as Drive requires. Clearing goes through
	// permissions.update's own removeExpiration parameter, so it is only
	// possible on a grant that exists.
	Expires string
	// Discoverable is allowFileDiscovery: whether a domain or anyone
	// grant makes the file turn up in search rather than only opening by
	// link. A pointer because "not passed" and "false" are different
	// requests on an existing grant: a plain bool silently narrowed a
	// file that was already findable by search every time the role was
	// changed, which is a change the caller never asked for and could
	// not opt out of.
	Discoverable *bool
	// AllowAnyone is the acknowledgement an anyone-with-the-link grant
	// needs. Without it the grant is refused however good the role.
	AllowAnyone bool
	// TransferOwnership is the acknowledgement the owner role needs.
	TransferOwnership bool
	DryRun            bool
}

// ShareFile grants or changes one principal's access, and reports who
// could see the file before and who can see it after. Google's
// organisation policy decides what may actually be shared; what this
// adds is the brakes on doing something the person is allowed to do but
// did not ask for.
func (s *Service) ShareFile(ctx context.Context, in ShareFileInput) (*Result, error) {
	if err := s.sharable("share_file"); err != nil {
		return nil, err
	}
	principal, err := parsePrincipal(in.Principal)
	if err != nil {
		return nil, err
	}
	role, err := parseRole(in.Role)
	if err != nil {
		return nil, err
	}
	if err := principal.allows(role, in.AllowAnyone); err != nil {
		return nil, err
	}
	if role == model.RoleOwner && !in.TransferOwnership {
		return nil, Errorf(ClassForbidden, "handing ownership to %s makes them the owner and demotes this "+
			"account to a writer, and it cannot be undone from here: only the new owner can hand it back. "+
			"Pass transfer_ownership: true on the same call if that is really what is wanted.", principal.label())
	}
	expires, clearExpiry, err := s.parseExpiry(in.Expires, principal, role)
	if err != nil {
		return nil, err
	}

	// A shortcut is the thing sitting in the folder, so sharing acts on
	// it rather than on what it points at.
	res, err := s.Resolve(ctx, in.File, ResolveOptions{FollowShortcut: false, Fresh: true})
	if err != nil {
		return nil, err
	}
	f := res.File
	if err := s.shareableTarget(f, role); err != nil {
		return nil, err
	}

	before, existing, err := s.exposure(ctx, res, principal)
	if err != nil {
		return nil, err
	}
	plan := sharePlan{
		principal: principal, role: role, expires: expires, clearExpiry: clearExpiry,
		existing: existing, notify: in.Notify, message: in.Message,
		discoverable: in.Discoverable, file: f,
	}
	if clearExpiry && existing == nil {
		return nil, Errorf(ClassInvalid, "there is no grant to %s yet, so it has no expiry to remove. "+
			"Leave expires out to grant access that does not expire.", principal.label())
	}
	if existing != nil && existing.Role == role && !plan.changesAnything() {
		note := fmt.Sprintf("%s already %s. Nothing was changed.", principal.label(), model.RoleWords(role))
		if existing.Expires != "" {
			// An unchanged grant that is going to lapse is not the same
			// as one that is not, and this is the call where somebody
			// would have found out.
			note += " That access expires on " + existing.Expires +
				"; pass expires to move it, or expires: never to remove the expiry."
		}
		return s.report(ctx, res, outcome{Action: render.ActionUnchanged, Note: note}), nil
	}
	if in.DryRun {
		return s.shareResult(ctx, res, before, before, plan, true), nil
	}

	if err := s.applyShare(ctx, f, plan); err != nil {
		return nil, s.shareError(err, f, principal)
	}
	// A grant changes who can reach the file, and the paths that led to
	// it are unaffected; only the cached copy of the file itself is.
	s.forget(f, false)
	res, after := s.rereadAfterSharing(ctx, res)
	return s.shareResult(ctx, res, before, after, plan, false), nil
}

// sharePlan is one grant as it will be applied, gathered so that the
// call, the prose and the structured result are all built from the same
// description rather than from three readings of the input.
type sharePlan struct {
	principal principal
	role      string
	expires   string
	// existing is the grant this principal already had, or nil.
	existing *model.Grant
	// clearExpiry removes an expiry the grant already has, which is a
	// query parameter on update rather than a value in the body.
	clearExpiry  bool
	notify       bool
	message      string
	discoverable *bool
	file         *gdrive.File
}

// changesAnything reports whether a repeat of an identical grant would
// alter something. The role is compared by the caller; this covers the
// fields that can differ while the role stays put.
func (p sharePlan) changesAnything() bool {
	if p.existing == nil {
		return true
	}
	if p.expires != "" && p.expires != p.existing.Expires {
		return true
	}
	if p.clearExpiry && p.existing.Expires != "" {
		return true
	}
	switch p.principal.kind {
	case principalDomain, principalAnyone:
		return p.discoverable != nil && *p.discoverable != p.existing.Discoverable
	}
	return false
}

// applyShare makes the call: an update when the principal already has a
// grant, and a create otherwise. Drive allows one permission per
// principal, so creating over an existing one is a 400 rather than a
// second grant.
func (s *Service) applyShare(ctx context.Context, f *gdrive.File, p sharePlan) error {
	meta := &gdrive.PermissionMeta{Role: p.role, ExpirationTime: p.expires}
	switch p.principal.kind {
	case principalDomain, principalAnyone:
		// Only when the caller said so. Sending it unasked narrowed a
		// file that was already findable by search every time somebody
		// changed a role.
		if p.discoverable != nil {
			meta.AllowFileDiscovery = gdrive.Bool(*p.discoverable)
		} else if p.existing == nil {
			// A new link grant is by-link-only unless asked otherwise,
			// which is Drive's own default and the quieter one.
			meta.AllowFileDiscovery = gdrive.Bool(false)
		}
	}
	if p.existing != nil {
		_, err := s.api.UpdatePermission(ctx, f.ID, p.existing.PermissionID, meta, gapi.UpdateShareOptions{
			TransferOwnership: p.role == model.RoleOwner,
			RemoveExpiration:  p.clearExpiry,
			ResourceIDs:       []string{f.ID},
		})
		return err
	}
	meta.Type = p.principal.kind
	meta.EmailAddress = p.principal.email
	meta.Domain = p.principal.domain
	o := gapi.ShareOptions{
		TransferOwnership: p.role == model.RoleOwner,
		EmailMessage:      p.message,
		ResourceIDs:       []string{f.ID},
	}
	// The reference allows sendNotificationEmail only for a user or a
	// group, and refuses it to be disabled for an ownership transfer.
	// Sending it anywhere else is a 400, so it is left off the request
	// entirely rather than sent as false.
	if p.principal.notifiable() {
		o.SendNotificationEmail = gdrive.Bool(p.notify || p.role == model.RoleOwner)
	}
	// Drive puts a transferred file in the new owner's root only outside
	// a shared drive, where ownership is the drive's and cannot move.
	o.MoveToNewOwnersRoot = p.role == model.RoleOwner && f.DriveID == ""
	_, err := s.api.CreatePermission(ctx, f.ID, meta, o)
	return err
}

// shareResult renders what the grant did, with the exposure before and
// after: the grant is the small half of the answer and who can now reach
// the file is the half worth reading.
func (s *Service) shareResult(ctx context.Context, res *Resolved, before, after model.Sharing, p sharePlan, dryRun bool) *Result {
	changes := make([]render.Change, 0, 2)
	to := model.RoleWords(p.role) + expiryWords(p.expires)
	if p.clearExpiry {
		to = model.RoleWords(p.role) + ", with no expiry"
	}
	changes = append(changes, render.Change{Field: "access for " + p.principal.label(),
		From: grantWords(p.existing), To: to})
	changes = append(changes, render.Exposure(before, after)...)

	out := outcome{Action: render.ActionShared, Changes: changes, DryRun: dryRun,
		Note: s.shareNote(p, after)}
	result := s.report(ctx, res, out)
	result.JSON.SharingBefore = before.Summary()
	result.JSON.SharingAfter = after.Summary()
	return result
}

// shareNote says the things a role and an exposure line do not: what a
// transfer actually did, what a link grant means, and whether mail went
// out.
func (s *Service) shareNote(p sharePlan, after model.Sharing) string {
	var parts []string
	if p.role == model.RoleOwner {
		if p.file.DriveID != "" {
			parts = append(parts, "in a shared drive the drive owns its files, so this makes "+
				p.principal.label()+" an organizer rather than an owner")
		} else {
			// What is certain is the demotion. Whether a consumer account
			// produces a pending transfer is read off the answer rather
			// than asserted: this server sends role owner with
			// transferOwnership and never sets pendingOwner itself, and
			// what Drive does with that on a consumer account has not
			// been seen here (spike F, §16). Describing an outcome
			// nobody has observed is how a result becomes wrong.
			parts = append(parts, "this account is now a writer on it rather than its owner, and cannot "+
				"take that back: only the new owner can hand it on")
			if after.PendingOwner != "" {
				parts = append(parts, "the transfer is waiting for "+after.PendingOwner+
					" to accept it, and list_permissions says pending until they do")
			}
		}
		parts = append(parts, "Google always mails an ownership transfer; that cannot be turned off")
	} else if p.principal.notifiable() {
		if p.notify {
			parts = append(parts, "Google sent "+p.principal.label()+" a notification email")
		} else {
			parts = append(parts, "no email was sent; pass notify: true if they should hear about it")
		}
	}
	if p.principal.kind == principalAnyone {
		// What the grant is now, not what this call asked for: leaving
		// discoverable out keeps whatever the grant already had, and the
		// exposure a person needs to read is the resulting one.
		findable := p.discoverable != nil && *p.discoverable
		if p.discoverable == nil && p.existing != nil {
			findable = p.existing.Discoverable
		}
		what := "anyone with the link can now reach it without signing in"
		if findable {
			what = "anyone on the internet can now reach it AND find it by search"
		}
		parts = append(parts, what)
	}
	return joinSentences(parts)
}

// UnshareFileInput revokes one grant.
type UnshareFileInput struct {
	File string
	// Principal names the grant to remove; PermissionID names it
	// directly, which is what an inherited or nameless grant needs.
	Principal    string
	PermissionID string
	// RemoveLink revokes the anyone-with-the-link or domain-wide grant,
	// which is the one people mean by "make it private again".
	RemoveLink bool
	DryRun     bool
}

// UnshareFile removes one grant and says what access is left. An
// inherited shared-drive permission is refused with its source named:
// removing it here would not work, and Drive's own error does not say
// where to go instead.
func (s *Service) UnshareFile(ctx context.Context, in UnshareFileInput) (*Result, error) {
	if err := s.sharable("unshare_file"); err != nil {
		return nil, err
	}
	given := 0
	for _, v := range []bool{strings.TrimSpace(in.Principal) != "", strings.TrimSpace(in.PermissionID) != "", in.RemoveLink} {
		if v {
			given++
		}
	}
	if given != 1 {
		return nil, Errorf(ClassInvalid, "pass exactly one of principal (an address, domain:example.com "+
			"or anyone), permission_id (from list_permissions), or remove_link: true")
	}
	res, err := s.Resolve(ctx, in.File, ResolveOptions{FollowShortcut: false, Fresh: true})
	if err != nil {
		return nil, err
	}
	f := res.File
	if !model.CanShare(f) {
		return nil, Errorf(ClassForbidden, "you cannot change sharing on %s. list_permissions shows who "+
			"can see it; only someone who may share it can change that.", f.Name)
	}

	before := s.sharingNow(ctx, f)
	if before.Unknown {
		// Saying "they have no grant" here would be a guess dressed as a
		// fact: the list could not be read, so what is on the file is
		// unknown and removing the right thing is impossible.
		return nil, Errorf(ClassForbidden, "this account cannot read who has access to %s, so there is no "+
			"way to tell which grant to remove. get_file shows what you may do with it.", f.Name)
	}
	target, err := s.grantToRemove(before, in)
	if err != nil {
		return nil, err
	}
	if inherited := target.InheritedFrom; inherited != "" {
		return nil, Errorf(ClassForbidden, "%s has access to %s through %s, not through a grant on the file "+
			"itself, so it cannot be removed here. Change it where it was given: unshare_file on that "+
			"shared drive, or on the folder named.", target.Label(), f.Name, inherited)
	}
	if target.Role == model.RoleOwner {
		return nil, Errorf(ClassInvalid, "%s owns %s, and an owner's access is not revoked but transferred: "+
			"share_file with role: owner and transfer_ownership: true hands it to somebody else.",
			target.Label(), f.Name)
	}

	if in.DryRun {
		return s.unshareResult(ctx, res, before, before, *target, true), nil
	}
	if err := s.api.DeletePermission(ctx, f.ID, target.PermissionID); err != nil {
		return nil, wrap(err, fmt.Sprintf("removing %s's access to %s", target.Label(), f.Name))
	}
	s.forget(f, false)
	res, after := s.rereadAfterSharing(ctx, res)
	return s.unshareResult(ctx, res, before, after, *target, false), nil
}

func (s *Service) unshareResult(ctx context.Context, res *Resolved, before, after model.Sharing, g model.Grant, dryRun bool) *Result {
	changes := make([]render.Change, 0, 2)
	changes = append(changes, render.Change{Field: "access for " + g.Label(),
		From: model.RoleWords(g.Role), To: "no access"})
	changes = append(changes, render.Exposure(before, after)...)
	note := ""
	if g.Type == principalAnyone || g.Type == principalDomain {
		note = "the link that grant made is dead; anyone holding it now sees a request-access page"
	}
	result := s.report(ctx, res, outcome{Action: render.ActionUnshared,
		Changes: changes, DryRun: dryRun, Note: note})
	result.JSON.SharingBefore = before.Summary()
	result.JSON.SharingAfter = after.Summary()
	return result
}

// grantToRemove picks the grant an unshare names, and refuses rather
// than guessing when nothing matches.
func (s *Service) grantToRemove(sharing model.Sharing, in UnshareFileInput) (*model.Grant, error) {
	if id := strings.TrimSpace(in.PermissionID); id != "" {
		for i := range sharing.Grants {
			if sharing.Grants[i].PermissionID == id {
				return &sharing.Grants[i], nil
			}
		}
		return nil, Errorf(ClassNotFound, "no grant with permission id %q. list_permissions shows the ids "+
			"that are there.", id)
	}
	if in.RemoveLink {
		var links []model.Grant
		if sharing.Link != nil {
			links = append(links, *sharing.Link)
		}
		links = append(links, sharing.Domains...)
		switch len(links) {
		case 1:
			return &links[0], nil
		case 0:
			return nil, Errorf(ClassNotFound, "there is no link grant to remove: nobody reaches this by "+
				"link or through a whole domain. list_permissions shows who does reach it.")
		}
		var b strings.Builder
		b.WriteString("more than one link-style grant is on this file; name one with permission_id:")
		for _, g := range links {
			fmt.Fprintf(&b, "\n  %s  %s (%s)", g.PermissionID, g.Label(), model.RoleWords(g.Role))
		}
		return nil, &Error{Class: ClassAmbiguous, Message: b.String()}
	}

	p, err := parsePrincipal(in.Principal)
	if err != nil {
		return nil, err
	}
	for i := range sharing.Grants {
		if p.matches(sharing.Grants[i]) {
			return &sharing.Grants[i], nil
		}
	}
	return nil, Errorf(ClassNotFound, "%s has no grant on this file. list_permissions shows who does; "+
		"access through a group or a whole domain is removed at that grant, not per person.", p.label())
}

// exposure reads the current permission list, returning both the
// summary a result reports and the grant this principal already has.
// One listing answers both questions, and asking twice would let them
// disagree.
func (s *Service) exposure(ctx context.Context, res *Resolved, p principal) (model.Sharing, *model.Grant, error) {
	perms, err := s.api.ListPermissions(ctx, res.File.ID)
	if err != nil {
		return model.Sharing{}, nil, wrap(err, "reading who can see "+res.File.Name)
	}
	sharing := s.sharingFrom(ctx, res.File, perms, res.DriveName)
	for i := range sharing.Grants {
		if p.matches(sharing.Grants[i]) {
			return sharing, &sharing.Grants[i], nil
		}
	}
	return sharing, nil, nil
}

// rereadAfterSharing reads the file again after a grant changed, and
// returns both the resolved file the card is rendered from and the
// exposure that file now has.
//
// Rendering from the file read BEFORE the write left the card's
// "sharing:" line showing the state the call had just changed — so
// share_file on a private file reported "sharing: private to you" in the
// same result whose change line said the file was now reachable by
// anyone with the link. That line is the one a person checks to see what
// they just exposed, and it was stale exactly when it mattered most.
//
// It also fixes the "after" summary. That was built from the permission
// list read afresh but the `shared` flag of the stale file, so removing
// the last grant reported "shared, but no grants are visible to this
// account" instead of "private to you".
//
// One read replaces the separate permission re-read it used to do, since
// a file read carries its own grants outside a shared drive.
func (s *Service) rereadAfterSharing(ctx context.Context, res *Resolved) (*Resolved, model.Sharing) {
	fresh, err := s.Resolve(ctx, res.File.ID, ResolveOptions{FollowShortcut: false, Fresh: true})
	if err != nil {
		// The write happened; failing to describe it must not fail the
		// call. Fall back to the permission list alone, which is still
		// better than the state from before the write.
		s.log.DebugContext(ctx, "could not re-read after a sharing change", "class", gapi.Class(err))
		return res, s.sharingNow(ctx, res.File)
	}
	return fresh, s.Model(ctx, fresh).Sharing
}

// sharingNow re-reads the permission list. A failure here is reported as
// unknown rather than as an empty list: a result that said "private to
// you" because a read failed would understate exposure, which is the one
// direction that must never happen.
//
// It lists rather than reading the grants a files.get already carried,
// and that is deliberate. Outside a shared drive the file read does
// carry them, so the list is one round trip that could be saved — but an
// absent `permissions` field and a file with no grants are the same
// empty slice, and the saving is only available on the sharing path,
// which is the one place where mistaking the second for the first
// understates exposure. TestUnshareSaysSoWhenItCannotReadWhoHasAccess
// is that distinction with a test around it. A review proposed the
// saving in phase 3 and this is why it was not taken.
func (s *Service) sharingNow(ctx context.Context, f *gdrive.File) model.Sharing {
	perms, err := s.api.ListPermissions(ctx, f.ID)
	if err != nil {
		s.log.DebugContext(ctx, "permissions unavailable", "file", gapi.ShortID(f.ID), "class", gapi.Class(err))
		return model.NewSharing(f.Shared, nil, false)
	}
	return s.sharingFrom(ctx, f, perms, "")
}

func (s *Service) sharingFrom(ctx context.Context, f *gdrive.File, perms []*gdrive.Permission, driveName string) model.Sharing {
	sharing := model.NewSharing(f.Shared, perms, true)
	if f.DriveID != "" {
		sharing.SharedDrive = orElse(driveName, s.Location(ctx, f).Drive)
	}
	return sharing
}

// shareableTarget refuses before the call what Drive would refuse after
// it, in terms that say what to do instead. The sharing guide asks apps
// to consult capabilities.canShare rather than infer rights from a role,
// and this is where that happens.
func (s *Service) shareableTarget(f *gdrive.File, role string) error {
	if !model.CanShare(f) {
		return Errorf(ClassForbidden, "you cannot change sharing on %s. Its owner may have turned off "+
			"re-sharing by editors, or your role does not allow it; get_file shows what you can do with it.", f.Name)
	}
	switch role {
	case model.RoleOrganizer, model.RoleFileOrganizer:
		if f.DriveID == "" {
			return Errorf(ClassInvalid, "organizer and file_organizer are shared-drive roles, and %s is in "+
				"My Drive. Use writer, commenter or reader here.", f.Name)
		}
	case model.RoleOwner:
		if f.DriveID != "" {
			return Errorf(ClassUnsupported, "a shared drive owns everything in it, so %s has no owner to "+
				"transfer. Give organizer instead, which is as much control as a shared drive has.", f.Name)
		}
		if !f.OwnedByMe {
			return Errorf(ClassForbidden, "only the owner can hand ownership on, and this account does not "+
				"own %s.", f.Name)
		}
	}
	return nil
}

// shareError explains Drive's refusals of a share in the terms that
// caused them. The policy family is mapped in internal/gapi; what is
// added here is which call it was and what the caller can do next.
func (s *Service) shareError(err error, f *gdrive.File, p principal) error {
	if gapi.Class(err) == ClassBlocked {
		return &Error{Class: ClassBlocked, Message: fmt.Sprintf(
			"your organisation's sharing policy does not allow giving %s access to %s. Google said: %s. "+
				"This is set by a Workspace administrator and no option here can work around it.",
			p.label(), f.Name, gapi.Message(err)), Err: err}
	}
	return wrap(err, fmt.Sprintf("sharing %s with %s", f.Name, p.label()))
}

// sharable refuses every sharing write when the deployer turned sharing
// off. The tools are not registered in that mode, so this is the second
// line: it holds if a later tool forgets to ask.
func (s *Service) sharable(tool string) error {
	if err := s.writable(tool); err != nil {
		return err
	}
	if s.opts.Sharing == config.SharingOff {
		return Errorf(ClassForbidden, "this server was started with GDRIVE_SHARING=off, so %s is not "+
			"available and nothing here can change who can see a file. list_permissions still shows who can.", tool)
	}
	return nil
}

// principal is one grantee, parsed from the single string a caller
// passes. Drive splits it across three fields and a type; a model should
// not have to.
type principal struct {
	kind   string
	email  string
	domain string
}

// parsePrincipal reads someone@example.com, group:team@example.com,
// domain:example.com or anyone.
//
// A bare address is a user, which is what it is nine times in ten; a
// group has to say so, because Drive does not accept "user" for a group
// address and the failure otherwise reads as though the address were
// wrong.
func parsePrincipal(v string) (principal, error) {
	raw := strings.TrimSpace(v)
	if raw == "" {
		return principal{}, Errorf(ClassInvalid, "principal is required: an address like someone@example.com, "+
			"group:team@example.com, domain:example.com, or anyone")
	}
	lower := strings.ToLower(raw)
	switch {
	case lower == principalAnyone || lower == "anyone with the link":
		return principal{kind: principalAnyone}, nil
	case strings.HasPrefix(lower, "domain:"):
		d := strings.TrimSpace(raw[len("domain:"):])
		if d == "" || strings.Contains(d, "@") {
			return principal{}, Errorf(ClassInvalid, "domain: takes a domain name like example.com, not %q", d)
		}
		return principal{kind: principalDomain, domain: strings.ToLower(d)}, nil
	case strings.HasPrefix(lower, "group:"):
		address := strings.TrimSpace(raw[len("group:"):])
		if !strings.Contains(address, "@") {
			return principal{}, Errorf(ClassInvalid, "group: takes an address like team@example.com, not %q", address)
		}
		return principal{kind: principalGroup, email: address}, nil
	case strings.HasPrefix(lower, "user:"):
		address := strings.TrimSpace(raw[len("user:"):])
		if !strings.Contains(address, "@") {
			return principal{}, Errorf(ClassInvalid, "user: takes an address like someone@example.com, not %q", address)
		}
		return principal{kind: principalUser, email: address}, nil
	case strings.Contains(raw, "@"):
		return principal{kind: principalUser, email: raw}, nil
	}
	return principal{}, Errorf(ClassInvalid, "%q is not a principal. Pass an address like "+
		"someone@example.com, group:team@example.com for a Google group, domain:example.com for everyone "+
		"in an organisation, or anyone for a link anybody can open.", raw)
}

// label renders the principal the way a result names it.
func (p principal) label() string {
	switch p.kind {
	case principalAnyone:
		return "anyone with the link"
	case principalDomain:
		return "everyone at " + p.domain
	case principalGroup:
		return "the group " + p.email
	default:
		return p.email
	}
}

// notifiable reports whether Drive accepts sendNotificationEmail for
// this principal. The reference allows it for users and groups and
// refuses it for anything else, so it is not a preference.
func (p principal) notifiable() bool {
	return p.kind == principalUser || p.kind == principalGroup
}

// matches reports whether a grant is this principal's.
func (p principal) matches(g model.Grant) bool {
	if g.Type != p.kind {
		return false
	}
	switch p.kind {
	case principalAnyone:
		return true
	case principalDomain:
		return strings.EqualFold(g.Who, p.domain)
	default:
		return strings.EqualFold(g.Who, p.email)
	}
}

// allows refuses the role and principal combinations Drive does not
// have, and the one this server refuses on its own: a public link
// without the acknowledgement that it is public.
func (p principal) allows(role string, allowAnyone bool) error {
	if p.kind == principalAnyone && !allowAnyone {
		return Errorf(ClassForbidden, "an anyone-with-the-link grant puts this file within reach of everyone "+
			"who has or guesses the link, with no sign-in. Pass allow_anyone: true on the same call if that "+
			"is really what is wanted.")
	}
	switch p.kind {
	case principalAnyone, principalDomain:
		if role == model.RoleOwner {
			return Errorf(ClassInvalid, "a file is owned by a person, not by %s.", p.label())
		}
		if role == model.RoleOrganizer || role == model.RoleFileOrganizer {
			return Errorf(ClassInvalid, "organizer and file_organizer are for the members of a shared drive, "+
				"and %s is not a member. Use writer, commenter or reader.", p.label())
		}
	case principalGroup:
		if role == model.RoleOwner {
			return Errorf(ClassInvalid, "Drive does not make a group the owner of a file; ownership goes to "+
				"a person.")
		}
	}
	return nil
}

// parseRole maps the word a caller passes onto Drive's own.
func parseRole(v string) (string, error) {
	name := strings.ToLower(strings.TrimSpace(v))
	if name == "" {
		return "", Errorf(ClassInvalid, "role is required: one of %s", strings.Join(Roles(), ", "))
	}
	role, ok := roles[name]
	if !ok {
		return "", Errorf(ClassInvalid, "role %q is not one of %s", v, strings.Join(Roles(), ", "))
	}
	return role, nil
}

// parseExpiry reads an RFC 3339 instant or a duration like 30d and
// checks it against the rules Drive states: users and groups only, in
// the future, at most a year out. Refusing here names which rule was
// broken, where Drive's own answer is a generic 400.
// parseExpiry reads when a grant should end, and reports separately that
// it should not end at all. "never" is its own answer rather than an
// empty string, because leaving expires out has to keep meaning "do not
// touch the expiry this grant already has".
func (s *Service) parseExpiry(v string, p principal, role string) (expires string, clear bool, err error) {
	raw := strings.TrimSpace(v)
	if raw == "" {
		return "", false, nil
	}
	if p.kind != principalUser && p.kind != principalGroup {
		return "", false, Errorf(ClassInvalid, "Drive only lets a grant to a person or a group expire, and "+
			"this one is to %s. Remove it with unshare_file when it is no longer wanted.", p.label())
	}
	if role == model.RoleOwner {
		return "", false, Errorf(ClassInvalid, "ownership does not expire; a transfer is permanent until it "+
			"is transferred again.")
	}
	if strings.EqualFold(raw, "never") {
		return "", true, nil
	}
	now := s.now()
	when, err := parseExpiryTime(raw, now)
	if err != nil {
		return "", false, err
	}
	switch {
	case !when.After(now):
		return "", false, Errorf(ClassInvalid, "expires %q is not in the future (it is %s). Drive refuses an "+
			"expiry that has already passed.", raw, model.Ago(when, now))
	case when.Sub(now) > maxExpiry:
		return "", false, Errorf(ClassInvalid, "expires %q is more than a year away, and Drive's limit is a "+
			"year. Pass never to remove an expiry a grant already has.", raw)
	}
	return when.UTC().Format(time.RFC3339), false, nil
}

// parseExpiryTime reads when a grant should end: an absolute date, or a
// duration from now. Days are spelled out because Go's ParseDuration
// stops at hours, and "30d" is how a person writes a month.
func parseExpiryTime(raw string, now time.Time) (time.Time, error) {
	if days, ok := strings.CutSuffix(raw, "d"); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(days)); err == nil {
			return now.Add(time.Duration(n) * 24 * time.Hour), nil
		}
	}
	if d, err := time.ParseDuration(raw); err == nil {
		return now.Add(d), nil
	}
	if t, ok := parseInstant(raw); ok {
		return t, nil
	}
	return time.Time{}, Errorf(ClassInvalid, "expires %q is neither a date like 2026-12-01 nor a duration "+
		"like 30d or 12h", raw)
}

// grantWords says what access a principal had before a change.
func grantWords(g *model.Grant) string {
	if g == nil {
		return "no access"
	}
	return model.RoleWords(g.Role) + expiryWords(g.Expires)
}

func expiryWords(expires string) string {
	if expires == "" {
		return ""
	}
	return " until " + expires
}

// joinSentences puts a list of clauses together as prose, capitalising
// nothing: the note is read as a continuation of the line above it.
func joinSentences(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, ". ") + "."
}
