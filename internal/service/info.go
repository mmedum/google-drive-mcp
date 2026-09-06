package service

import (
	"context"

	"github.com/mmedum/google-drive-mcp/internal/gapi"
	"github.com/mmedum/google-drive-mcp/internal/gdrive"
	"github.com/mmedum/google-drive-mcp/internal/model"
	"github.com/mmedum/google-drive-mcp/internal/render"
)

// GetFileInput selects a file to describe.
type GetFileInput struct {
	File string
	// IncludeLabels asks for the Workspace labels applied to the file.
	IncludeLabels bool
}

// GetFile renders the file card: what the thing is, where it sits, who
// can see it, and what this account may do with it. It is the cheapest
// useful call and the one to make first when handed an id or a URL.
func (s *Service) GetFile(ctx context.Context, in GetFileInput) (string, error) {
	res, err := s.Resolve(ctx, in.File, ResolveOptions{FollowShortcut: false, IncludeLabels: in.IncludeLabels && s.opts.Labels})
	if err != nil {
		return "", err
	}
	m := s.Model(ctx, res)
	return render.FileCard(m, render.FileCardOptions{Now: s.now(), FollowedShortcut: res.FollowedShortcut}), nil
}

// Model builds the model view of a resolved file, filling in the parts a
// single files.get does not carry: the folder path, the shared drive's
// name, the export formats, and, in a shared drive, the permission list.
func (s *Service) Model(ctx context.Context, res *Resolved) *model.File {
	f := res.File
	perms, known := s.permissionsFor(ctx, f)
	return model.New(f, model.Options{
		Location:         s.Location(ctx, f),
		Permissions:      perms,
		PermissionsKnown: known,
		ExportFormats:    model.ExportFormats(f),
		SharedDriveName:  res.DriveName,
		LabelDefinitions: s.labelNames(ctx, f),
	})
}

// labelNames reads the definitions of the labels on a file, so the card
// can name them and their fields instead of printing generated ids.
//
// It is best effort on purpose. The definitions are a separate API
// behind a separate scope, and a file card that failed because a label's
// title could not be read would be refusing to report the file over a
// decoration. Without them the labels still appear, by id.
func (s *Service) labelNames(ctx context.Context, f *gdrive.File) map[string]*model.LabelDefinition {
	if f == nil || f.LabelInfo == nil || len(f.LabelInfo.Labels) == 0 || !s.opts.Labels {
		return nil
	}
	defs, err := s.allLabelDefinitions(ctx)
	if err != nil {
		s.log.DebugContext(ctx, "label definitions unavailable", "class", gapi.Class(err))
		return nil
	}
	return defs
}

// permissionsFor returns the grants on a file and whether the list could
// be read at all. files.get carries the grants for a My Drive file; in a
// shared drive the list is separate. The second return matters: a file
// with no grants of its own and a file whose grants this account may not
// read look identical otherwise, and calling the second one "private"
// would understate exposure, which is the one direction that must never
// happen.
func (s *Service) permissionsFor(ctx context.Context, f *gdrive.File) ([]*gdrive.Permission, bool) {
	if f == nil {
		return nil, false
	}
	if f.DriveID == "" || len(f.Permissions) > 0 {
		return f.Permissions, true
	}
	perms, err := s.api.ListPermissions(ctx, f.ID)
	if err != nil {
		s.log.DebugContext(ctx, "permissions unavailable", "file", gapi.ShortID(f.ID), "class", gapi.Class(err))
		return nil, false
	}
	return perms, true
}
