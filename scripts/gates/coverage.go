package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
)

// modulePath is this repository's module, the prefix every covered file
// carries in a coverage profile.
const modulePath = "github.com/mmedum/google-drive-mcp"

// corePackages carry the rules worth testing, and each has to clear the
// floor on its own: an average would let a well-tested renderer hide an
// untested policy.
var corePackages = []string{
	"internal/config", "internal/credentials", "internal/auth", "internal/gapi",
	"internal/ref", "internal/model", "internal/render", "internal/service",
	"internal/tools", "internal/server",
}

// block identifies one basic block in a coverage profile. The profile is
// produced with -coverpkg=./internal/..., so a block appears once per
// test binary that could have run it and has to be de-duplicated: the
// same block counts once, and counts as covered if any binary hit it.
type block struct {
	file   string
	extent string
}

func coverage(out io.Writer, args []string) error {
	profile := arg(args, 0, "cov.out")
	min, err := strconv.ParseFloat(arg(args, 1, "80"), 64)
	if err != nil {
		return fmt.Errorf("coverage floor %q: %w", arg(args, 1, "80"), err)
	}
	statements, hit, err := readProfile(profile)
	if err != nil {
		return err
	}

	var failed []string
	for _, pkg := range corePackages {
		prefix := modulePath + "/" + pkg + "/"
		var total, covered int
		for b, n := range statements {
			if !strings.HasPrefix(b.file, prefix) {
				continue
			}
			// Only this package's own files: a nested package has its own row.
			if strings.Contains(strings.TrimPrefix(b.file, prefix), "/") {
				continue
			}
			total += n
			if hit[b] {
				covered += n
			}
		}
		pct := 0.0
		if total > 0 {
			pct = 100 * float64(covered) / float64(total)
		}
		_, _ = fmt.Fprintf(out, "%-24s %6.1f%%\n", pkg, pct)
		if pct < min {
			failed = append(failed, pkg)
		}
	}
	if len(failed) > 0 {
		sort.Strings(failed)
		return fmt.Errorf("coverage below %.0f%% in %s", min, strings.Join(failed, ", "))
	}
	return nil
}

// readProfile parses a Go coverage profile into statement counts and
// which blocks were reached.
func readProfile(path string) (statements map[block]int, hit map[block]bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read coverage profile: %w", err)
	}
	defer func() { _ = f.Close() }()

	statements = map[block]int{}
	hit = map[block]bool{}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "mode:") {
			continue
		}
		// name.go:line.col,line.col numberOfStatements count
		fields := strings.Fields(text)
		if len(fields) != 3 {
			return nil, nil, fmt.Errorf("%s:%d: expected three fields, got %d", path, line, len(fields))
		}
		file, extent, ok := strings.Cut(fields[0], ":")
		if !ok {
			return nil, nil, fmt.Errorf("%s:%d: no file in %q", path, line, fields[0])
		}
		n, err := strconv.Atoi(fields[1])
		if err != nil {
			return nil, nil, fmt.Errorf("%s:%d: statement count: %w", path, line, err)
		}
		count, err := strconv.Atoi(fields[2])
		if err != nil {
			return nil, nil, fmt.Errorf("%s:%d: hit count: %w", path, line, err)
		}
		b := block{file: file, extent: extent}
		if _, seen := statements[b]; !seen {
			statements[b] = n
		}
		if count > 0 {
			hit[b] = true
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(statements) == 0 {
		return nil, nil, fmt.Errorf("%s holds no coverage data", path)
	}
	return statements, hit, nil
}
