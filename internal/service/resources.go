package service

import (
	"context"
	"fmt"

	"github.com/mmedum/google-drive-mcp/internal/gdrive"
	"github.com/mmedum/google-drive-mcp/internal/model"
	"github.com/mmedum/google-drive-mcp/internal/render"
)

// Resource is one gdrive:// resource's content and the media type it is
// in.
//
// A resource read differs from the matching tool call in three ways, and
// the third is the one worth stating: it carries a media type, it takes
// the whole output budget in one go because there is no model on the
// other end to ask for the next window, and it is the CONTENT ALONE.
// read_file lays its window out under a header saying which part of
// which file it is; a resource cannot, because a client told the bytes
// are text/csv may hand them to something that parses csv, and five
// lines of prose on top would make the media type a lie. The description
// a header would have carried is what gdrive://{file}/meta is for.
type Resource struct {
	Text     string
	MimeType string
	// Truncated marks content the budget cut short, with how much came
	// back and how much there is. It travels in the read's _meta rather
	// than in the text, for the same reason the header does not: a note
	// appended to a csv is no longer a csv.
	Truncated bool
	Bytes     int64
	Total     int64
}

// ResourceText is what gdrive://{file} returns: the file's text under
// one budget, in the form read_file would give it — markdown for a Doc,
// csv for a Sheet, the file's own type otherwise — and nothing else.
func (s *Service) ResourceText(ctx context.Context, reference string) (*Resource, error) {
	res, err := s.Resolve(ctx, reference, ResolveOptions{FollowShortcut: true})
	if err != nil {
		return nil, err
	}
	f := res.File
	if f.IsFolder() {
		return nil, Errorf(ClassInvalid, "%s is a folder; its listing is %s.", f.Name, ResourceURI(f.ID, "children"))
	}
	plan, err := s.readPlan(f, "")
	if err != nil {
		return nil, err
	}
	in := ReadFileInput{File: f.ID, MaxChars: render.MaxMaxChars}
	w, err := s.textWindow(ctx, res, plan, in, render.MaxMaxChars)
	if err != nil {
		return nil, err
	}
	return &Resource{
		Text: w.text, MimeType: textMimeOf(f, plan),
		Truncated: w.more, Bytes: w.used, Total: w.total,
	}, nil
}

// textMimeOf is the media type the text a read produced is in. For a
// Google-native document it is the export format; for a blob it is the
// file's own type, which is already a text type or read_file would have
// refused it.
func textMimeOf(f *gdrive.File, plan readPlan) string {
	if plan.exportMime != "" {
		return plan.exportMime
	}
	if mime := gdrive.MimeOnly(f.MimeType); mime != "" {
		return mime
	}
	return "text/plain"
}

// ResourceCard is what gdrive://{file}/meta returns: the same file card
// get_file renders.
func (s *Service) ResourceCard(ctx context.Context, reference string) (*Resource, error) {
	text, err := s.GetFile(ctx, GetFileInput{File: reference})
	if err != nil {
		return nil, err
	}
	return &Resource{Text: text, MimeType: "text/plain"}, nil
}

// ResourceChildren is what gdrive://{folder}/children returns: the first
// page of a folder's listing. It is one page and never a tree: a
// resource has no arguments to bound a walk with, and a resource read
// that costs a hundred listings is one nobody can predict the price of.
func (s *Service) ResourceChildren(ctx context.Context, reference string) (*Resource, error) {
	res, err := s.Resolve(ctx, reference, ResolveOptions{FollowShortcut: true})
	if err != nil {
		return nil, err
	}
	if !res.File.IsFolder() {
		return nil, Errorf(ClassInvalid, "%s is %s, not a folder; its text is %s.",
			res.File.Name, model.KindWithArticle(res.File), ResourceURI(res.File.ID, ""))
	}
	text, err := s.ListFolder(ctx, ListFolderInput{Folder: res.File.ID})
	if err != nil {
		return nil, err
	}
	return &Resource{Text: text, MimeType: "text/plain"}, nil
}

// ResourceURI is the gdrive:// URI for a file id, which is what a
// resource link in a tool result would carry.
func ResourceURI(id, suffix string) string {
	if suffix == "" {
		return "gdrive://" + id
	}
	return fmt.Sprintf("gdrive://%s/%s", id, suffix)
}
