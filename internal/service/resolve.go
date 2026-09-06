package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mmedum/google-drive-mcp/internal/gapi"
	"github.com/mmedum/google-drive-mcp/internal/gdrive"
	"github.com/mmedum/google-drive-mcp/internal/model"
	"github.com/mmedum/google-drive-mcp/internal/ref"
)

// RootAlias is the id Drive accepts for the root of My Drive.
const RootAlias = "root"

// maxPathDepth bounds how far a location walk climbs. My Drive allows
// 100 levels; a path longer than this says so rather than costing a
// hundred reads.
const maxPathDepth = 25

// Resolved is a reference turned into one file.
type Resolved struct {
	File *gdrive.File
	Ref  ref.Ref
	// FollowedShortcut is the shortcut's id when a read followed one to
	// its target, so the result can say where it ended up.
	FollowedShortcut string
	// DriveName is the shared drive the file lives in, when it does.
	DriveName string
}

// ResolveOptions tune a resolution.
type ResolveOptions struct {
	// FollowShortcut makes the resolver return a shortcut's target
	// instead of the shortcut. Read and content tools follow; organising,
	// sharing and trash tools act on the shortcut itself, because that is
	// the thing sitting in the folder the person is looking at.
	FollowShortcut bool
	// IncludeLabels asks Drive for labelInfo.
	IncludeLabels bool
	// Fresh skips the short-lived file cache.
	Fresh bool
}

// Resolve turns any accepted reference into one file. A name or path
// that matches more than one item is refused with the candidates listed:
// the model picks an id, and this server never takes the first match.
func (s *Service) Resolve(ctx context.Context, reference string, o ResolveOptions) (*Resolved, error) {
	r, err := ref.Parse(reference)
	if err != nil {
		return nil, &Error{Class: ClassInvalid, Message: err.Error(), Err: err}
	}
	return s.resolveRef(ctx, r, o)
}

func (s *Service) resolveRef(ctx context.Context, r ref.Ref, o ResolveOptions) (*Resolved, error) {
	var id string
	switch r.Kind {
	case ref.KindID:
		if r.ResourceKey != "" {
			s.api.RememberResourceKey(r.ID, r.ResourceKey)
		}
		id = r.ID
	case ref.KindRoot:
		id = RootAlias
	case ref.KindPath:
		got, err := s.walk(ctx, RootAlias, "My Drive", r.Segments)
		if err != nil {
			return nil, err
		}
		id = got
	case ref.KindDrivePath:
		d, err := s.findDrive(ctx, r.Drive)
		if err != nil {
			return nil, err
		}
		if len(r.Segments) == 0 {
			id = d.ID
		} else {
			// A shared drive's root folder id is the drive id.
			got, err := s.walk(ctx, d.ID, d.Name+" (shared drive)", r.Segments)
			if err != nil {
				return nil, err
			}
			id = got
		}
	}

	f, err := s.fetch(ctx, id, o)
	if err != nil {
		if alt, ok := s.retryIDAsName(ctx, r, err, o); ok {
			return alt, nil
		}
		return nil, s.notFoundHint(err, r)
	}
	res := &Resolved{File: f, Ref: r}
	if o.FollowShortcut && f.IsShortcut() && f.ShortcutDetails != nil && f.ShortcutDetails.TargetID != "" {
		if f.ShortcutDetails.TargetResourceKey != "" {
			s.api.RememberResourceKey(f.ShortcutDetails.TargetID, f.ShortcutDetails.TargetResourceKey)
		}
		target, err := s.fetch(ctx, f.ShortcutDetails.TargetID, o)
		if err != nil {
			return nil, wrap(err, fmt.Sprintf("following the shortcut %q to its target", f.Name))
		}
		res.FollowedShortcut = f.ID
		res.File = target
	}
	if res.File.DriveID != "" {
		if d, err := s.driveByID(ctx, res.File.DriveID); err == nil && d != nil {
			res.DriveName = d.Name
		}
	}
	return res, nil
}

// retryIDAsName covers the one genuinely ambiguous input: a bare word in
// the id alphabet that Drive does not know. It could have been a file
// name all along, so it is tried as one under My Drive rather than left
// as a puzzling "not found". A name that matches several items is still
// reported as ambiguous; nothing is guessed.
func (s *Service) retryIDAsName(ctx context.Context, r ref.Ref, err error, o ResolveOptions) (*Resolved, bool) {
	if r.Kind != ref.KindID || r.FromURL || !errors.Is(err, gapi.ErrNotFound) {
		return nil, false
	}
	// Only for a word that could plausibly have been a name. Drive ids
	// are base64 of random bytes, so one without a digit essentially
	// does not occur; a stale or mistyped id is the commoner input by
	// far, and it is not worth two listings to tell it that it is wrong.
	if strings.ContainsAny(r.ID, "0123456789") {
		return nil, false
	}
	byName := ref.Ref{Kind: ref.KindPath, Segments: []string{r.ID}, Raw: r.Raw}
	res, nameErr := s.resolveRef(ctx, byName, o)
	if nameErr != nil {
		return nil, false
	}
	s.log.DebugContext(ctx, "reference read as a name after no file had that id")
	return res, true
}

// notFoundHint turns Drive's bare 404 into something the model can act
// on, because "File not found: <id>" says nothing about which of the
// several reasons applies.
func (s *Service) notFoundHint(err error, r ref.Ref) error {
	e := wrap(err, "reading "+r.Display())
	var se *Error
	if !errors.As(e, &se) || se.Class != ClassNotFound {
		return e
	}
	se.Message = fmt.Sprintf("nothing at %s. Either the id is wrong, or this account cannot see it: "+
		"a file shared by link may also need its resource key, which arrives in the URL as ?resourcekey=…", r.Display())
	if r.Kind == ref.KindID {
		se.Message += ". search_files finds it by name if you have one."
	}
	return se
}

// walk resolves a path one segment at a time, listing each folder for an
// exact name match. It is the only place a name becomes an id, and it
// reports a tie rather than picking a winner.
func (s *Service) walk(ctx context.Context, rootID, rootName string, segments []string) (string, error) {
	parent := rootID
	walked := make([]string, 0, len(segments))
	for i, segment := range segments {
		// A shortcut part-way along a path is a way through to a folder,
		// so it is followed. The last segment is the thing the caller
		// named, and whether to follow it is the caller's decision
		// (ResolveOptions.FollowShortcut), taken once in resolveRef.
		last := i == len(segments)-1
		id, err := s.child(ctx, parent, segment, rootName, walked, !last)
		if err != nil {
			return "", err
		}
		parent = id
		walked = append(walked, segment)
	}
	return parent, nil
}

func (s *Service) child(ctx context.Context, parent, name, rootName string, walked []string, followShortcut bool) (string, error) {
	key := pathKey(parent, name, followShortcut)
	s.mu.Lock()
	id, ok := cacheGet(s.paths, key, s.now(), s.opts.PathTTL)
	s.mu.Unlock()
	if ok {
		return id, nil
	}

	q := childQuery(parent, "name = "+quote(name), false)
	list, err := s.api.ListFiles(ctx, gapi.ListQuery{
		Q: q, PageSize: 10,
		Fields:      "files(id,name,mimeType,modifiedTime,shortcutDetails,driveId,resourceKey)",
		ResourceIDs: []string{parent},
	})
	if err != nil {
		return "", wrap(err, fmt.Sprintf("looking for %q in %s", name, pathSoFar(rootName, walked)))
	}

	switch len(list.Files) {
	case 0:
		return "", s.notFoundInFolder(ctx, parent, name, rootName, walked)
	case 1:
		got := list.Files[0]
		if followShortcut && got.IsShortcut() && got.ShortcutDetails != nil && got.ShortcutDetails.TargetID != "" {
			s.log.DebugContext(ctx, "path went through a shortcut", "at", gapi.ShortID(got.ID))
			got = &gdrive.File{ID: got.ShortcutDetails.TargetID}
		}
		s.mu.Lock()
		s.paths[key] = cached[string]{value: got.ID, at: s.now()}
		s.mu.Unlock()
		return got.ID, nil
	}
	return "", s.ambiguous(name, pathSoFar(rootName, walked), list.Files)
}

// ambiguous refuses to guess and hands back every candidate, because
// acting on the wrong file is the failure this design exists to prevent.
func (s *Service) ambiguous(name, where string, files []*gdrive.File) error {
	var b strings.Builder
	fmt.Fprintf(&b, "%d items in %s are named %q. Pass one of these ids instead of a path:", len(files), where, name)
	for _, f := range files {
		fmt.Fprintf(&b, "\n  %s  %s", f.ID, model.Kind(f))
		if f.ModifiedTime != "" {
			fmt.Fprintf(&b, ", modified %s", f.ModifiedTime)
		}
	}
	return &Error{Class: ClassAmbiguous, Message: b.String()}
}

// siblingsShown is how many of a folder's contents a not-found message
// names. Enough to pick the right one from; short enough to read.
const siblingsShown = 12

// notFoundInFolder says what is actually in the folder, so the next call
// can be right rather than another guess. The listing it spends is the
// same one a `list_folder` would cost, and it buys the names rather than
// a count the caller would then have to go and look up.
func (s *Service) notFoundInFolder(ctx context.Context, parent, name, rootName string, walked []string) error {
	where := pathSoFar(rootName, walked)
	msg := fmt.Sprintf("nothing named %q in %s. Names must match exactly, including capitals; "+
		"search_files matches word beginnings instead", name, where)
	list, err := s.api.ListFiles(ctx, gapi.ListQuery{
		Q: childQuery(parent, "", false), PageSize: siblingsShown + 1,
		OrderBy: "folder,name_natural", Fields: "files(id,name,mimeType)", ResourceIDs: []string{parent},
	})
	if err != nil {
		// Naming the siblings is a courtesy; failing at it must not
		// replace the error the caller actually needs.
		return &Error{Class: ClassNotFound, Message: msg + ". list_folder shows what is there."}
	}
	if len(list.Files) == 0 {
		return &Error{Class: ClassNotFound, Message: msg + ". That folder is empty."}
	}
	names := make([]string, 0, siblingsShown)
	for _, f := range list.Files[:min(len(list.Files), siblingsShown)] {
		n := f.Name
		if f.IsFolder() {
			n += "/"
		}
		names = append(names, n)
	}
	msg += ". That folder holds " + strings.Join(names, ", ")
	if len(list.Files) > siblingsShown {
		msg += ", and more (list_folder shows them all)"
	}
	return &Error{Class: ClassNotFound, Message: msg + "."}
}

func pathSoFar(root string, walked []string) string {
	if len(walked) == 0 {
		return root
	}
	return root + "/" + strings.Join(walked, "/")
}

// fetch reads one file, coalescing repeats within a few seconds so a
// tool that needs the same file twice pays for it once.
func (s *Service) fetch(ctx context.Context, id string, o ResolveOptions) (*gdrive.File, error) {
	key := id
	if o.IncludeLabels {
		key += "\x00labels"
	}
	if !o.Fresh {
		s.mu.Lock()
		f, ok := cacheGet(s.files, key, s.now(), s.opts.FileTTL)
		s.mu.Unlock()
		if ok {
			return f, nil
		}
	}
	f, err := s.api.GetFile(ctx, id, gapi.GetFileOptions{})
	if err != nil {
		return nil, err
	}
	// Labels cost a second call. Drive's includeLabels parameter would
	// fold them into the first, but only for label ids the caller already
	// knows, and a file card's whole question is which labels are on this
	// file. files.listLabels answers that, and needs no labels scope.
	if o.IncludeLabels {
		labels, err := s.api.AllFileLabels(ctx, id)
		if err != nil {
			return nil, err
		}
		f.LabelInfo = &gdrive.LabelInfo{Labels: labels}
	}
	s.mu.Lock()
	s.files[key] = cached[*gdrive.File]{value: f, at: s.now()}
	// A full read carries every field a parent lookup wants, so it fills
	// that entry too: without this, moving a file into a folder read it,
	// and then the location walk read the same folder again.
	s.files[parentKey(id)] = cached[*gdrive.File]{value: f, at: s.now()}
	s.mu.Unlock()
	return f, nil
}

// Location works out where a file sits by climbing its parents. Names
// are not unique in Drive and a file's meaning depends on where it is,
// so every result carries one. A single card may spend as many reads as
// the chain needs; a page of results shares one budget (see decorate).
func (s *Service) Location(ctx context.Context, f *gdrive.File) model.Location {
	budget := maxPathDepth
	return s.locationWithBudget(ctx, f, &budget)
}

// locationWithBudget climbs the parent chain. Parents already known cost
// nothing; each fresh read spends one unit of the budget, and running
// out marks the path as having more folders above rather than inventing
// a shorter one.
func (s *Service) locationWithBudget(ctx context.Context, f *gdrive.File, budget *int) model.Location {
	loc := model.Location{Drive: "My Drive"}
	if f == nil {
		return loc
	}
	if f.DriveID != "" {
		loc.SharedDrive = true
		loc.Drive = f.DriveID
		if d, err := s.driveByID(ctx, f.DriveID); err == nil && d != nil {
			loc.Drive = d.Name
		}
	}
	// Only a My Drive path needs the root id; inside a shared drive the
	// drive itself is the head of the path.
	root := ""
	if f.DriveID == "" {
		root = s.rootID(ctx)
	}
	if f.Parent() == "" {
		return s.locationWithoutParent(f, root, loc)
	}
	loc.Folders, loc.Above = s.climb(ctx, f, root, loc.SharedDrive, budget)
	return loc
}

// locationWithoutParent decides what a file with no visible parent is.
// Three different things look identical on the wire, and calling any of
// them by the wrong name misleads: the drive's own root, a file someone
// shared, and a file that really has lost its only parent.
func (s *Service) locationWithoutParent(f *gdrive.File, root string, loc model.Location) model.Location {
	switch {
	case f.SharedWithMeTime != "":
		loc.SharedWithMe = true
	case f.DriveID != "" && f.ID == f.DriveID:
		// The drive's own root folder is the drive.
	case f.ID == root || f.ID == RootAlias:
		// My Drive itself.
	default:
		// Drive shows these under "Orphaned"; saying so beats inventing
		// a folder that does not exist.
		loc.Orphaned = true
	}
	return loc
}

// climb walks from a file up to the root of its drive, returning the
// folder names top-down and whether the walk stopped short. Parents
// already known cost nothing; each fresh read spends one unit of the
// budget, and running out reports a gap rather than a shorter path.
func (s *Service) climb(ctx context.Context, f *gdrive.File, root string, inSharedDrive bool, budget *int) (names []string, above bool) {
	parent := f.Parent()
	seen := map[string]bool{}
	for depth := 0; parent != "" && depth < maxPathDepth; depth++ {
		if seen[parent] || (inSharedDrive && parent == f.DriveID) {
			break
		}
		if parent == root || parent == RootAlias {
			// The root of My Drive is the head of the path already, and
			// reading it would spend a call to learn its own id.
			break
		}
		seen[parent] = true
		p, ok := s.cachedParent(parent)
		if !ok {
			if *budget <= 0 {
				return reverse(names), true
			}
			var err error
			if p, err = s.fetchParent(ctx, parent); err != nil {
				return reverse(names), true
			}
			*budget--
		}
		names = append(names, p.Name)
		parent = p.Parent()
	}
	return reverse(names), parent != "" && len(names) >= maxPathDepth
}

// reverse turns a bottom-up walk into a top-down path.
func reverse(s []string) []string {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
	return s
}

// rootID is the id of My Drive's root folder. Drive answers to the alias
// "root" but reports the real id everywhere else, so the two have to be
// tied together to tell the root apart from a file that has genuinely
// lost its parent. It never changes, so it is read at most once and an
// unreachable Drive simply leaves it empty.
func (s *Service) rootID(ctx context.Context) string {
	s.mu.Lock()
	if s.rootTried {
		id := s.root
		s.mu.Unlock()
		return id
	}
	s.mu.Unlock()

	id := ""
	if f, err := s.api.GetFile(ctx, RootAlias, gapi.GetFileOptions{Fields: "id"}); err == nil {
		id = f.ID
	}
	// The attempt is recorded either way. Without that, an unreachable
	// Drive costs one read per file in a page of results.
	s.mu.Lock()
	s.root, s.rootTried = id, true
	s.mu.Unlock()
	return id
}

// fetchParent reads just what a location needs, and caches it: a folder
// is a parent to many files and its name does not change often.
func (s *Service) fetchParent(ctx context.Context, id string) (*gdrive.File, error) {
	key := parentKey(id)
	s.mu.Lock()
	f, ok := cacheGet(s.files, key, s.now(), s.opts.PathTTL)
	s.mu.Unlock()
	if ok {
		return f, nil
	}
	f, err := s.api.GetFile(ctx, id, gapi.GetFileOptions{Fields: "id,name,parents,driveId,mimeType"})
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.files[key] = cached[*gdrive.File]{value: f, at: s.now()}
	s.mu.Unlock()
	return f, nil
}

// parentKey namespaces the cache entry for a parent read, which asks for
// fewer fields than a full file read and must not be mistaken for one.
func parentKey(id string) string { return "parent\x00" + id }

// pathKey identifies one (folder, name) lookup. A shortcut resolves to
// two different answers depending on whether the walk was passing
// through it, so the two cannot share an entry. Every reader and every
// writer of the path cache builds its key here: an eviction that spelled
// the key differently would silently evict nothing.
func pathKey(parent, name string, through bool) string {
	key := parent + "\x00" + name
	if through {
		key += "\x00through"
	}
	return key
}

// drivesList returns the shared drives this account can see, cached.
func (s *Service) drivesList(ctx context.Context) ([]*gdrive.Drive, error) {
	s.mu.Lock()
	if !s.drivesAt.IsZero() && s.now().Sub(s.drivesAt) <= s.opts.PathTTL {
		out := s.drives
		s.mu.Unlock()
		return out, nil
	}
	s.mu.Unlock()

	var all []*gdrive.Drive
	token := ""
	for {
		page, err := s.api.ListDrives(ctx, gapi.ListDrivesOptions{PageToken: token})
		if err != nil {
			return nil, err
		}
		all = append(all, page.Drives...)
		if page.NextPageToken == "" || len(page.Drives) == 0 {
			break
		}
		token = page.NextPageToken
	}
	s.mu.Lock()
	s.drives, s.drivesAt = all, s.now()
	s.mu.Unlock()
	return all, nil
}

// Drives returns the shared drives this account can see.
func (s *Service) Drives(ctx context.Context) ([]*gdrive.Drive, error) {
	d, err := s.drivesList(ctx)
	if err != nil {
		return nil, wrap(err, "listing shared drives")
	}
	return d, nil
}

func (s *Service) driveByID(ctx context.Context, id string) (*gdrive.Drive, error) {
	drives, err := s.drivesList(ctx)
	if err != nil {
		return nil, err
	}
	for _, d := range drives {
		if d.ID == id {
			return d, nil
		}
	}
	return nil, nil
}

// findDrive resolves a shared drive by id or by name. Two drives of one
// name are reported rather than guessed at, the same as any other tie.
func (s *Service) findDrive(ctx context.Context, nameOrID string) (*gdrive.Drive, error) {
	drives, err := s.drivesList(ctx)
	if err != nil {
		return nil, wrap(err, "listing shared drives")
	}
	if len(drives) == 0 {
		return nil, &Error{Class: ClassNotFound, Message: "this account can see no shared drives. " +
			"Shared drives are a Google Workspace feature; get_account says whether this account has them."}
	}
	var matches []*gdrive.Drive
	for _, d := range drives {
		if d.ID == nameOrID {
			return d, nil
		}
		if strings.EqualFold(d.Name, nameOrID) {
			matches = append(matches, d)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		names := make([]string, 0, len(drives))
		for _, d := range drives {
			names = append(names, d.Name)
		}
		return nil, &Error{Class: ClassNotFound, Message: fmt.Sprintf(
			"no shared drive named %q. This account can see: %s", nameOrID, strings.Join(names, ", "))}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d shared drives are named %q. Use drive:<id> with one of these:", len(matches), nameOrID)
	for _, d := range matches {
		fmt.Fprintf(&b, "\n  %s", d.ID)
	}
	return nil, &Error{Class: ClassAmbiguous, Message: b.String()}
}

// queryQuoter escapes what the search guide says must be escaped inside
// a query literal: a single quote and a backslash, each with a backslash.
var queryQuoter = strings.NewReplacer(`\`, `\\`, `'`, `\'`)

// quote wraps a value as a Drive query literal. Every value that reaches
// a query goes through here; nothing else builds one.
func quote(v string) string { return "'" + queryQuoter.Replace(v) + "'" }
