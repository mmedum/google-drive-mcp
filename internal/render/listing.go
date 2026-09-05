package render

import (
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/mmedum/google-drive-mcp/internal/model"
)

// ListingOptions tune a listing.
type ListingOptions struct {
	// Title heads the listing, e.g. the folder path or the query.
	Title string
	// Now anchors relative times.
	Now time.Time
	// ShowLocation adds a location column, which a search needs because
	// its hits come from all over and a name alone does not identify one.
	ShowLocation bool
	// NextPageToken, when set, is offered as the way to see more.
	NextPageToken string
	// IncompleteSearch passes on Drive's own warning that it could not
	// search every corpus.
	IncompleteSearch bool
	// Note is a closing line, e.g. where a budget stopped a walk.
	Note string
	// Empty is what to say when there is nothing, so each caller can
	// explain what would have matched.
	Empty string
}

// Listing renders one page of files as aligned columns.
func Listing(files []*model.File, o ListingOptions) string {
	var b buf
	if o.Title != "" {
		b.line(o.Title)
	}
	if len(files) == 0 {
		if o.Empty != "" {
			b.line(o.Empty)
		} else {
			b.line("(nothing here)")
		}
		// An empty page is not the end of the story: Drive can return one
		// with a continuation token, and a search it could not complete
		// is most misleading precisely when it matched nothing.
		writeListingFooter(&b, o)
		return b.String()
	}

	var table strings.Builder
	// Two spaces of padding keeps the columns apart without drawing rules.
	w := tabwriter.NewWriter(&table, 0, 0, 2, ' ', 0)
	for _, f := range files {
		cells := []string{f.Kind, f.Name, f.ID}
		if o.ShowLocation {
			cells = append(cells, f.Location.String())
		}
		cells = append(cells, listingWhen(f, o.Now))
		if flags := listingFlags(f); flags != "" {
			cells = append(cells, flags)
		}
		_, _ = fmt.Fprintln(w, strings.Join(cells, "\t"))
	}
	_ = w.Flush()
	b.sb.WriteString(table.String())

	writeListingFooter(&b, o)
	return b.String()
}

// writeListingFooter emits what the caller has to see whether or not the
// page had rows in it.
func writeListingFooter(b *buf, o ListingOptions) {
	if o.Note != "" {
		b.line(o.Note)
	}
	if o.IncompleteSearch {
		b.line("Drive reports the search was incomplete: it could not reach every shared drive in time, " +
			"so there may be matches it did not return. Narrow it with drive: or in_folder, or run it again.")
	}
	if o.NextPageToken != "" {
		b.linef("more results: call again with page_token %q", o.NextPageToken)
	}
}

func listingWhen(f *model.File, now time.Time) string {
	if f.Modified.IsZero() {
		return ""
	}
	out := "modified " + model.HumanTime(f.Modified, now)
	if f.ModifiedBy != "" {
		out += " by " + f.ModifiedBy
	}
	return out
}

// listingFlags marks the states worth seeing at a glance in a list.
func listingFlags(f *model.File) string {
	var flags []string
	if f.Starred {
		flags = append(flags, "starred")
	}
	if f.Trashed {
		flags = append(flags, "trashed")
	}
	if f.Sharing.Public() {
		flags = append(flags, "link-shared")
	} else if f.Sharing.Shared {
		flags = append(flags, "shared")
	}
	return strings.Join(flags, ", ")
}

// TreeNode is one folder in a recursive listing.
type TreeNode struct {
	File *model.File
	// Children are the items directly inside, folders first.
	Children []*TreeNode
	// Items is how many children the folder holds in Drive, which can be
	// more than were listed.
	Items int
	// NotEntered explains why a folder's contents are missing: a depth
	// or item budget stopped the walk.
	NotEntered string
	// Truncated explains that the folder's own listing was cut short, so
	// the children shown are fewer than it holds.
	Truncated string
}

// TreeOptions tune a tree.
type TreeOptions struct {
	Title string
	// Note carries where a budget stopped the walk.
	Note string
}

// Tree renders a folder walk. A walk that stopped says so, and names the
// folders it did not enter, because a tree that silently ends is a tree
// a person will trust wrongly.
func Tree(root *TreeNode, o TreeOptions) string {
	var b buf
	if o.Title != "" {
		b.line(o.Title)
	}
	if root == nil {
		b.line("(nothing here)")
		return b.String()
	}
	b.line(treeLabel(root, true))
	writeTreeChildren(&b, root, "")
	if o.Note != "" {
		b.line(o.Note)
	}
	return b.String()
}

func writeTreeChildren(b *buf, n *TreeNode, prefix string) {
	for i, c := range n.Children {
		last := i == len(n.Children)-1
		branch, next := "├── ", "│   "
		if last {
			branch, next = "└── ", "    "
		}
		b.line(prefix + branch + treeLabel(c, false))
		writeTreeChildren(b, c, prefix+next)
	}
}

func treeLabel(n *TreeNode, root bool) string {
	f := n.File
	name := f.Name
	if root {
		// The root shows its full path, built the same way the header
		// above it is: a location with a name added, not a rendered
		// location with a name glued on. A drive's own root is already
		// named by its location, so it is not appended twice; testing the
		// string for a matching suffix would drop a level from a folder
		// called Projects inside a folder called Projects.
		if f.IsDriveRoot {
			name = f.Location.String()
		} else {
			name = f.Location.Child(f.Name).String()
		}
	}
	if f.IsFolder {
		name += "/"
	}
	var extras []string
	if f.IsFolder {
		// When the listing was cut short the count is what was shown, not
		// what the folder holds, and the label has to say which.
		if n.Truncated != "" {
			extras = append(extras, model.Plural(n.Items, "item shown", "items shown"))
		} else {
			extras = append(extras, model.Plural(n.Items, "item", "items"))
		}
	} else {
		extras = append(extras, f.Kind)
		if f.HasSize {
			extras = append(extras, model.HumanSize(f.Size))
		}
	}
	if n.NotEntered != "" {
		extras = append(extras, "not entered: "+n.NotEntered)
	}
	if n.Truncated != "" {
		extras = append(extras, "listing cut short: "+n.Truncated)
	}
	return fmt.Sprintf("%s  [%s]  (%s)", name, f.ID, strings.Join(extras, ", "))
}
