package service

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/mmedum/google-drive-mcp/v3/internal/gapi"
	"github.com/mmedum/google-drive-mcp/v3/internal/gdrive"
	"github.com/mmedum/google-drive-mcp/v3/internal/model"
	"github.com/mmedum/google-drive-mcp/v3/internal/render"
)

// Budgets for a recursive copy. They are separate constants from the
// tree walk's even though two of them match today: a listing that stops
// short shows what it found, and a copy that stops short leaves half a
// tree somebody has to reconcile by hand.
const (
	DefaultCopyItems = 200
	MaxCopyItems     = 2000
	MaxCopyDepth     = 10
)

// copyTree copies a folder and everything under it.
//
// The walk happens FIRST, in full, and the copying only starts if the
// whole tree fits inside the budgets. A budget that cuts a copy halfway
// through leaves a folder that looks complete and is not, and nothing in
// the result can make that safe — where a listing that stops short is
// merely a listing that stops short. So the budgets are a refusal here
// rather than a truncation, and the walk costs nothing extra: it is the
// same listing per folder the copy needs anyway.
func (s *Service) copyTree(ctx context.Context, res *Resolved, in CopyFileInput) (*Result, error) {
	source := res.File
	if cannotList(source) {
		return nil, Errorf(ClassForbidden, "this account cannot list what is inside %s, so it cannot copy it.",
			source.Name)
	}
	items := in.MaxItems
	switch {
	case items <= 0:
		items = DefaultCopyItems
	case items > MaxCopyItems:
		return nil, Errorf(ClassInvalid, "max_items %d is above the ceiling of %d for one copy. Copy a "+
			"subfolder at a time.", in.MaxItems, MaxCopyItems)
	}

	// The destination is settled BEFORE the walk. A walk is one listing
	// per folder at twenty units each, and a bad destination or a name
	// already taken would throw all of it away; resolving the parent is
	// one mostly-cached read and the duplicate check is one listing,
	// whatever the tree turns out to be.
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = "Copy of " + source.Name
	}
	place, err := s.copyPlace(ctx, source, in.To, name, in.AllowDuplicate)
	if err != nil {
		return nil, err
	}
	destination, destName, target := place.id, place.name, place.target

	plan, err := s.planCopy(ctx, source, items)
	if err != nil {
		return nil, err
	}
	// A folder copied into itself or into its own subtree would copy the
	// copy. The walk has just named every folder under the source, so
	// the check costs nothing and is exact — which is why this one waits
	// for the plan while the two above do not.
	if destination == source.ID || plan.folderIDs[destination] {
		return nil, Errorf(ClassInvalid, "%s cannot be copied into itself or into a folder inside it; the "+
			"copy would then be inside what is being copied. Pick a destination outside %s.",
			source.Name, source.Name)
	}

	// Every item of the copy reaches who the destination does, while the
	// tree it comes from may reach fewer: the folder itself, or a folder
	// inside it with limited access.
	parts := s.predictCopy(ctx, res, plan.items, target)
	if err := s.copyRefused(ctx, parts); err != nil {
		return nil, err
	}
	if in.DryRun {
		note := plan.words(name, destName)
		for _, p := range parts {
			if p.widens() {
				note += " " + copyWidens(p, p == parts[0])
			}
		}
		return s.copyResult(ctx, res, parts[0].before, parts[0].after, outcome{Action: render.ActionCopied,
			DryRun: true, Note: note + " Nothing was copied."}), nil
	}
	created, note, err := s.runCopy(ctx, source, plan, name, destination, destName, in.KeepRevisionForever)
	if err != nil {
		return nil, err
	}
	s.forget(created, false)
	after := s.sharingNow(ctx, created)
	if gained := s.moveNote(ctx, parts[0], after, false); gained != "" {
		note += " " + sentence(gained)
	}
	return s.copyResult(ctx, &Resolved{File: created}, parts[0].before, after, outcome{Action: render.ActionCopied,
		Note: note}), nil
}

// copyWidens says, on a dry run, that one part of a tree would be
// reached further by its copy. A part copyRefused let through gains only
// people and groups inside the organization.
func copyWidens(p *movePlan, whole bool) string {
	what := "The copy would reach more people than the original does, all of them inside this account's " +
		"organization, or give them more access: "
	if !whole {
		what = "Inside it, " + p.res.File.Name + " has limited access, and its copy would reach more people " +
			"than it does, all of them inside this account's organization, or give them more access: "
	}
	return what + render.Reach(p.gained) + ". A real copy does that."
}

// treePlan is everything the walk found, in the order it has to be
// written: a folder always comes before what is inside it.
type treePlan struct {
	items []*gdrive.File
	// folderIDs is every folder in the source tree, for the check that
	// the destination is not one of them.
	folderIDs map[string]bool
	folders   int
	files     int
	shortcuts int
	depth     int
}

func (p *treePlan) words(name, destination string) string {
	held := []string{model.Plural(p.folders, "folder", "folders"), model.Plural(p.files, "file", "files")}
	if p.shortcuts > 0 {
		held = append(held, model.Plural(p.shortcuts, "shortcut", "shortcuts"))
	}
	first := fmt.Sprintf("%s would hold %s, nested %s deep",
		name, strings.Join(held, ", "), model.Plural(p.depth, "level", "levels"))
	if destination != "" {
		first += ", in " + destination
	}
	parts := []string{first}
	if p.shortcuts > 0 {
		parts = append(parts, "the shortcuts would be made again pointing where they point now, "+
			"not at the copies")
	}
	return joinSentences(parts)
}

// planCopy walks the source breadth first and refuses a tree that does
// not fit. Breadth first is not an accident: it is the order the writes
// happen in, so a plan that lists a folder before its contents cannot be
// executed out of order.
func (s *Service) planCopy(ctx context.Context, root *gdrive.File, maxItems int) (*treePlan, error) {
	plan := &treePlan{folderIDs: map[string]bool{root.ID: true}}
	// One level at a time, a batch of its folders listed at once: each
	// listing is a round trip that does not depend on the others. The
	// results are read in order, and the budget and any error are checked
	// between batches, so a tree too big is refused after at most one
	// batch past the point a walk in turn would stop.
	level := []*gdrive.File{root}
	for depth := 0; len(level) > 0; depth++ {
		plan.depth = depth
		if depth >= MaxCopyDepth {
			return nil, Errorf(ClassUnsupported, "%s is more than %d folders deep. Copying part of it at a "+
				"time is the way through: a copy that stopped at the limit would look finished and would "+
				"not be.", root.Name, MaxCopyDepth)
		}
		var next []*gdrive.File
		for start := 0; start < len(level); start += levelFanOut {
			batch := level[start:min(len(level), start+levelFanOut)]
			// One over the budget left when the batch starts, so a tree that
			// is exactly too big is caught by what came back rather than by
			// a second call.
			left := maxItems - len(plan.items)
			type listed struct {
				children []*gdrive.File
				more     bool
				err      error
			}
			out := make([]listed, len(batch))
			fanOut(len(batch), levelFanOut, func(i int) {
				out[i].children, out[i].more, out[i].err = s.childrenOf(ctx, batch[i], "", false, left+1)
			})
			for i := range batch {
				if out[i].err != nil {
					return nil, out[i].err
				}
				if out[i].more || len(out[i].children) > maxItems-len(plan.items) {
					return nil, Errorf(ClassInvalid, "%s holds more than the %d items this copy is allowed. Raise "+
						"max_items, up to %d, or copy a subfolder at a time; nothing has been copied.",
						root.Name, maxItems, MaxCopyItems)
				}
				for _, c := range out[i].children {
					if c.IsFolder() && cannotList(c) {
						return nil, Errorf(ClassForbidden, "this account cannot list what is inside %s, in %s, so "+
							"it cannot copy %s whole; nothing has been copied.", c.Name, root.Name, root.Name)
					}
					plan.items = append(plan.items, c)
					switch {
					case c.IsFolder():
						plan.folders++
						plan.folderIDs[c.ID] = true
						next = append(next, c)
					case c.IsShortcut():
						plan.shortcuts++
					default:
						plan.files++
					}
				}
			}
		}
		level = next
	}
	return plan, nil
}

// levelFanOut is how many listings or writes of one level of a tree run at
// once, for a recursive copy and a recursive listing. The client paces
// both, so this only keeps a level from waiting on each round trip in
// turn.
const levelFanOut = 4

// fanOut runs do for each of 0 to n-1, at most limit at once, and
// returns when every one has. A panic in one is raised again here, on the
// caller's goroutine, where the handler's recovery sees it as before.
func fanOut(n, limit int, do func(i int)) {
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	var once sync.Once
	var panicked any
	for i := range n {
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			defer func() {
				if r := recover(); r != nil {
					once.Do(func() { panicked = r })
				}
			}()
			do(i)
		}()
	}
	wg.Wait()
	if panicked != nil {
		panic(panicked)
	}
}

// byLevel splits a breadth-first plan into its levels, so every item's
// parent is the source or is in an earlier level.
func byLevel(source string, items []*gdrive.File) [][]*gdrive.File {
	depth := map[string]int{source: 0}
	var levels [][]*gdrive.File
	for _, item := range items {
		d := depth[item.Parent()] + 1
		depth[item.ID] = d
		for len(levels) < d {
			levels = append(levels, nil)
		}
		levels[d-1] = append(levels[d-1], item)
	}
	return levels
}

// runCopy writes the tree. Every item is attempted: a failure partway
// through cannot be rolled back — Drive has no transaction and the
// copies already made are real files — so the honest thing is to finish
// and say exactly what did not make it.
func (s *Service) runCopy(ctx context.Context, source *gdrive.File, plan *treePlan,
	name, destination, destName string, keepRevision bool,
) (*gdrive.File, string, error) {
	ids := s.idPool(ctx, plan)
	rootMeta := &gdrive.FileMeta{Name: name, MimeType: gdrive.MimeFolder, ID: ids.take(gdrive.MimeFolder)}
	if destination != "" {
		rootMeta.Parents = []string{destination}
	}
	created, err := s.api.CreateFile(ctx, rootMeta, gapi.WriteOptions{ResourceIDs: []string{destination}})
	if err != nil {
		return nil, "", wrap(err, fmt.Sprintf("making the folder %s to copy %s into", name, source.Name))
	}

	// copies maps a source folder id to the folder that now stands for it.
	copies := map[string]string{source.ID: created.ID}
	var failed []string
	done := 0
	// A level's items are written at once: each one's folder was made in an
	// earlier level, and copies is written only between levels. The
	// results are read in plan order, so what a copy reports is the same.
	for _, level := range byLevel(source.ID, plan.items) {
		type written struct {
			id       string
			err      error
			orphaned bool
		}
		out := make([]written, len(level))
		fanOut(len(level), levelFanOut, func(i int) {
			parent, ok := copies[level[i].Parent()]
			if !ok {
				out[i].orphaned = true
				return
			}
			out[i].id, out[i].err = s.copyOne(ctx, level[i], parent, ids, keepRevision)
		})
		for i, item := range level {
			switch r := out[i]; {
			case r.orphaned:
				// Its folder failed, so there is nowhere to put it. Saying so
				// once per orphan would bury the failure that caused them.
			case r.err != nil:
				failed = append(failed, fmt.Sprintf("%s (%s)", item.Name, gapi.Message(r.err)))
			default:
				done++
				if item.IsFolder() {
					copies[item.ID] = r.id
				}
			}
		}
	}
	return created, copyNote(source, plan, destName, done, failed), nil
}

// copiedItemFields is all a tree copy reads back per item. The default
// is the whole file card — permissions, export links, capabilities,
// properties — and this loop uses the id and nothing else, so on a
// two-hundred-item tree the default is two hundred full metadata
// documents on the wire and parsed into maps that are dropped on the
// next line. The folder the copy lands in is read in full, once, because
// the result renders a card from it.
const copiedItemFields = "id"

// copyOne writes one item of the tree and returns the new id. A folder
// is created, a shortcut is made again, and everything else is copied.
func (s *Service) copyOne(ctx context.Context, item *gdrive.File, parent string, ids *idPool,
	keepRevision bool,
) (string, error) {
	switch {
	case item.IsFolder():
		made, err := s.api.CreateFile(ctx, &gdrive.FileMeta{
			Name: item.Name, MimeType: gdrive.MimeFolder, Parents: []string{parent},
			ID: ids.take(gdrive.MimeFolder),
		}, gapi.WriteOptions{Fields: copiedItemFields, ResourceIDs: []string{parent}})
		if err != nil {
			return "", err
		}
		return made.ID, nil
	case item.IsShortcut():
		// A shortcut is made again rather than copied: what files.copy
		// does with one is not verified against Drive, and making one is
		// checked by the live step "a shortcut to a file is created". The
		// new shortcut points where the old one points, including at a
		// file inside the tree that was just copied — a copy of a pointer
		// is a pointer to the same thing.
		meta := &gdrive.FileMeta{
			Name: item.Name, MimeType: gdrive.MimeShortcut, Parents: []string{parent},
			ShortcutDetails: &gdrive.ShortcutDetails{},
		}
		if item.ShortcutDetails != nil {
			meta.ShortcutDetails.TargetID = item.ShortcutDetails.TargetID
		}
		if meta.ShortcutDetails.TargetID == "" {
			return "", Errorf(ClassInvalid, "the shortcut %s names no target", item.Name)
		}
		made, err := s.api.CreateFile(ctx, meta, gapi.WriteOptions{
			Fields: copiedItemFields, ResourceIDs: []string{parent}})
		if err != nil {
			return "", err
		}
		return made.ID, nil
	default:
		// keep_revision_forever applies to the files, not to the folders
		// that hold them: a folder has no content and so no revision to
		// pin. Passing it here rather than ignoring it is the difference
		// between honoring an argument and accepting one.
		made, err := s.api.CopyFile(ctx, item.ID, &gdrive.FileMeta{
			Name: item.Name, Parents: []string{parent}, ID: ids.take(item.MimeType),
		}, gapi.WriteOptions{Fields: copiedItemFields, ResourceIDs: []string{item.ID, parent},
			KeepRevisionForever: keepRevision})
		if err != nil {
			return "", err
		}
		return made.ID, nil
	}
}

func copyNote(source *gdrive.File, plan *treePlan, destName string, done int, failed []string) string {
	first := fmt.Sprintf("copied %d of %d items from %s", done, len(plan.items), source.Name)
	if destName != "" {
		first += ", into " + destName
	}
	parts := []string{first}
	if plan.shortcuts > 0 {
		parts = append(parts, fmt.Sprintf("%s made again pointing where the originals point, not at "+
			"the copies", model.Plural(plan.shortcuts, "shortcut", "shortcuts")))
	}
	if len(failed) > 0 {
		parts = append(parts, "these did not copy and are not in the new folder: "+strings.Join(failed, "; ")+
			". What did copy is real and stays; copy the rest one at a time, or trash the new folder and "+
			"start again")
	}
	return joinSentences(parts)
}

// idPool hands out pre-generated ids for a tree copy.
//
// One create carrying a pre-generated id is idempotent: a retry after an
// ambiguous failure is collapsed into the first rather than making a
// second file. Asking for them one at a time costs a round trip per
// item, which on a two-hundred-item tree is two hundred round trips
// before any work; asking once for the whole tree costs one. Drive
// documents no cost to an id that is never used, and this pool asks for
// exactly what the walk counted, so almost none go unused.
//
// The Docs Editors formats refuse a generated id, so a copy that becomes
// one takes none, and take() is the single place that knows it.
type idPool struct {
	mu  sync.Mutex
	ids []string
}

func (s *Service) idPool(ctx context.Context, plan *treePlan) *idPool {
	// The root folder, plus every item that will take an id.
	// AcceptsGeneratedID already answers for a shortcut, which is a
	// Google type and not a folder; asking again here would be a second
	// place that knows which types refuse an id, and take() is meant to
	// be the only one.
	want := 1
	for _, item := range plan.items {
		if gapi.AcceptsGeneratedID(item.MimeType) {
			want++
		}
	}
	ids, err := s.api.GenerateIDs(ctx, want)
	if err != nil {
		// The pool is an optimization and a safety net, not a
		// requirement: without it every create is simply not repeatable,
		// which is what a create with no id has always been.
		s.log.DebugContext(ctx, "id pool unavailable", "class", gapi.Class(err), "wanted", want)
		return &idPool{}
	}
	return &idPool{ids: ids}
}

// take returns an id for a file of this type, or "" when Drive would
// refuse one.
func (p *idPool) take(mime string) string {
	if p == nil {
		return ""
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.ids) == 0 || !gapi.AcceptsGeneratedID(mime) {
		return ""
	}
	id := p.ids[0]
	p.ids = p.ids[1:]
	return id
}
