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

// activityActions are the action words list_activity accepts, mapped to
// the API's own filter names. The API filters on
// `detail.action_detail_case`, whose values are SCREAMING_SNAKE; the
// words offered here are the ones the rest of this surface uses.
var activityActions = map[string]string{
	"create":     "CREATE",
	"edit":       "EDIT",
	"rename":     "RENAME",
	"move":       "MOVE",
	"delete":     "DELETE",
	"restore":    "RESTORE",
	"permission": "PERMISSION_CHANGE",
	"comment":    "COMMENT",
	"label":      "APPLIED_LABEL_CHANGE",
}

// ActivityActions lists the action filters list_activity accepts.
func ActivityActions() []string { return sortedKeys(activityActions) }

// ListActivityInput selects what activity to report.
type ListActivityInput struct {
	// File is the item to report on. A folder reports only what happened
	// to the folder itself unless Recursive is set.
	File string
	// Recursive reports the folder and everything under it, which is the
	// API's ancestorName rather than its itemName.
	Recursive bool
	// Actions filters to these kinds of action.
	Actions []string
	// Since limits the window, as a date or an RFC 3339 instant.
	Since     string
	PageSize  int
	PageToken string
}

// ListActivity reports what happened to a file or a folder.
func (s *Service) ListActivity(ctx context.Context, in ListActivityInput) (string, error) {
	if !s.opts.Activity {
		return "", Errorf(ClassUnsupported,
			"activity is off: this server was started without GDRIVE_ACTIVITY=true, which is also what "+
				"adds the Drive Activity scope at login")
	}
	res, err := s.Resolve(ctx, in.File, ResolveOptions{FollowShortcut: true})
	if err != nil {
		return "", err
	}
	f := res.File
	if in.Recursive && !f.IsFolder() {
		return "", Errorf(ClassInvalid,
			"recursive reports a folder and everything under it, and %s is %s",
			f.Name, model.KindWithArticle(f))
	}
	size := in.PageSize
	if size > gapi.MaxActivityPageSize {
		return "", Errorf(ClassInvalid, "page_size %d is above this server's ceiling of %d for activity",
			size, gapi.MaxActivityPageSize)
	}
	filter, err := s.activityFilter(in)
	if err != nil {
		return "", err
	}

	q := gdrive.ActivityQuery{PageSize: size, PageToken: in.PageToken, Filter: filter}
	// itemName and ancestorName are alternatives, and a folder answers
	// almost nothing through itemName: what happens INSIDE a folder is
	// activity on the children, not on the folder.
	if in.Recursive {
		q.AncestorName = "items/" + f.ID
	} else {
		q.ItemName = "items/" + f.ID
	}
	page, err := s.api.QueryActivity(ctx, q)
	if err != nil {
		return "", s.activityError(err, f)
	}

	events := make([]*model.Activity, 0, len(page.Activities))
	dropped := 0
	for _, a := range page.Activities {
		if converted := model.NewActivity(a); converted != nil {
			events = append(events, converted)
			continue
		}
		dropped++
	}
	note := ""
	if dropped > 0 {
		// A kind of action this server has no words for. Saying so beats
		// a count that silently disagrees with the page.
		note = fmt.Sprintf("%s of a kind this server has no words for and %s left out.",
			model.Plural(dropped, "One entry on this page is", "Entries on this page are"),
			map[bool]string{true: "is", false: "are"}[dropped == 1])
	}
	return render.Activity(events, render.ActivityOptions{
		Subject:       activitySubject(f, len(events), in.Recursive),
		Location:      s.Location(ctx, f).String(),
		Now:           s.now(),
		NextPageToken: page.NextPageToken,
		Note:          note,
	}), nil
}

// activitySubject heads the listing with what was actually asked about,
// because a folder asked about two ways gives two very different answers.
func activitySubject(f *gdrive.File, count int, recursive bool) string {
	head := fmt.Sprintf("%s — %s: %s", f.Name, model.Kind(f), model.Plural(count, "1 event", "events"))
	if recursive {
		return head + ", in this folder and everything under it"
	}
	if f.IsFolder() {
		return head + ", on the folder itself. Pass recursive to see what happened inside it."
	}
	return head
}

// activityFilter builds the API's own filter expression. Its language is
// not Drive's query language, and the two are not interchangeable: this
// one joins with AND, compares `time` against an RFC 3339 instant, and
// matches action kinds with a `has` operator over a parenthesised list.
func (s *Service) activityFilter(in ListActivityInput) (string, error) {
	var clauses []string
	if since := strings.TrimSpace(in.Since); since != "" {
		stamp, err := parseSearchDate(since)
		if err != nil {
			return "", Errorf(ClassInvalid, "since: %v", err)
		}
		clauses = append(clauses, `time >= "`+stamp+`"`)
	}
	if len(in.Actions) > 0 {
		names := make([]string, 0, len(in.Actions))
		for _, a := range in.Actions {
			word := strings.ToLower(strings.TrimSpace(a))
			if word == "" {
				continue
			}
			name, ok := activityActions[word]
			if !ok {
				return "", Errorf(ClassInvalid, "actions: %q is not one of %s",
					a, strings.Join(ActivityActions(), ", "))
			}
			names = append(names, name)
		}
		if len(names) == 1 {
			clauses = append(clauses, "detail.action_detail_case:"+names[0])
		} else if len(names) > 1 {
			clauses = append(clauses, "detail.action_detail_case:("+strings.Join(names, " ")+")")
		}
	}
	return strings.Join(clauses, " AND "), nil
}

// activityError names the setup step behind the refusal every deployer
// meets first, the same way the labels one does. The Drive Activity API
// is a separate API with a separate scope and a separate enablement.
func (s *Service) activityError(err error, f *gdrive.File) error {
	if class := gapi.Class(err); class == ClassAuth || class == ClassForbidden {
		return &Error{Class: ClassForbidden, Message: fmt.Sprintf(
			"reading the activity on %s failed because the Drive Activity API refused the token. It is a "+
				"separate API from Drive: enable the Drive Activity API in the Cloud project, add the "+
				"activity scope to the consent screen, and run `google-drive-mcp login` again with "+
				"GDRIVE_ACTIVITY=true. Google said: %s", f.Name, gapi.Message(err)), Err: err}
	}
	return wrap(err, "reading the activity on "+f.Name)
}
