package service_test

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mmedum/google-drive-mcp/v3/internal/gapi/drivetest"
	"github.com/mmedum/google-drive-mcp/v3/internal/service"
)

// Every write takes dry_run, and a dry run sends Drive nothing but reads,
// writes nothing to the local directory, and says it was a dry run.
func TestEveryWriteSendsNothingOnADryRun(t *testing.T) {
	type write func(ctx context.Context, svc *service.Service, fake *drivetest.Server, dir string) (any, error)
	for _, tc := range []struct {
		name string
		call write
	}{
		{"create_file", func(ctx context.Context, svc *service.Service, _ *drivetest.Server, _ string) (any, error) {
			return svc.CreateFile(ctx, service.CreateFileInput{Name: "new notes.txt", Parent: "id-2026-fixture",
				Content: "some text", DryRun: true})
		}},
		{"create_folder", func(ctx context.Context, svc *service.Service, _ *drivetest.Server, _ string) (any, error) {
			return svc.CreateFolder(ctx, service.CreateFolderInput{Name: "New folder", Parent: "id-2026-fixture", DryRun: true})
		}},
		{"create_shortcut", func(ctx context.Context, svc *service.Service, _ *drivetest.Server, _ string) (any, error) {
			return svc.CreateShortcut(ctx, service.CreateShortcutInput{Target: "id-budget-fixture",
				Parent: "id-archive-fixture", DryRun: true})
		}},
		{"upload_file", func(ctx context.Context, svc *service.Service, _ *drivetest.Server, dir string) (any, error) {
			return svc.UploadFile(ctx, service.UploadFileInput{LocalPath: filepath.Join(dir, "upload.txt"),
				Parent: "id-2026-fixture", DryRun: true})
		}},
		{"update_content", func(ctx context.Context, svc *service.Service, _ *drivetest.Server, _ string) (any, error) {
			return svc.UpdateContent(ctx, service.UpdateContentInput{File: "id-notes-txt-fixture",
				Content: "replaced", DryRun: true})
		}},
		{"update_file", func(ctx context.Context, svc *service.Service, _ *drivetest.Server, _ string) (any, error) {
			return svc.UpdateFile(ctx, service.UpdateFileInput{File: "id-budget-fixture", Name: "Budget 2.xlsx", DryRun: true})
		}},
		{"download_file", func(ctx context.Context, svc *service.Service, _ *drivetest.Server, _ string) (any, error) {
			return svc.DownloadFile(ctx, service.DownloadFileInput{File: "id-notes-txt-fixture", DryRun: true})
		}},
		{"extract_text", func(ctx context.Context, svc *service.Service, _ *drivetest.Server, _ string) (any, error) {
			return svc.ExtractText(ctx, service.ExtractTextInput{File: "id-receipt-fixture", DryRun: true})
		}},
		{"add_comment", func(ctx context.Context, svc *service.Service, _ *drivetest.Server, _ string) (any, error) {
			return svc.AddComment(ctx, service.AddCommentInput{File: "id-notes-fixture", Content: "a note", DryRun: true})
		}},
		{"reply_comment", func(ctx context.Context, svc *service.Service, _ *drivetest.Server, _ string) (any, error) {
			return svc.ReplyComment(ctx, service.ReplyCommentInput{File: "id-notes-fixture", Comment: "id-comment-fixture",
				Content: "a reply", DryRun: true})
		}},
		{"manage_approval", func(ctx context.Context, svc *service.Service, _ *drivetest.Server, _ string) (any, error) {
			return svc.ManageApproval(ctx, service.ManageApprovalInput{File: "id-notes-fixture", Approval: "id-approval-1",
				Action: "comment", Message: "still looking", DryRun: true})
		}},
		{"approve_file", func(ctx context.Context, svc *service.Service, _ *drivetest.Server, _ string) (any, error) {
			return svc.ApproveFile(ctx, service.ApproveFileInput{File: "id-notes-fixture", Approval: "id-approval-1", DryRun: true})
		}},
		{"manage_labels", func(ctx context.Context, svc *service.Service, _ *drivetest.Server, _ string) (any, error) {
			return svc.ManageLabels(ctx, service.ManageLabelsInput{File: "id-budget-fixture", Label: "id-label-review",
				Action: "apply", DryRun: true})
		}},
		{"manage_revision", func(ctx context.Context, svc *service.Service, fake *drivetest.Server, _ string) (any, error) {
			return svc.ManageRevision(ctx, service.ManageRevisionInput{File: "id-budget-fixture",
				Revision: fake.Revisions["id-budget-fixture"][0].ID, Action: "keep", DryRun: true})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "upload.txt"), []byte("local text"), 0o600); err != nil {
				t.Fatal(err)
			}
			svc, fake := setup(t, service.Options{LocalDir: dir, MaxDownload: 1 << 20, Labels: true})
			fake.LabelsEnabled = true
			fake.AddLabelDefinition("id-label-review", "Review state")
			fake.AddFile("id-notes-txt-fixture", "notes.txt", "text/plain", "id-2026-fixture")
			fake.SetContent("id-notes-txt-fixture", "the notes")
			fake.AddFile("id-receipt-fixture", "receipt.pdf", "application/pdf", "id-2026-fixture")
			fake.SetContent("id-receipt-fixture", "%PDF-1.4 a receipt")
			fake.AddComment("id-notes-fixture", "id-comment-fixture", "a thread")
			fake.AddApproval("id-notes-fixture", "id-approval-1", "person@example.com")
			fake.SetContent("id-budget-fixture", "first")
			fake.SetContent("id-budget-fixture", "second")
			before := len(fake.Requests)

			got, err := tc.call(t.Context(), svc, fake, dir)
			if err != nil {
				t.Fatalf("a dry run: %v", err)
			}
			for _, r := range fake.Requests[before:] {
				if r.Method != http.MethodGet {
					t.Errorf("a dry run sent %s %s", r.Method, r.Path)
				}
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 1 {
				t.Errorf("a dry run wrote to the local directory: %v, %v", entries, err)
			}
			text := fmt.Sprint(got)
			if r, ok := got.(*service.Result); ok {
				text = r.Text
			}
			if !strings.Contains(strings.ToLower(text), "dry run") {
				t.Errorf("the result does not say it was a dry run:\n%s", text)
			}
		})
	}
}

// A dry run runs every check the call would: what the call refuses, the
// dry run refuses with the same class (D-92).
func TestADryRunRefusesWhatTheCallRefuses(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(ctx context.Context, svc *service.Service, fake *drivetest.Server, dry bool) error
	}{
		{"an approval started with nobody to answer it", func(ctx context.Context, svc *service.Service, _ *drivetest.Server, dry bool) error {
			_, err := svc.ManageApproval(ctx, service.ManageApprovalInput{File: "id-notes-fixture", Action: "start", DryRun: dry})
			return err
		}},
		{"a reply with nothing to say", func(ctx context.Context, svc *service.Service, _ *drivetest.Server, dry bool) error {
			_, err := svc.ReplyComment(ctx, service.ReplyCommentInput{File: "id-notes-fixture", Comment: "id-comment-fixture",
				Action: "reply", DryRun: dry})
			return err
		}},
		{"a download past the size limit", func(ctx context.Context, svc *service.Service, _ *drivetest.Server, dry bool) error {
			_, err := svc.DownloadFile(ctx, service.DownloadFileInput{File: "id-big-download-fixture", DryRun: dry})
			return err
		}},
		{"an old kept revision unkept", func(ctx context.Context, svc *service.Service, fake *drivetest.Server, dry bool) error {
			old := fake.Revisions["id-budget-fixture"][0]
			old.KeepForever = true
			_, err := svc.ManageRevision(ctx, service.ManageRevisionInput{File: "id-budget-fixture", Revision: old.ID,
				Action: "unkeep", DryRun: dry})
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			class := func(dry bool) string {
				svc, fake := setup(t, service.Options{LocalDir: t.TempDir(), MaxDownload: 4})
				fake.AddComment("id-notes-fixture", "id-comment-fixture", "a thread")
				fake.AddFile("id-big-download-fixture", "big.txt", "text/plain", "id-2026-fixture")
				fake.SetContent("id-big-download-fixture", "more than four bytes")
				fake.SetContent("id-budget-fixture", "first")
				fake.SetContent("id-budget-fixture", "second")
				err := tc.call(t.Context(), svc, fake, dry)
				if err == nil {
					return ""
				}
				c, _, _ := strings.Cut(err.Error(), "]")
				return c + "]"
			}
			real, dry := class(false), class(true)
			if real == "" || dry != real {
				t.Errorf("the call refuses with %q and its dry run with %q, want the same refusal", real, dry)
			}
		})
	}
}
