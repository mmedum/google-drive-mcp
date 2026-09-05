package render

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mmedum/google-drive-mcp/internal/gdrive"
	"github.com/mmedum/google-drive-mcp/internal/model"
)

// benchTree builds a tree of n items to lay out. It is the renderer's
// half of the target in §11: what a walk costs on the wire is one
// listing per folder, and what it costs here is laying the answer out.
//
// The shape matters as much as the count. Three folders per folder puts
// ten thousand items about eight levels down, which is inside the depth
// a walk will go to (service.MaxMaxDepth); the first version of this
// made one folder per folder and produced a thousand levels, which is a
// tree Drive would never hand back and which measured the cost of the
// indent rather than the cost of the listing.
func benchTree(n int) *TreeNode {
	root := &TreeNode{File: model.New(&gdrive.File{
		ID: "id-bench-root", Name: "Everything", MimeType: gdrive.MimeFolder,
	}, model.Options{})}
	level := []*TreeNode{root}
	made := 0
	for made < n && len(level) > 0 {
		var next []*TreeNode
		for _, parent := range level {
			for i := 0; i < 10 && made < n; i++ {
				made++
				mime := gdrive.MimeDocument
				if i < 3 {
					mime = gdrive.MimeFolder
				}
				node := &TreeNode{File: model.New(&gdrive.File{
					ID:           fmt.Sprintf("id-bench-%06d", made),
					Name:         fmt.Sprintf("Item %06d", made),
					MimeType:     mime,
					ModifiedTime: "2026-03-04T09:00:00Z",
					Size:         "4096",
				}, model.Options{})}
				parent.Children = append(parent.Children, node)
				parent.Items++
				if mime == gdrive.MimeFolder {
					next = append(next, node)
				}
			}
		}
		level = next
	}
	return root
}

func BenchmarkTree10000(b *testing.B) {
	root := benchTree(10000)
	for b.Loop() {
		if out := Tree(root, TreeOptions{Title: "Everything — tree"}); out == "" {
			b.Fatal("empty")
		}
	}
}

// TestATenThousandItemTreeStaysLinear is the §11 target in a form make
// check can run.
//
// It does not assert the 50 ms, because make check runs the tests under
// the race detector and coverage counters, and 10 000 items take about
// 4.7 ms uninstrumented and over 60 ms with both on. A wall-clock
// ceiling there would be measuring the instrumentation. The failure
// worth catching is a renderer that became quadratic, and that shows up
// as a ratio however slow the machine is: ten times the items should
// cost about ten times the work, and the allowance below is double that.
//
// The absolute number is in the benchmark, where nothing is instrumented.
func TestATenThousandItemTreeStaysLinear(t *testing.T) {
	small := time.Duration(0)
	large := time.Duration(0)
	// Three runs each, taking the fastest: a shared runner will stall one
	// of them, and a flaky performance test is a test people delete.
	for range 3 {
		small = fastest(small, timeTree(t, 1000))
		large = fastest(large, timeTree(t, 10000))
	}
	t.Logf("1 000 items in %s, 10 000 in %s", small, large)
	if small <= 0 {
		t.Fatal("the smaller tree took no measurable time, so the ratio below means nothing")
	}
	if ratio := float64(large) / float64(small); ratio > 20 {
		t.Errorf("ten times the items cost %.1f times the work; the renderer is not linear any more", ratio)
	}
	// A backstop far above anything instrumentation explains, so a
	// change that made every size equally slow is still caught.
	if large > 500*time.Millisecond {
		t.Errorf("rendering 10 000 items took %s", large)
	}
}

func timeTree(t *testing.T, n int) time.Duration {
	t.Helper()
	root := benchTree(n)
	start := time.Now()
	out := Tree(root, TreeOptions{Title: "Everything — tree"})
	took := time.Since(start)
	if lines := strings.Count(out, "\n"); lines < n {
		t.Fatalf("the tree rendered %d lines for %d items, so it is not the tree this measured", lines, n)
	}
	return took
}

func fastest(best, got time.Duration) time.Duration {
	if best == 0 || got < best {
		return got
	}
	return best
}

func BenchmarkFileCard(b *testing.B) {
	card := sheet()
	now := time.Date(2026, 3, 6, 12, 0, 0, 0, time.UTC)
	for b.Loop() {
		if out := FileCard(card, FileCardOptions{Now: now}); out == "" {
			b.Fatal("empty")
		}
	}
}
