package service

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mmedum/google-drive-mcp/v2/internal/gapi"
	"github.com/mmedum/google-drive-mcp/v2/internal/gdrive"
	"github.com/mmedum/google-drive-mcp/v2/internal/mediatype"
	"github.com/mmedum/google-drive-mcp/v2/internal/model"
	"github.com/mmedum/google-drive-mcp/v2/internal/render"
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
	// Property matches a custom file property. "key" alone finds every
	// file carrying that key whatever its value; "key=value" matches
	// both. Properties are the key-value pairs update_file sets, which
	// is how one app tags files for another to find.
	Property string
	// Visibility is who can open the file without being named on it:
	// anyone, link, domain or limited.
	Visibility string
	// SharedWith is an address of a person or group the file is shared
	// with, as a viewer, commenter or editor.
	SharedWith string
	Limit      int
	PageToken  string
	RawQuery   string
}

// visibilities maps the visibility a tool accepts onto Drive's own
// values, and says it in words. anyone covers both of Drive's values for
// the whole internet, by link or by search, because a person asking what
// is public means both; link is the one Drive's sharing dialog calls
// "Anyone with the link".
var visibilities = map[string]struct {
	values []string
	words  string
}{
	"anyone":  {[]string{"anyoneCanFind", "anyoneWithLink"}, "open to anyone on the internet"},
	"link":    {[]string{"anyoneWithLink"}, "open to anyone with the link"},
	"domain":  {[]string{"domainCanFind", "domainWithLink"}, "open to everyone in the organization"},
	"limited": {[]string{"limited"}, "open only to the people and groups it is shared with"},
}

// Visibilities lists the accepted visibility names.
func Visibilities() []string {
	return sortedKeys(visibilities)
}

// Scope values for a search.
const (
	ScopeAll          = "all"
	ScopeMyDrive      = "my_drive"
	ScopeSharedWithMe = "shared_with_me"
)

// kindClause turns a kind filter into the Drive query clause that
// selects it; the empty filter and "any" select everything. The types come from internal/mediatype, so a kind is added
// there and every layer — the filter, the display name, the export
// format — learns about it at once.
//
// Two shapes of filter: one that names its media types, and one Drive
// matches by prefix because the list is open-ended. A group that somehow
// had neither would silently match everything, so it is refused.
func kindClause(kind string) (string, error) {
	group := strings.ToLower(strings.TrimSpace(kind))
	if group == "" || group == "any" {
		return "", nil
	}
	if prefix := mediatype.GroupPrefix(group); prefix != "" {
		return "mimeType contains " + quote(prefix), nil
	}
	mimes := mediatype.GroupMimes(group)
	switch len(mimes) {
	case 0:
		return "", Errorf(ClassInvalid, "kind %q is not one of %s", group, strings.Join(Kinds(), ", "))
	case 1:
		return "mimeType = " + quote(mimes[0]), nil
	}
	clauses := make([]string, 0, len(mimes))
	for _, mime := range mimes {
		clauses = append(clauses, "mimeType = "+quote(mime))
	}
	return "(" + strings.Join(clauses, " or ") + ")", nil
}

// Kinds lists the accepted kind names, for tool descriptions and errors.
// "any" is not a group of media types, it is the absence of a filter, so
// it is added here rather than sitting in the registry as a kind with no
// types in it.
func Kinds() []string {
	out := append(mediatype.Groups(), "any")
	slices.Sort(out)
	return out
}

// orderByKeys maps the order names a tool accepts onto Drive's own keys.
var orderByKeys = map[string]string{
	"modified": "modifiedTime desc",
	"created":  "createdTime desc",
	"name":     "name_natural",
	"recency":  "recency desc",
	"viewed":   "viewedByMeTime desc",
	"size":     "quotaBytesUsed desc",
	// Only a file shared with this account has the time, so this order
	// limits a search to those files.
	OrderShared: "sharedWithMeTime desc",
}

// OrderShared is the order that puts the files most recently shared
// with this account first.
const OrderShared = "shared"

// defaultOrder is the order a search takes when none is named.
const defaultOrder = "modified"

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
	orderName := strings.ToLower(strings.TrimSpace(in.OrderBy))
	if orderName == "" {
		orderName = defaultOrder
	}
	order, ok := orderByKeys[orderName]
	if !ok {
		return "", Errorf(ClassInvalid, "order_by %q is not one of %s", in.OrderBy, strings.Join(OrderBys(), ", "))
	}
	query, describe, err := s.buildQuery(ctx, &in, orderName)
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
// question rather than guessed at. orderName is the order the search
// uses, which the description names when nothing else narrows it.
func (s *Service) buildQuery(ctx context.Context, in *SearchInput, orderName string) (query, describe string, err error) {
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
	access, accessWords, err := accessClauses(in)
	if err != nil {
		return "", "", err
	}
	clauses = append(clauses, access...)
	described = append(described, accessWords...)
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
	scopeQuery, scopeWords, err := scopeClause(in.Scope, orderName)
	if err != nil {
		return "", "", err
	}
	if scopeQuery != "" {
		clauses = append(clauses, scopeQuery)
	}
	if scopeWords != "" {
		described = append(described, scopeWords)
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
	if property := strings.TrimSpace(in.Property); property != "" {
		clause, words, err := propertyClause(property)
		if err != nil {
			return "", "", err
		}
		clauses = append(clauses, clause)
		described = append(described, words)
	}
	if raw := strings.TrimSpace(in.RawQuery); raw != "" {
		clauses = append(clauses, "("+raw+")")
		described = append(described, "raw query "+raw)
	}
	if in.Drive != "" {
		described = append(described, "in the shared drive "+in.Drive)
	}

	if len(described) == 0 {
		// Nothing narrows it, so it is every live file this account can
		// see, and the order is all that decides which come first.
		described = append(described, "everything you can see, by "+orderName)
	}
	return strings.Join(clauses, " and "), "search: " + strings.Join(described, ", "), nil
}

// accessClauses are the clauses about who can reach a file: its owner,
// its visibility and an address it is shared with.
func accessClauses(in *SearchInput) (clauses, described []string, err error) {
	if owner := strings.TrimSpace(in.Owner); owner != "" {
		clauses = append(clauses, quote(owner)+" in owners")
		described = append(described, "owned by "+owner)
	}
	if v := strings.TrimSpace(in.Visibility); v != "" {
		clause, words, err := visibilityClause(v)
		if err != nil {
			return nil, nil, err
		}
		clauses = append(clauses, clause)
		described = append(described, words)
	}
	if who := strings.TrimSpace(in.SharedWith); who != "" {
		clause, err := sharedWithClause(who)
		if err != nil {
			return nil, nil, err
		}
		clauses = append(clauses, clause)
		described = append(described, "shared with "+who)
	}
	return clauses, described, nil
}

// visibilityClause is the query clause for one visibility name and the
// words that describe it.
func visibilityClause(v string) (clause, words string, err error) {
	vis, ok := visibilities[strings.ToLower(v)]
	if !ok {
		return "", "", Errorf(ClassInvalid, "visibility %q is not one of %s", v, strings.Join(Visibilities(), ", "))
	}
	terms := make([]string, 0, len(vis.values))
	for _, value := range vis.values {
		terms = append(terms, "visibility = "+quote(value))
	}
	if len(terms) == 1 {
		return terms[0], vis.words, nil
	}
	return "(" + strings.Join(terms, " or ") + ")", vis.words, nil
}

// sharedWithClause finds the files an address can open. It asks readers
// and writers both: the reference does not say whether readers includes
// the people who can edit, and asking both is right either way.
func sharedWithClause(who string) (string, error) {
	if !strings.Contains(who, "@") || strings.ContainsAny(who, " \t") {
		return "", Errorf(ClassInvalid, "shared_with takes one address of a person or group, like "+
			"someone@example.com, not %q", who)
	}
	return "(" + quote(who) + " in readers or " + quote(who) + " in writers)", nil
}

// scopeClause is the query clause a scope adds and the words that
// describe it. order_by shared means something only for the files shared
// with this account, so with no scope it implies that one and says so.
func scopeClause(scope, orderName string) (clause, words string, err error) {
	scope = strings.ToLower(strings.TrimSpace(scope))
	sharedWords := "shared with me"
	if orderName == OrderShared {
		switch scope {
		case "":
			scope, sharedWords = ScopeSharedWithMe, "shared with me (order_by shared implies it)"
		case ScopeSharedWithMe:
		default:
			return "", "", Errorf(ClassInvalid, "order_by shared orders the files shared with you by when they were "+
				"shared, so it takes scope shared_with_me or no scope, not %q", scope)
		}
	}
	switch scope {
	case "", ScopeAll:
		return "", "", nil
	case ScopeMyDrive:
		return "", "scope my_drive", nil
	case ScopeSharedWithMe:
		return "sharedWithMe = true", sharedWords, nil
	}
	return "", "", Errorf(ClassInvalid, "scope %q is not one of %s, %s, %s", scope, ScopeAll, ScopeMyDrive, ScopeSharedWithMe)
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

// propertyClause builds the collection-matching clause for a custom file
// property. Drive's form is
//
//	properties has { key='mass' and value='1.3kg' }
//
// The braces are Drive's syntax and not a quoted value, so the key and
// the value are quoted individually and the clause is assembled here
// rather than passed through: a caller writing the whole expression
// would be writing a raw query, and raw_query already exists for that.
//
// BOTH halves are required, which is not what Google's search guide
// says. It gives "properties has { key='department' }" as an example of
// finding every file carrying a key whatever its value — and Drive
// answers that 400 `invalid` "Invalid Value". Phase 4 shipped the guide's
// form, and the live run refused it; a probe then tried it twice in each
// of three spellings, and against appProperties, and every one was
// refused. The guide is wrong, so this refuses the shape locally rather
// than spending a round trip to be told the same thing with less
// explanation.
func propertyClause(property string) (clause, words string, err error) {
	key, value, hasValue := strings.Cut(property, "=")
	key, value = strings.TrimSpace(key), strings.TrimSpace(value)
	switch {
	case key == "":
		return "", "", Errorf(ClassInvalid,
			"property needs a key and a value, as \"key=value\"")
	case !hasValue || value == "":
		return "", "", Errorf(ClassInvalid,
			"property needs a value as well as a key, as \"%s=something\". Drive has no way to search for "+
				"a key whatever its value: its own guide gives that form as an example and the API "+
				"refuses it. If you do not know the value, search another way and read the properties "+
				"off the file cards", key)
	}
	return "properties has { key=" + quote(key) + " and value=" + quote(value) + " }",
		fmt.Sprintf("with the property %s=%s", key, value), nil
}
