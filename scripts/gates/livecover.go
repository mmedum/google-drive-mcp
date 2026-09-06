package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/mmedum/google-drive-mcp/scripts/internal/livecover"
)

// The live driver is the only check that Drive agrees with this server,
// and nobody knew what fraction of the tool surface one run exercises.
// §17a raised it after phase 5 grew the driver a great deal, including a
// whole destructive mode, and the honest version of the entry was that a
// number nobody has is not evidence of a good one. It reads 122 of 188.
//
// testdata/live-cover.tsv is the record, in the shape
// testdata/api-coverage.tsv already uses here: every option the driver
// does not send is either undrivable from this account or an undriven
// gap with the recipe for closing it, and this gate holds both
// directions — an option that is neither driven nor listed fails, and an
// option listed that the driver has since started sending fails too.
//
// The blindness is worth stating because it decides what this gate is
// worth. It reads the driver's SOURCE, so a step that exists and never
// runs — in a slice nobody passes on, behind a condition that was false
// — reads exactly like a step that runs. A sibling repository proved
// that by deleting one call and watching its own static gate report full
// coverage. That is why the driver carries a recorder as well, and why
// the recorder is the half to build first if only one gets built: it
// knows what was sent because it sent it.
const liveCoverFile = "testdata/live-cover.tsv"

// liveCoverEntry is one row: an option the driver does not send, and why.
type liveCoverEntry struct {
	verdict string
	reason  string
	line    int
}

func liveCover(out io.Writer, args []string) error {
	binary := arg(args, 0, "./google-drive-mcp")
	env, err := fullSurfaceEnv()
	if err != nil {
		return err
	}
	dump, _, err := dumpSchemas(binary, env...)
	if err != nil {
		return err
	}
	known := map[string][]string{}
	for _, t := range dump.Tools {
		options := make([]string, 0, len(t.InputSchema.Properties))
		for name := range t.InputSchema.Properties {
			options = append(options, name)
		}
		sort.Strings(options)
		known[t.Name] = options
	}
	if len(known) < 20 {
		return fmt.Errorf("the dump carries %d tools; this gate is not looking at the whole surface", len(known))
	}

	recorded, err := readLiveCover()
	if err != nil {
		return err
	}
	sent, err := livecover.FromSource("scripts/livedrive", known)
	if err != nil {
		return err
	}

	var problems []string
	total, driven, excused := 0, 0, 0
	for _, tool := range livecover.Sorted(known) {
		for _, option := range known[tool] {
			total++
			name := tool + "." + option
			entry, listed := recorded[name]
			switch {
			case sent[tool][option] && listed:
				problems = append(problems, fmt.Sprintf(
					"%s:%d: %s is listed as %s and the driver sends it; delete the row",
					liveCoverFile, entry.line, name, entry.verdict))
				driven++
			case sent[tool][option]:
				driven++
			case listed:
				excused++
			default:
				problems = append(problems, fmt.Sprintf(
					"%s is a tool option no step in the live driver sends, and %s does not say why. "+
						"Drive it, or add a row: undrivable with what blocks it, or undriven with what "+
						"closing it would take", name, liveCoverFile))
			}
		}
	}
	// The other direction, and the one that rots quietly: a row for an
	// option that no longer exists outlives the option by years, and
	// reads as a decision somebody made about today's surface.
	for _, name := range livecover.Sorted(recorded) {
		tool, option, _ := strings.Cut(name, ".")
		if _, ok := known[tool]; !ok {
			problems = append(problems, fmt.Sprintf("%s:%d: %s names a tool that does not exist",
				liveCoverFile, recorded[name].line, name))
			continue
		}
		if !hasOption(known[tool], option) {
			problems = append(problems, fmt.Sprintf("%s:%d: %s names an option %s does not have",
				liveCoverFile, recorded[name].line, name, tool))
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		for _, p := range problems {
			_, _ = fmt.Fprintln(out, p)
		}
		return fmt.Errorf("%d live-coverage problem(s)", len(problems))
	}
	_, _ = fmt.Fprintf(out, "live cover ok (%d of %d options driven, %d recorded as not: %d undrivable, "+
		"%d undriven)\n", driven, total, excused, countVerdict(recorded, "undrivable"),
		countVerdict(recorded, "undriven"))
	return nil
}

func hasOption(options []string, want string) bool {
	for _, o := range options {
		if o == want {
			return true
		}
	}
	return false
}

func countVerdict(recorded map[string]liveCoverEntry, verdict string) int {
	n := 0
	for _, e := range recorded {
		if e.verdict == verdict {
			n++
		}
	}
	return n
}

// readLiveCover reads the record. A verdict this gate does not know is a
// refusal rather than a shrug: the two words carry the difference
// between a gap somebody could close this afternoon and one that needs
// another person, and inventing a third silently would lose it.
func readLiveCover() (map[string]liveCoverEntry, error) {
	f, err := os.Open(liveCoverFile)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	out := map[string]liveCoverEntry{}
	scan := bufio.NewScanner(f)
	line := 0
	for scan.Scan() {
		line++
		// Spaces and a stray carriage return go; a trailing TAB does not,
		// because it is the empty third field — and trimming it turned
		// "no reason given" into "wrong number of fields", which is a
		// worse message for the commoner mistake.
		text := strings.TrimRight(scan.Text(), " \r")
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		fields := strings.Split(text, "\t")
		if len(fields) != 3 {
			return nil, fmt.Errorf("%s:%d: want three tab-separated fields, got %d", liveCoverFile, line, len(fields))
		}
		name, verdict, reason := fields[0], fields[1], strings.TrimSpace(fields[2])
		if verdict != "undrivable" && verdict != "undriven" {
			return nil, fmt.Errorf("%s:%d: %s has verdict %q; it must be undrivable or undriven",
				liveCoverFile, line, name, verdict)
		}
		if reason == "" {
			return nil, fmt.Errorf("%s:%d: %s carries no reason, and %q without one is not a decision",
				liveCoverFile, line, name, verdict)
		}
		if _, seen := out[name]; seen {
			return nil, fmt.Errorf("%s:%d: %s is listed twice", liveCoverFile, line, name)
		}
		out[name] = liveCoverEntry{verdict: verdict, reason: reason, line: line}
	}
	if err := scan.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s lists nothing; a record of no decisions passes every check there is",
			liveCoverFile)
	}
	return out, nil
}
