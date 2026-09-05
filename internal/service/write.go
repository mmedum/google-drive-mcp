package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mmedum/google-drive-mcp/internal/gapi"
	"github.com/mmedum/google-drive-mcp/internal/gdrive"
	"github.com/mmedum/google-drive-mcp/internal/model"
	"github.com/mmedum/google-drive-mcp/internal/render"
)

// Result is what a write tool returns: the text a person reads, and the
// same facts in a structured form. Both are sent, because a client may
// show the model either one.
type Result struct {
	Text string
	JSON *render.WriteJSON
}

// newKinds are the empty Workspace files create_file can make.
var newKinds = map[string]string{
	"doc":     gdrive.MimeDocument,
	"sheet":   gdrive.MimeSheet,
	"slides":  gdrive.MimeSlides,
	"drawing": gdrive.MimeDrawing,
	"form":    gdrive.MimeForm,
}

// NewKinds lists the kinds create_file accepts, for tool descriptions
// and errors.
func NewKinds() []string { return sortedKeys(newKinds) }

// convertKinds are the Google formats an import can convert to.
var convertKinds = map[string]string{
	"doc":     gdrive.MimeDocument,
	"sheet":   gdrive.MimeSheet,
	"slides":  gdrive.MimeSlides,
	"drawing": gdrive.MimeDrawing,
}

// ConvertKinds lists the conversion targets, for tool descriptions.
func ConvertKinds() []string { return sortedKeys(convertKinds) }

// CreateFileInput describes a file to create.
type CreateFileInput struct {
	Name   string
	Parent string
	// Kind makes an empty Workspace file: doc, sheet, slides, drawing, form.
	Kind string
	// Content is inline text, for a small file written from the model's
	// own output.
	Content   string
	MimeType  string
	ConvertTo string

	Description string
	Starred     bool
	// AllowDuplicate permits a second file of the same name in one
	// folder, which Drive allows and this server does not by default.
	AllowDuplicate bool
}

// CreateFile makes an empty Workspace document or a small file from
// inline text. Anything already on disk goes through upload_file, and
// anything above five megabytes has to.
func (s *Service) CreateFile(ctx context.Context, in CreateFileInput) (*Result, error) {
	if err := s.writable("create_file"); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, Errorf(ClassInvalid, "name is required")
	}
	kind := strings.ToLower(strings.TrimSpace(in.Kind))
	hasContent := in.Content != ""
	if kind == "" && !hasContent {
		return nil, Errorf(ClassInvalid, "create_file needs either kind (%s) for an empty Google file, "+
			"or content for a small text file. upload_file sends one that is already on disk.",
			strings.Join(NewKinds(), ", "))
	}
	if kind != "" && hasContent {
		return nil, Errorf(ClassInvalid, "kind makes an empty Google file and content makes a text file; "+
			"pass one or the other. To turn text into a Google Doc, pass content with convert_to: doc.")
	}
	if len(in.Content) > gapi.MaxMultipartUpload {
		return nil, Errorf(ClassInvalid, "%s of inline content is more than the %s create_file takes. "+
			"Write it to %s and use upload_file, which sends a large file in chunks.",
			model.HumanSize(int64(len(in.Content))), model.HumanSize(gapi.MaxMultipartUpload), s.opts.LocalDir)
	}

	parent, err := s.parentFolder(ctx, in.Parent)
	if err != nil {
		return nil, err
	}
	if err := s.refuseDuplicate(ctx, parent, name, in.AllowDuplicate); err != nil {
		return nil, err
	}

	meta := &gdrive.FileMeta{Name: name, Parents: []string{parent.ID}}
	if in.Description != "" {
		meta.Description = gdrive.String(in.Description)
	}
	if in.Starred {
		meta.Starred = gdrive.Bool(true)
	}

	var f *gdrive.File
	if kind != "" {
		target, ok := newKinds[kind]
		if !ok {
			return nil, Errorf(ClassInvalid, "kind %q is not one of %s", in.Kind, strings.Join(NewKinds(), ", "))
		}
		meta.MimeType = target
		if err := s.assignID(ctx, meta); err != nil {
			return nil, err
		}
		f, err = s.api.CreateFile(ctx, meta, gapi.WriteOptions{ResourceIDs: []string{parent.ID}})
	} else {
		contentType := strings.TrimSpace(in.MimeType)
		if contentType == "" {
			contentType = "text/plain"
		}
		if meta.MimeType, err = convertTarget(in.ConvertTo); err != nil {
			return nil, err
		}
		if err := s.assignID(ctx, meta); err != nil {
			return nil, err
		}
		f, err = s.api.UploadMultipart(ctx, gapi.UploadRequest{
			Meta: meta, ContentType: contentType,
		}, []byte(in.Content))
	}
	if err != nil {
		return nil, wrap(err, fmt.Sprintf("creating %q in %s", name, parent.Name))
	}
	return s.write(ctx, f, outcome{Action: render.ActionCreated})
}

// UploadFileInput describes a local file to send to Drive.
type UploadFileInput struct {
	LocalPath string
	Name      string
	Parent    string
	MimeType  string
	ConvertTo string
	// OCRLanguage hints the language of the text in a scan being
	// converted to a document.
	OCRLanguage    string
	Description    string
	AllowDuplicate bool
}

// UploadFile sends a file from GDRIVE_LOCAL_DIR to Drive: one request up
// to five megabytes, and above that a resumable upload in chunks that
// survives a dropped connection.
func (s *Service) UploadFile(ctx context.Context, in UploadFileInput) (*Result, error) {
	if err := s.writable("upload_file"); err != nil {
		return nil, err
	}
	file, info, err := s.openLocal(in.LocalPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = filepath.Base(file.Name())
	}
	parent, err := s.parentFolder(ctx, in.Parent)
	if err != nil {
		return nil, err
	}
	if err := s.refuseDuplicate(ctx, parent, name, in.AllowDuplicate); err != nil {
		return nil, err
	}

	contentType := strings.TrimSpace(in.MimeType)
	if contentType == "" {
		contentType = sniffType(file.Name(), file)
	}
	meta := &gdrive.FileMeta{Name: name, Parents: []string{parent.ID}}
	if in.Description != "" {
		meta.Description = gdrive.String(in.Description)
	}
	if meta.MimeType, err = convertTarget(in.ConvertTo); err != nil {
		return nil, err
	}
	if err := s.assignID(ctx, meta); err != nil {
		return nil, err
	}

	req := gapi.UploadRequest{
		WriteOptions: gapi.WriteOptions{OCRLanguage: in.OCRLanguage},
		Meta:         meta, ContentType: contentType,
	}
	uploaded, err := s.send(ctx, req, file, info.Size())
	if err != nil {
		return nil, wrap(err, fmt.Sprintf("uploading %q to %s", name, parent.Name))
	}
	return s.write(ctx, uploaded, outcome{Action: render.ActionUploaded})
}

// send picks the upload path by size: one request for a small file, and
// chunks with recovery for a large one. It takes the open file rather
// than a reader because a file that grew since it was measured has to be
// started again from the beginning, with the size it has now.
func (s *Service) send(ctx context.Context, req gapi.UploadRequest, file *os.File, size int64) (*gdrive.File, error) {
	if size <= gapi.MaxMultipartUpload {
		// One byte past the limit, so a file that grew past it is
		// noticed; the size is known, so the buffer is not grown into.
		data := make([]byte, size+1)
		n, err := io.ReadFull(file, data)
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, Errorf(ClassInvalid, "cannot read the file to upload: %s", err)
		}
		data = data[:n]
		if int64(len(data)) <= gapi.MaxMultipartUpload {
			return s.api.UploadMultipart(ctx, req, data)
		}
		// It grew between the stat and the read, so it is a large file
		// after all. Measure it again and send it from the top: a
		// resumable upload has to declare a total, and declaring the
		// stale one would upload a truncated file and call it complete.
		info, err := file.Stat()
		if err != nil {
			return nil, Errorf(ClassInvalid, "cannot measure the file to upload: %s", err)
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return nil, Errorf(ClassInvalid, "cannot rewind the file to upload: %s", err)
		}
		size = info.Size()
	}
	return s.api.UploadResumable(ctx, req, file, size, gapi.ResumableOptions{
		Progress: func(sent, total int64) {
			s.log.DebugContext(ctx, "upload progress", "sent", sent, "total", total)
		},
	})
}

// UpdateContentInput replaces a file's bytes.
type UpdateContentInput struct {
	File      string
	Content   string
	LocalPath string
	MimeType  string
	// KeepPreviousRevision pins the version being replaced, which Drive
	// would otherwise discard after thirty days.
	KeepPreviousRevision bool
	// ExpectHeadRevision refuses the write when the file has moved on.
	// Drive v3 has no preconditions, so this is a check, not a lock.
	ExpectHeadRevision string
}

// UpdateContent replaces what is in a file, keeping its id, its place
// and everything that points at it. A Google Doc's content is not bytes
// and is refused.
func (s *Service) UpdateContent(ctx context.Context, in UpdateContentInput) (*Result, error) {
	if err := s.writable("update_content"); err != nil {
		return nil, err
	}
	localPath := strings.TrimSpace(in.LocalPath)
	if (in.Content == "") == (localPath == "") {
		return nil, Errorf(ClassInvalid, "pass either content (inline text) or local_path (a file in %s)", s.opts.LocalDir)
	}
	res, err := s.Resolve(ctx, in.File, ResolveOptions{FollowShortcut: false, Fresh: true})
	if err != nil {
		return nil, err
	}
	f := res.File
	switch {
	case f.IsFolder():
		return nil, Errorf(ClassInvalid, "%s is a folder; it has no content to replace.", f.Name)
	case f.IsShortcut():
		return nil, Errorf(ClassInvalid, "%s is a shortcut. It points at %s; update that instead.",
			f.Name, f.ShortcutDetails.TargetID)
	case f.IsWorkspaceDoc():
		return nil, Errorf(ClassUnsupported, "%s is %s. Its content is edited through the Docs, Sheets or "+
			"Slides API, which this server does not offer. To replace it wholesale, upload a new file with "+
			"convert_to and trash this one.", f.Name, model.Kind(f))
	}
	if in.ExpectHeadRevision != "" && f.HeadRevisionID != in.ExpectHeadRevision {
		return nil, Errorf(ClassExists, "%s is at revision %s, not the %s you expected: it changed since you "+
			"read it. Read it again before replacing it.", f.Name, f.HeadRevisionID, in.ExpectHeadRevision)
	}

	before := f.HeadRevisionID
	beforeSize := sizeOf(f)

	explicit := strings.TrimSpace(in.MimeType)
	contentType := explicit
	if contentType == "" {
		contentType = f.MimeType
	}
	req := gapi.UploadRequest{FileID: f.ID, ContentType: contentType}

	var updated *gdrive.File
	if localPath != "" {
		file, info, openErr := s.openLocal(localPath)
		if openErr != nil {
			return nil, openErr
		}
		defer func() { _ = file.Close() }()
		if explicit == "" {
			req.ContentType = sniffType(file.Name(), file)
		}
		updated, err = s.send(ctx, req, file, info.Size())
	} else {
		if len(in.Content) > gapi.MaxMultipartUpload {
			return nil, Errorf(ClassInvalid, "%s of inline content is more than update_content takes; "+
				"write it to %s and pass local_path.", model.HumanSize(int64(len(in.Content))), s.opts.LocalDir)
		}
		updated, err = s.api.UploadMultipart(ctx, req, []byte(in.Content))
	}
	if err != nil {
		return nil, wrap(err, "replacing the content of "+f.Name)
	}

	note := fmt.Sprintf("replaced the content: %s → %s, revision %s → %s",
		model.HumanSize(beforeSize), model.HumanSize(sizeOf(updated)),
		orNone(before), orNone(updated.HeadRevisionID))
	// The pin happens after the upload, not before it. Pinning first
	// meant a failed upload left a revision kept forever that nothing
	// had replaced and nothing would unpin; an old revision can be
	// pinned by id just as well once it is no longer the head.
	if in.KeepPreviousRevision && before != "" {
		if _, pinErr := s.api.UpdateRevision(ctx, f.ID, before, true); pinErr != nil {
			note += ". The content was replaced, but the previous revision could NOT be pinned (" +
				gapi.Message(pinErr) + "), so Drive will discard it after 30 days."
		} else {
			note += ". The previous revision is pinned and will not be discarded."
		}
	}
	return s.write(ctx, updated, outcome{Action: render.ActionUpdated, Note: note})
}

// convertTarget maps a convert_to name onto the Google format an import
// produces, or "" when no conversion was asked for.
func convertTarget(name string) (string, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return "", nil
	}
	target, ok := convertKinds[name]
	if !ok {
		return "", Errorf(ClassInvalid, "convert_to %q is not one of %s", name, strings.Join(ConvertKinds(), ", "))
	}
	return target, nil
}

// sniffType decides what a local file is: its extension first, because
// that is what the person who named it meant, and its first bytes when
// the extension says nothing.
func sniffType(path string, f io.ReadSeeker) string {
	if t := mime.TypeByExtension(filepath.Ext(path)); t != "" {
		return gdrive.MimeOnly(t)
	}
	head := make([]byte, 512)
	n, err := f.Read(head)
	if _, seekErr := f.Seek(0, io.SeekStart); seekErr != nil || (err != nil && !errors.Is(err, io.EOF)) {
		return "application/octet-stream"
	}
	return gdrive.MimeOnly(http.DetectContentType(head[:n]))
}

// assignID gives a create an id from Drive, so that a retry after an
// ambiguous failure cannot leave two files behind: Drive refuses the
// duplicate id and the first file stands.
//
// Drive refuses a generated id for the Docs Editors formats, so a new
// Doc, Sheet, Slides deck, Drawing or Form is created without one and is
// not idempotent. Nothing can be done about that from here; what catches
// the duplicate is the same-name guard on the next call.
func (s *Service) assignID(ctx context.Context, meta *gdrive.FileMeta) error {
	return s.assignIDFor(ctx, meta, meta.MimeType)
}

// assignIDFor is assignID where the file's type is not the one in the
// request body: a copy carries no mimeType unless it is converting, and
// what it becomes is the source's type.
func (s *Service) assignIDFor(ctx context.Context, meta *gdrive.FileMeta, becomes string) error {
	if !gapi.AcceptsGeneratedID(becomes) {
		return nil
	}
	ids, err := s.api.GenerateIDs(ctx, 1)
	if err != nil {
		return wrap(err, "asking Drive for a file id")
	}
	meta.ID = ids[0]
	return nil
}

// parentFolder resolves the folder a new file goes into, defaulting to
// the root of My Drive.
func (s *Service) parentFolder(ctx context.Context, reference string) (*gdrive.File, error) {
	target := strings.TrimSpace(reference)
	if target == "" {
		target = RootAlias
	}
	res, err := s.Resolve(ctx, target, ResolveOptions{FollowShortcut: true})
	if err != nil {
		return nil, err
	}
	if !res.File.IsFolder() {
		return nil, Errorf(ClassInvalid, "%s is %s, not a folder", res.File.Name, model.Kind(res.File))
	}
	return res.File, nil
}

// refuseDuplicate stops a second file of the same name in one folder.
// Drive allows it; a model that loses a result and retries produces it,
// and nobody notices until there are two.
func (s *Service) refuseDuplicate(ctx context.Context, parent *gdrive.File, name string, allow bool) error {
	if allow {
		return nil
	}
	list, err := s.api.ListFiles(ctx, gapi.ListQuery{
		Q: childQuery(parent.ID, "name = "+quote(name), false), PageSize: 5,
		Fields: "files(id,name,mimeType)", ResourceIDs: []string{parent.ID},
	})
	if err != nil {
		// The guard is a courtesy; failing at it must not stop the write
		// the caller actually asked for.
		s.log.DebugContext(ctx, "duplicate check unavailable", "class", gapi.Class(err))
		return nil
	}
	if len(list.Files) == 0 {
		return nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s already holds %s named %q:", parent.Name, model.Plural(len(list.Files), "item", "items"), name)
	for _, f := range list.Files {
		fmt.Fprintf(&b, "\n  %s  %s", f.ID, model.Kind(f))
	}
	b.WriteString("\nUse a different name, update that file, or pass allow_duplicate: true if two really are wanted.")
	return &Error{Class: ClassExists, Message: b.String()}
}

// writable refuses every write when the server was started read-only.
// The tools are not registered in that mode, so this is the second line:
// it holds if a later tool forgets to ask.
func (s *Service) writable(tool string) error {
	if s.opts.ReadOnly {
		return Errorf(ClassForbidden, "this server is read-only (GDRIVE_READ_ONLY), so %s is not available. "+
			"get_account says what it will do.", tool)
	}
	return nil
}

// outcome is what one write did, in the terms a result is built from.
// Every tool goes through it, including the ones that changed nothing:
// the text and the JSON are rendered from a single description of what
// happened, so they cannot say different things.
type outcome struct {
	Action render.Action
	Note   string
	// Changes are the fields the call altered, before and after.
	Changes []render.Change
	// DryRun marks a call that reported what it would do and did none of
	// it. The renderer says so; the caller does not have to remember to.
	DryRun bool
	// Moved says the file's name, place or existence changed, which is
	// what makes a cached path unreliable. A create does not: it cannot
	// falsify a path that already resolved.
	Moved bool
}

// write renders what a write produced: the file card, what changed, and
// the same in structured form.
func (s *Service) write(ctx context.Context, f *gdrive.File, out outcome) (*Result, error) {
	s.forget(f, out.Moved)
	return s.report(ctx, &Resolved{File: f}, out), nil
}

// report builds a result from a file that is already in hand. It is what
// the calls that changed nothing use — an "unchanged", a dry run — so
// that they take the same path as a real write rather than assembling a
// near-copy of it.
func (s *Service) report(ctx context.Context, res *Resolved, out outcome) *Result {
	m := s.Model(ctx, res)
	text := render.FileCard(m, render.FileCardOptions{
		Now: s.now(), FollowedShortcut: res.FollowedShortcut,
		Action: out.Action, Note: out.Note, Changes: out.Changes, DryRun: out.DryRun,
	})
	json := render.NewWriteJSON(m, out.Action, out.Note, text, out.Changes)
	json.DryRun = out.DryRun
	return &Result{Text: text, JSON: json}
}

// forget drops the cached copies of a file the moment it changes, so the
// next read sees what the write did rather than what was there before.
//
// Two different things go stale. The path that now leads to this file is
// stale whatever the write was: putting a second "notes.txt" beside the
// first makes that name ambiguous, and a cached entry would go on
// answering with the older file's id — the one thing hard rule 3 exists
// to prevent. The paths that *led* to the file go stale only when it
// moved, was renamed or was trashed, and those are cleared by value: a
// create cannot falsify a path that already resolved, and clearing the
// whole map on every create cost a 100-unit listing per path segment on
// the next call.
func (s *Service) forget(f *gdrive.File, moved bool) {
	if f == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.files, f.ID)
	delete(s.files, parentKey(f.ID))
	if parent := f.Parent(); parent != "" && f.Name != "" {
		delete(s.paths, pathKey(parent, f.Name, false))
		delete(s.paths, pathKey(parent, f.Name, true))
	}
	if !moved {
		return
	}
	for key, entry := range s.paths {
		if entry.value == f.ID {
			delete(s.paths, key)
		}
	}
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

// sortedKeys lists a lookup table's keys in order, with any extras that
// are accepted but have no entry. Every list a tool description or an
// error message names is built this way, so the words a model is offered
// are the words the code will accept.
func sortedKeys[V any](m map[string]V, extra ...string) []string {
	out := make([]string, 0, len(m)+len(extra))
	for k := range m {
		out = append(out, k)
	}
	out = append(out, extra...)
	sort.Strings(out)
	return out
}
