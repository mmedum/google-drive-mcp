package service_test

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mmedum/google-drive-mcp/internal/gapi/drivetest"
	"github.com/mmedum/google-drive-mcp/internal/gdrive"
	"github.com/mmedum/google-drive-mcp/internal/service"
)

// The numbers §11 of docs/architecture.md commits to, measured against
// the in-memory Drive. The fake answers in microseconds, so what these
// measure is this server's own work: the calls it makes, the parsing and
// the rendering. Round trips to Google dominate in production, which is
// why the call counts beside them are asserted in tests of their own
// (targets_test.go) rather than left to a benchmark nobody runs.

func BenchmarkGetFile(b *testing.B) {
	svc, _ := setup(b, service.Options{})
	for b.Loop() {
		if _, err := svc.GetFile(b.Context(), service.GetFileInput{File: "id-budget-fixture"}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkResolvePathCold(b *testing.B) {
	// A fresh service per iteration, so every walk pays for itself: this
	// is what the first call of a session costs.
	for b.Loop() {
		b.StopTimer()
		svc, _ := setup(b, service.Options{})
		b.StartTimer()
		if _, err := svc.Resolve(b.Context(), "/Projects/2026/Budget.xlsx", service.ResolveOptions{}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkResolvePathWarm(b *testing.B) {
	svc, _ := setup(b, service.Options{})
	if _, err := svc.Resolve(b.Context(), "/Projects/2026/Budget.xlsx", service.ResolveOptions{}); err != nil {
		b.Fatal(err)
	}
	for b.Loop() {
		if _, err := svc.Resolve(b.Context(), "/Projects/2026/Budget.xlsx", service.ResolveOptions{}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSearchPage(b *testing.B) {
	svc, fake := setup(b, service.Options{})
	for i := range 100 {
		fake.AddFile(fmt.Sprintf("id-bench-hit-%03d", i), fmt.Sprintf("Report %03d", i),
			gdrive.MimeDocument, "id-2026-fixture")
	}
	for b.Loop() {
		if _, err := svc.Search(b.Context(), service.SearchInput{Name: "Report"}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkListFolderTree(b *testing.B) {
	svc, fake := setup(b, service.Options{})
	benchTree(fake, "id-projects-fixture", 3, 8)
	for b.Loop() {
		if _, err := svc.ListFolder(b.Context(), service.ListFolderInput{
			Folder: "id-projects-fixture", Recursive: true, MaxDepth: 10, MaxItems: 2000,
		}); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkDownloadStream pulls a large file through the client and
// reports how much was allocated to do it. The number that matters is
// bytes allocated per operation: a streaming copy is flat in the file's
// size and a buffered one is not, so a regression that started holding
// the file in memory shows up here as a jump of the file's whole size
// rather than as a slower run.
func BenchmarkDownloadStream(b *testing.B) {
	const size = 256 << 20
	dir := b.TempDir()
	svc, fake := setup(b, service.Options{LocalDir: dir, MaxDownload: 2 * size})
	fake.AddFile("id-benchmark-blob-fixture", "big.bin", "application/octet-stream", "id-2026-fixture")
	fake.AddGeneratedContent("id-benchmark-blob-fixture", size)
	b.SetBytes(size)
	for b.Loop() {
		if _, err := svc.DownloadFile(b.Context(), service.DownloadFileInput{File: "id-benchmark-blob-fixture"}); err != nil {
			b.Fatal(err)
		}
		// A download will not land on top of a file that is already
		// there, which is deliberate; clearing the directory is not part
		// of what is being measured.
		b.StopTimer()
		entries, err := os.ReadDir(dir)
		if err != nil {
			b.Fatal(err)
		}
		for _, e := range entries {
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
				b.Fatal(err)
			}
		}
		b.StartTimer()
	}
}

// TestADownloadDoesNotHoldTheFile is the assertion behind that
// benchmark, in a form make check runs. A gigabyte is streamed through
// the client and the heap is measured across it: what a streaming copy
// allocates is its buffer and its bookkeeping, and what a buffered one
// allocates is the file.
func TestADownloadDoesNotHoldTheFile(t *testing.T) {
	if testing.Short() {
		t.Skip("streams a gigabyte through the fake")
	}
	const size = 1 << 30
	dir := t.TempDir()
	svc, fake := setup(t, service.Options{LocalDir: dir, MaxDownload: 2 * size})
	fake.AddFile("id-big-blob-fixture", "big.bin", "application/octet-stream", "id-2026-fixture")
	fake.AddGeneratedContent("id-big-blob-fixture", size)

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	out, err := svc.DownloadFile(t.Context(), service.DownloadFileInput{File: "id-big-blob-fixture"})
	if err != nil {
		t.Fatalf("DownloadFile: %v", err)
	}
	runtime.ReadMemStats(&after)

	// TotalAlloc is cumulative, so this is every byte the download asked
	// the allocator for, not the peak. A copy through a 256 KiB buffer
	// stays in the low megabytes whatever the file's size; a read into
	// memory cannot come in under the file.
	allocated := after.TotalAlloc - before.TotalAlloc
	const ceiling = 64 << 20
	if allocated > ceiling {
		t.Errorf("downloading %d bytes allocated %d, over the %d ceiling: something is holding the file",
			int64(size), allocated, int64(ceiling))
	}
	if !strings.Contains(out, "big.bin") {
		t.Fatalf("the result does not name the file it wrote:\n%s", out)
	}
	// And the bytes really landed, or the measurement above is of a
	// download that did not happen.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read the local directory: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("the local directory holds %d files, want the one that was downloaded", len(entries))
	}
	info, err := entries[0].Info()
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() != size {
		t.Errorf("wrote %d bytes, want %d", info.Size(), int64(size))
	}
}

// benchTree fills a folder with a synthetic tree: width children per
// folder, depth levels deep.
func benchTree(fake *drivetest.Server, parent string, depth, width int) int {
	if depth == 0 {
		return 0
	}
	made := 0
	for i := range width {
		id := fmt.Sprintf("id-bench-%s-%d", parent, i)
		if depth > 1 && i%2 == 0 {
			fake.AddFolder(id, fmt.Sprintf("Folder %d", i), parent)
			made += 1 + benchTree(fake, id, depth-1, width)
			continue
		}
		fake.AddFile(id, fmt.Sprintf("File %d", i), gdrive.MimeDocument, parent)
		made++
	}
	return made
}
