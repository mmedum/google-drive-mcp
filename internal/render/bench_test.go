package render

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mmedum/google-drive-mcp/v3/internal/gdrive"
	"github.com/mmedum/google-drive-mcp/v3/internal/model"
)

// benchTree builds a tree of n items to lay out. It is the renderer's
// half of the target that a 10 000-item tree renders in under 50 ms:
// what a walk costs on the wire is one listing per folder, and what it
// costs here is laying the answer out.
//
// The shape matters as much as the count. Three folders per folder puts
// ten thousand items about eight levels down, which is inside the depth
// a walk will go to (service.MaxMaxDepth). One folder per folder would
// make a thousand levels, a tree Drive would never hand back, and would
// measure the cost of the indent rather than the cost of the listing.
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

// TestATenThousandItemTreeStaysLinear is the 50 ms target in a form make
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
// Sizes are chosen so the smaller measurement is milliseconds rather
// than microseconds. At 1 000 items a render takes about as long as
// scheduling noise on a shared runner, and the ratio fails a plain `go
// test` while passing under `go test -race`, whose constant overhead
// inflates the small side and flatters the ratio.
const (
	linearSmallN = 5_000
	linearLargeN = 50_000
)

func TestATenThousandItemTreeStaysLinear(t *testing.T) {
	var small, large time.Duration
	perLine := 0.0
	// Three runs each, taking the fastest: a shared runner will stall one
	// of them, and a flaky performance test is a test people delete. The
	// tree and the plain lines are compared within one run, so a stall in
	// either half is not set against a quiet moment in the other.
	for range 3 {
		small = fastest(small, timeTree(t, linearSmallN))
		tree := timeTree(t, linearLargeN)
		large = fastest(large, tree)
		if r := float64(tree) / float64(timeLines(linearLargeN)); perLine == 0 || r < perLine {
			perLine = r
		}
	}
	t.Logf("%d items in %s, %d in %s, %.1f times as many plain lines", linearSmallN, small, linearLargeN, large, perLine)

	// A backstop against every size becoming equally slow, measured against
	// writing as many plain lines, which a busy machine slows as much.
	// Measured: 2.6 to 2.7 plain, 1.2 under the race detector, 2.2 under it
	// with the coverage counters make check adds.
	if perLine > 5 {
		t.Errorf("rendering %d items cost %.1f times writing as many lines", linearLargeN, perLine)
	}

	// Below a millisecond the ratio is measuring the machine, not the
	// renderer. Asserting on it anyway is what made this test unreliable.
	if small < time.Millisecond {
		t.Skipf("the smaller tree rendered in %s, too fast to compare against on this machine", small)
	}
	// Ten times the items should cost about ten times the work. The limit
	// is not 11, or even 20, because this test exists to catch a
	// complexity regression and not to police a constant factor: if the
	// renderer went quadratic the ratio would be near 100, while
	// instrumentation and a shared runner move it by a factor of two.
	// Measured: 14.7 locally, 21.8 under the race detector, 22.4 on a
	// contended runner without it. A limit of 20 called all but the first
	// of those a regression, which is how this came to block a release
	// twice while reporting nothing about the renderer.
	if ratio := float64(large) / float64(small); ratio > 40 {
		t.Errorf("ten times the items cost %.1f times the work; the renderer looks superlinear", ratio)
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

// timeLines writes n lines shaped like a tree's rows, the work a linear
// renderer cannot do with less.
func timeLines(n int) time.Duration {
	start := time.Now()
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "├── item %d.txt  [1SyntheticFixtureFileId%011d]  (text file, %d B)\n", i, i, i)
	}
	return time.Since(start)
}

func fastest(best, got time.Duration) time.Duration {
	if best == 0 || got < best {
		return got
	}
	return best
}

func BenchmarkFileCard(b *testing.B) {
	card := sheet()
	for b.Loop() {
		if out := FileCard(card, FileCardOptions{Now: now}); out == "" {
			b.Fatal("empty")
		}
	}
}
