package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mmedum/google-drive-mcp/internal/gapi"
	"github.com/mmedum/google-drive-mcp/internal/gdrive"
	"github.com/mmedum/google-drive-mcp/internal/model"
	"github.com/mmedum/google-drive-mcp/internal/render"
)

// SearchInput is a typed search. The fields build the Drive query, so
// the escaping rules and the prefix semantics of `contains` live here
// rather than in every caller.
type SearchInput struct {
	Name           string
	Text           string
	Kind           string
	MimeType       string
	InFolder       string
	Drive          string
	Scope          string
	Owner          string
	Starred        bool
	Trashed        bool
	ModifiedAfter  string
	ModifiedBefore string
	CreatedAfter   string
	OrderBy        string
	Limit          int
	PageToken      string
	RawQuery       string
}

// Scope values for a search.
const (
	ScopeAll          = "all"
	ScopeMyDrive      = "my_drive"
	ScopeSharedWithMe = "shared_with_me"
)

// kindMimes maps the kind names a tool accepts onto Drive query clauses.
// `office` is several types, so it becomes a parenthesised alternation.
var kindMimes = map[string]string{
	"folder":   "mimeType = " + quote(gdrive.MimeFolder),
	"shortcut": "mimeType = " + quote(gdrive.MimeShortcut),
	"doc":      "mimeType = " + quote(gdrive.MimeDocument),
	"sheet":    "mimeType = " + quote(gdrive.MimeSheet),
	"slides":   "mimeType = " + quote(gdrive.MimeSlides),
	"form":     "mimeType = " + quote(gdrive.MimeForm),
	"drawing":  "mimeType = " + quote(gdrive.MimeDrawing),
	"pdf":      "mimeType = 'application/pdf'",
	"image":    "mimeType contains 'image/'",
	"video":    "mimeType contains 'video/'",
	"audio":    "mimeType contains 'audio/'",
	"office": "(mimeType = 'application/vnd.openxmlformats-officedocument.wordprocessingml.document'" +
		" or mimeType = 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet'" +
		" or mimeType = 'application/vnd.openxmlformats-officedocument.presentationml.presentation'" +
		" or mimeType = 'application/msword'" +
		" or mimeType = 'application/vnd.ms-excel'" +
		" or mimeType = 'application/vnd.ms-powerpoint')",
}

// Kinds lists the accepted kind names, for tool descriptions and errors.
func Kinds() []string {
	return sortedKeys(kindMimes, "any")
}

// orderByKeys maps the order names a tool accepts onto Drive's own keys.
var orderByKeys = map[string]string{
	"modified": "modifiedTime desc",
	"created":  "createdTime desc",
	"name":     "name_natural",
	"recency":  "recency desc",
	"viewed":   "viewedByMeTime desc",
	"size":     "quotaBytesUsed desc",
}

// OrderBys lists the accepted order names.
func OrderBys() []string {
	return sortedKeys(orderByKeys)
}

// Default and maximum page sizes for a search.
const (
	DefaultSearchLimit = 25
	MaxSearchLimit     = 200
)

// Search runs a typed search and renders one page of hits.
func (s *Service) Search(ctx context.Context, in SearchInput) (string, error) {
	query, describe, err := s.buildQuery(ctx, &in)
	if err != nil {
		return "", err
	}
	limit := in.Limit
	switch {
	case limit <= 0:
		limit = DefaultSearchLimit
	case limit > MaxSearchLimit:
		limit = MaxSearchLimit
	}
	order, ok := orderByKeys[strings.ToLower(strings.TrimSpace(in.OrderBy))]
	if !ok {
		if in.OrderBy != "" {
			return "", Errorf(ClassInvalid, "order_by %q is not one of %s", in.OrderBy, strings.Join(OrderBys(), ", "))
		}
		order = orderByKeys["modified"]
	}

	lq := gapi.ListQuery{Q: query, PageSize: limit, PageToken: in.PageToken, OrderBy: order}
	if in.Drive != "" {
		d, err := s.findDrive(ctx, in.Drive)
		if err != nil {
			return "", err
		}
		lq.DriveID = d.ID
	}
	if strings.EqualFold(in.Scope, ScopeMyDrive) {
		lq.Corpora = gapi.CorporaUser
	}
	list, err := s.api.ListFiles(ctx, lq)
	if err != nil {
		return "", wrap(err, "searching Drive")
	}

	files := s.decorate(ctx, list.Files)
	empty := "no file matched. Note that `name` matches the beginnings of words, not any substring: " +
		"\"udget\" will not find \"Budget\". `text` matches whole words in the content. Widen the search or try search_files with fewer fields."
	return render.Listing(files, render.ListingOptions{
		Title:            fmt.Sprintf("%s — %s", describe, model.Plural(len(list.Files), "hit", "hits")),
		Now:              s.now(),
		ShowLocation:     true,
		NextPageToken:    list.NextPageToken,
		IncompleteSearch: list.IncompleteSearch,
		Empty:            empty,
	}), nil
}

// buildQuery turns the typed fields into a Drive query and a description
// of what was actually asked, so an empty result can be read against the
// question rather than guessed at.
func (s *Service) buildQuery(ctx context.Context, in *SearchInput) (query, describe string, err error) {
	var clauses, described []string

	if name := strings.TrimSpace(in.Name); name != "" {
		clauses = append(clauses, "name contains "+quote(name))
		described = append(described, fmt.Sprintf("name starting with %q", name))
	}
	if text := strings.TrimSpace(in.Text); text != "" {
		clauses = append(clauses, "fullText contains "+quote(text))
		described = append(described, fmt.Sprintf("containing the words %q", text))
	}
	clause, err := kindClause(in.Kind)
	if err != nil {
		return "", "", err
	}
	if clause != "" {
		clauses = append(clauses, clause)
		described = append(described, "kind "+strings.ToLower(strings.TrimSpace(in.Kind)))
	}
	if mime := strings.TrimSpace(in.MimeType); mime != "" {
		clauses = append(clauses, "mimeType = "+quote(mime))
		described = append(described, "mime type "+mime)
	}
	if folder := strings.TrimSpace(in.InFolder); folder != "" {
		res, err := s.Resolve(ctx, folder, ResolveOptions{FollowShortcut: true})
		if err != nil {
			return "", "", err
		}
		if !res.File.IsFolder() {
			return "", "", Errorf(ClassInvalid, "in_folder must name a folder; %q is %s", folder, model.KindWithArticle(res.File))
		}
		clauses = append(clauses, quote(res.File.ID)+" in parents")
		described = append(described, "directly inside "+res.File.Name)
	}
	if owner := strings.TrimSpace(in.Owner); owner != "" {
		clauses = append(clauses, quote(owner)+" in owners")
		described = append(described, "owned by "+owner)
	}
	if in.Starred {
		clauses = append(clauses, "starred = true")
		described = append(described, "starred")
	}
	// Trashed items are excluded unless asked for: a search that turned
	// up deleted files as if they were live would be a trap.
	if in.Trashed {
		clauses = append(clauses, "trashed = true")
		described = append(described, "in the trash")
	} else {
		clauses = append(clauses, "trashed = false")
	}
	switch scope := strings.ToLower(strings.TrimSpace(in.Scope)); scope {
	case "", ScopeAll, ScopeMyDrive:
	case ScopeSharedWithMe:
		clauses = append(clauses, "sharedWithMe = true")
		described = append(described, "shared with me")
	default:
		return "", "", Errorf(ClassInvalid, "scope %q is not one of %s, %s, %s", in.Scope, ScopeAll, ScopeMyDrive, ScopeSharedWithMe)
	}
	for _, t := range []struct {
		value, field, op, words string
	}{
		{in.ModifiedAfter, "modifiedTime", ">", "modified after"},
		{in.ModifiedBefore, "modifiedTime", "<", "modified before"},
		{in.CreatedAfter, "createdTime", ">", "created after"},
	} {
		if strings.TrimSpace(t.value) == "" {
			continue
		}
		stamp, err := parseSearchDate(t.value)
		if err != nil {
			return "", "", Errorf(ClassInvalid, "%s: %v", t.words, err)
		}
		clauses = append(clauses, fmt.Sprintf("%s %s %s", t.field, t.op, quote(stamp)))
		described = append(described, t.words+" "+stamp)
	}
	if raw := strings.TrimSpace(in.RawQuery); raw != "" {
		clauses = append(clauses, "("+raw+")")
		described = append(described, "raw query "+raw)
	}
	if in.Drive != "" {
		described = append(described, "in the shared drive "+in.Drive)
	}

	if len(described) == 0 {
		return "", "", Errorf(ClassInvalid, "a search needs at least one of name, text, kind, mime_type, in_folder, "+
			"owner, starred, trashed, modified_after, modified_before, created_after or raw_query. "+
			"list_folder lists a folder without a search.")
	}
	return strings.Join(clauses, " and "), "search: " + strings.Join(described, ", "), nil
}

// parseSearchDate accepts an RFC 3339 timestamp or a plain date and
// returns the RFC 3339 form Drive wants. A relative form is deliberately
// not accepted here: "7d" reads as the past in a search filter and as
// the future in an expiry, and one spelling meaning opposite things is
// worse than not having it.
func parseSearchDate(v string) (string, error) {
	if t, ok := parseInstant(v); ok {
		return t.UTC().Format(time.RFC3339), nil
	}
	return "", fmt.Errorf("%q is not a date; write 2026-03-04 or 2026-03-04T09:00:00Z", v)
}

// parseInstant reads the absolute date forms this server accepts
// wherever a caller writes a time. It is shared so that a date accepted
// by a search is a date accepted by an expiry.
func parseInstant(v string) (time.Time, bool) {
	v = strings.TrimSpace(v)
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, v); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

// decorate turns wire files into model files with their locations filled
// in. A page's hits come from all over, so each needs one; the parent
// lookups are budgeted and cached, and anything past the budget shows
// the parent id rather than costing another read.
func (s *Service) decorate(ctx context.Context, files []*gdrive.File) []*model.File {
	budget := maxParentLookups
	out := make([]*model.File, 0, len(files))
	for _, f := range files {
		out = append(out, model.New(f, model.Options{Location: s.locationWithBudget(ctx, f, &budget)}))
	}
	return out
}

// maxParentLookups caps the parent reads one page of results may cost.
// Hits on a page usually share folders, and a folder read once is free
// after that, so this is a ceiling rather than a per-file cost.
const maxParentLookups = 20

func (s *Service) cachedParent(id string) (*gdrive.File, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cacheGet(s.files, parentKey(id), s.now(), s.opts.PathTTL)
}
