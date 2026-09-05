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

// TestATenThousandItemTreeRendersUnderFiftyMilliseconds is the target of
// §11 in a form make check runs. It is a wide margin on purpose: a
// shared runner is slower than a laptop, and the failure worth catching
// is a renderer that became quadratic, not one that got ten per cent
// slower.
func TestATenThousandItemTreeRendersUnderFiftyMilliseconds(t *testing.T) {
	root := benchTree(10000)
	start := time.Now()
	out := Tree(root, TreeOptions{Title: "Everything — tree"})
	took := time.Since(start)
	if lines := strings.Count(out, "\n"); lines < 10000 {
		t.Fatalf("the tree rendered %d lines, so it is not the tree this measured", lines)
	}
	if took > 50*time.Millisecond {
		t.Errorf("rendering 10 000 items took %s, over the 50ms target", took)
	}
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
