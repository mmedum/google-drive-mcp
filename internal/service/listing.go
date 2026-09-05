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

// Budgets for a folder walk. A list costs twenty reads in Drive's unit
// accounting, so a recursive walk is bounded twice: by how deep it goes
// and by how much it returns.
const (
	DefaultMaxDepth = 3
	MaxMaxDepth     = 10
	DefaultMaxItems = 200
	MaxMaxItems     = 2000
	// DefaultPageSize is one page of a flat listing.
	DefaultPageSize = 100
	MaxPageSize     = 200
)

// ListFolderInput selects what to list.
type ListFolderInput struct {
	Folder    string
	Kind      string
	PageSize  int
	PageToken string
	Recursive bool
	MaxDepth  int
	MaxItems  int
	// IncludeTrashed lists items in the trash as well; they are left out
	// by default so a listing shows what is really there.
	IncludeTrashed bool
}

// ListFolder renders one page of a folder's children, or a budgeted tree.
func (s *Service) ListFolder(ctx context.Context, in ListFolderInput) (string, error) {
	target := strings.TrimSpace(in.Folder)
	if target == "" {
		target = RootAlias
	}
	res, err := s.Resolve(ctx, target, ResolveOptions{FollowShortcut: true})
	if err != nil {
		return "", err
	}
	if !res.File.IsFolder() {
		return "", Errorf(ClassInvalid, "%s is %s, not a folder. get_file describes it.",
			res.File.Name, model.KindWithArticle(res.File))
	}
	kindClause, err := kindClause(in.Kind)
	if err != nil {
		return "", err
	}
	loc := s.Location(ctx, res.File)
	if in.Recursive {
		return s.tree(ctx, res.File, loc, in, kindClause)
	}
	return s.page(ctx, res.File, loc, in, kindClause)
}

// uniqueStrings keeps the first occurrence of each name, so a folder
// that both truncated and was left unentered is named once.
func uniqueStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := in[:0]
	for _, v := range in {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// folderLocation is where a folder's contents live: the folder's own
// location with its name added, unless it is the top of a drive, whose
// name the location already carries. Every result that names a folder
// goes through it, because a listing and a tree of the same folder that
// disagree about where it is are worse than either alone.
func (s *Service) folderLocation(ctx context.Context, folder *gdrive.File, loc model.Location) model.Location {
	if s.isDriveRoot(ctx, folder) {
		return loc
	}
	return loc.Child(folder.Name)
}

// isDriveRoot reports whether the folder is the top of My Drive or of a
// shared drive, whose name is the drive's own.
func (s *Service) isDriveRoot(ctx context.Context, f *gdrive.File) bool {
	if f.DriveID != "" {
		return f.ID == f.DriveID
	}
	return f.ID == RootAlias || f.ID == s.rootID(ctx)
}

func kindClause(kind string) (string, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" || kind == "any" {
		return "", nil
	}
	clause, ok := kindMimes[kind]
	if !ok {
		return "", Errorf(ClassInvalid, "kind %q is not one of %s", kind, strings.Join(Kinds(), ", "))
	}
	return clause, nil
}

// childQuery builds the query for one folder's direct children, with an
// optional extra clause (a kind filter, or the exact name a path walk is
// after). Drive cannot recurse in a query, which is why a tree costs a
// call per folder, and this is the only place the query is assembled.
func childQuery(parentID, extra string, includeTrashed bool) string {
	clauses := []string{quote(parentID) + " in parents"}
	if !includeTrashed {
		clauses = append(clauses, "trashed = false")
	}
	if extra != "" {
		clauses = append(clauses, extra)
	}
	return strings.Join(clauses, " and ")
}

func (s *Service) page(ctx context.Context, folder *gdrive.File, loc model.Location, in ListFolderInput, kindClause string) (string, error) {
	size := in.PageSize
	switch {
	case size <= 0:
		size = DefaultPageSize
	case size > MaxPageSize:
		size = MaxPageSize
	}
	list, err := s.api.ListFiles(ctx, gapi.ListQuery{
		Q: childQuery(folder.ID, kindClause, in.IncludeTrashed), PageSize: size, PageToken: in.PageToken,
		// Folders first, then natural name order: 2 sorts before 10.
		OrderBy:     "folder,name_natural",
		DriveID:     folder.DriveID,
		ResourceIDs: []string{folder.ID},
	})
	if err != nil {
		return "", wrap(err, "listing "+folder.Name)
	}
	// Children of one folder share its location exactly, so no per-file
	// parent lookup is needed here at all. A drive's own root contributes
	// no name of its own: it is already the head of the path.
	childLoc := s.folderLocation(ctx, folder, loc)
	files := make([]*model.File, 0, len(list.Files))
	for _, f := range list.Files {
		files = append(files, model.New(f, model.Options{Location: childLoc, SharedDriveName: loc.Drive}))
	}
	return render.Listing(files, render.ListingOptions{
		Title:            fmt.Sprintf("%s — %s, folders first", childLoc.String(), model.Plural(len(files), "item", "items")),
		Now:              s.now(),
		NextPageToken:    list.NextPageToken,
		IncompleteSearch: list.IncompleteSearch,
		Empty:            "this folder is empty.",
	}), nil
}

// tree walks breadth-first under a depth and item budget, and names the
// folders it did not enter: a tree that quietly stops is one a person
// will read as complete.
func (s *Service) tree(ctx context.Context, folder *gdrive.File, loc model.Location, in ListFolderInput, kindClause string) (string, error) {
	depth := in.MaxDepth
	switch {
	case depth <= 0:
		depth = DefaultMaxDepth
	case depth > MaxMaxDepth:
		depth = MaxMaxDepth
	}
	items := in.MaxItems
	switch {
	case items <= 0:
		items = DefaultMaxItems
	case items > MaxMaxItems:
		items = MaxMaxItems
	}

	// The tree's shape carries each node's position, so only the root
	// needs a written-out location.
	root := &render.TreeNode{File: model.New(folder, model.Options{
		Location: loc, SharedDriveName: loc.Drive, IsDriveRoot: s.isDriveRoot(ctx, folder)})}
	type queued struct {
		node  *render.TreeNode
		file  *gdrive.File
		level int
	}
	queue := []queued{{node: root, file: folder, level: 0}}
	shown := 0
	var skipped []string

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur.level >= depth {
			cur.node.NotEntered = fmt.Sprintf("depth limit %d", depth)
			skipped = append(skipped, cur.file.Name)
			continue
		}
		if shown >= items {
			cur.node.NotEntered = fmt.Sprintf("item limit %d", items)
			skipped = append(skipped, cur.file.Name)
			continue
		}
		children, more, err := s.childrenOf(ctx, cur.file, kindClause, in.IncludeTrashed, items-shown)
		if err != nil {
			return "", err
		}
		cur.node.Items = len(children)
		if more {
			// The budget cut this folder's own listing, so the count above
			// is what was shown, not what the folder holds.
			cur.node.Truncated = fmt.Sprintf("item limit %d reached; more items here", items)
			skipped = append(skipped, cur.file.Name)
		}
		for _, c := range children {
			shown++
			node := &render.TreeNode{File: model.New(c, model.Options{Location: loc, SharedDriveName: loc.Drive})}
			cur.node.Children = append(cur.node.Children, node)
			if c.IsFolder() {
				queue = append(queue, queued{node: node, file: c, level: cur.level + 1})
			}
		}
		if shown >= items && len(queue) > 0 {
			for _, q := range queue {
				q.node.NotEntered = fmt.Sprintf("item limit %d", items)
				skipped = append(skipped, q.file.Name)
			}
			queue = nil
		}
	}

	note := ""
	if len(skipped) > 0 {
		note = fmt.Sprintf("this walk stopped short in %s: %s. "+
			"List one of them directly, or raise max_depth or max_items.",
			model.Plural(len(skipped), "folder", "folders"), strings.Join(uniqueStrings(skipped), ", "))
	}
	return render.Tree(root, render.TreeOptions{
		// The folder being listed, not the folder it sits in: a tree
		// headed with its parent's path names something other than what
		// it shows.
		Title: fmt.Sprintf("%s — tree, depth %d, %s shown",
			s.folderLocation(ctx, folder, loc).String(), depth, model.Plural(shown, "item", "items")),
		Note: note,
	}), nil
}

// childrenOf pages through one folder, stopping at the remaining item
// budget.
func (s *Service) childrenOf(ctx context.Context, folder *gdrive.File, kindClause string, includeTrashed bool, budget int) (files []*gdrive.File, more bool, err error) {
	var out []*gdrive.File
	token := ""
	for budget > 0 {
		// files.list costs the same whatever the page size, so a walk
		// takes the largest page Drive allows rather than ten times as
		// many calls for the same items.
		size := min(budget, gapi.MaxPageSize)
		list, err := s.api.ListFiles(ctx, gapi.ListQuery{
			Q: childQuery(folder.ID, kindClause, includeTrashed), PageSize: size, PageToken: token,
			OrderBy: "folder,name_natural", DriveID: folder.DriveID, ResourceIDs: []string{folder.ID},
		})
		if err != nil {
			return nil, false, wrap(err, "listing "+folder.Name)
		}
		out = append(out, list.Files...)
		budget -= len(list.Files)
		if len(list.Files) == 0 {
			return out, false, nil
		}
		if list.NextPageToken == "" {
			return out, false, nil
		}
		token = list.NextPageToken
	}
	// The budget ran out with a page still waiting: this folder holds
	// more than was listed, and the caller has to say so.
	return out, true, nil
}
