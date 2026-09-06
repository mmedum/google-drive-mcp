package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
)

// staleness fails when the documentation drifts from the code. Each
// check below exists because the drift it catches is invisible in review:
// nothing breaks, the docs simply start lying.
func staleness(out io.Writer, args []string) error {
	binary := arg(args, 0, "./google-drive-mcp")
	var problems []string

	// The README documents every tool that can be registered, not only
	// the ones a default build carries, so the surface it is compared
	// against is the one with the feature flags on. Without this a tool
	// behind a flag could be documented and deleted, or added and never
	// documented, and this gate would say nothing either way.
	//
	// The destructive five are deliberately not included: the README
	// names them in a paragraph rather than in the table, because a row
	// beside the ordinary tools is exactly the wrong prominence for them.
	dump, _, err := dumpSchemas(binary, "GDRIVE_LABELS=true", "GDRIVE_ACTIVITY=true")
	if err != nil {
		return err
	}
	problems = append(problems, checkReadmeTools(dump.names())...)

	settings, err := definedSettings()
	if err != nil {
		return err
	}
	problems = append(problems, checkConfigDocs(settings)...)
	problems = append(problems, checkArchitectureStatus()...)

	changelog, err := checkChangelog()
	if err != nil {
		return err
	}
	problems = append(problems, changelog...)

	if len(problems) > 0 {
		for _, p := range problems {
			_, _ = fmt.Fprintln(out, p)
		}
		return fmt.Errorf("%d documentation problem(s)", len(problems))
	}
	_, _ = fmt.Fprintln(out, "staleness check ok")
	return nil
}

// readmeToolRow matches a row of the README's tool table.
var readmeToolRow = regexp.MustCompile("(?m)^\\| `([a-z_]+)` \\|")

// checkReadmeTools keeps the README's table and the registered tools in
// step, in both directions: a tool nobody documented, and a documented
// tool that no longer exists.
func checkReadmeTools(registered []string) []string {
	readme, err := os.ReadFile("README.md")
	if err != nil {
		return []string{"cannot read README.md: " + err.Error()}
	}
	documented := map[string]bool{}
	for _, m := range readmeToolRow.FindAllStringSubmatch(string(readme), -1) {
		documented[m[1]] = true
	}
	var problems []string
	for _, name := range registered {
		if !documented[name] {
			problems = append(problems, "README's tool table does not list the registered tool "+name)
		}
		delete(documented, name)
	}
	extra := make([]string, 0, len(documented))
	for name := range documented {
		extra = append(extra, name)
	}
	sort.Strings(extra)
	for _, name := range extra {
		problems = append(problems, "README's tool table lists "+name+", which is not registered")
	}
	return problems
}

// settingDefinition matches the Define calls in config.go, which are the
// authority on which GDRIVE_* variables exist.
var settingDefinition = regexp.MustCompile(`def\(&s\.[A-Za-z]+, "[a-z-]+", "([A-Z_]+)"`)

func definedSettings() ([]string, error) {
	source, err := os.ReadFile("internal/config/config.go")
	if err != nil {
		return nil, fmt.Errorf("read internal/config/config.go: %w", err)
	}
	matches := settingDefinition.FindAllStringSubmatch(string(source), -1)
	if len(matches) == 0 {
		return nil, fmt.Errorf("found no settings in internal/config/config.go; has Define changed shape?")
	}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, m[1])
	}
	return out, nil
}

func checkConfigDocs(settings []string) []string {
	docs, err := os.ReadFile("docs/configuration.md")
	if err != nil {
		return []string{"cannot read docs/configuration.md: " + err.Error()}
	}
	var problems []string
	for _, name := range settings {
		if !strings.Contains(string(docs), "GDRIVE_"+name) {
			problems = append(problems, "docs/configuration.md does not document GDRIVE_"+name)
		}
	}
	return problems
}

func checkArchitectureStatus() []string {
	arch, err := os.ReadFile("docs/architecture.md")
	if err != nil {
		return []string{"cannot read docs/architecture.md: " + err.Error()}
	}
	if strings.Contains(strings.ToLower(string(arch)), "no code yet") {
		return []string{"docs/architecture.md still says 'no code yet'"}
	}
	return nil
}

var versionHeading = regexp.MustCompile(`(?m)^## \[([0-9]+\.[0-9]+\.[0-9]+)\]`)

// checkChangelog requires source changes to be written up. Normally that
// means content under [Unreleased]; on a release commit those notes have
// just moved under the new version heading, which is only acceptable
// while that version has no tag yet.
func checkChangelog() ([]string, error) {
	if !goFilesChanged() {
		return nil, nil
	}
	raw, err := os.ReadFile("CHANGELOG.md")
	if err != nil {
		return nil, fmt.Errorf("read CHANGELOG.md: %w", err)
	}
	text := string(raw)
	if countEntries(text, "## [Unreleased]") > 0 {
		return nil, nil
	}
	where := "[Unreleased]"
	if m := versionHeading.FindStringSubmatch(text); m != nil {
		if !tagExists("v" + m[1]) {
			if countEntries(text, "## ["+m[1]+"]") > 0 {
				return nil, nil
			}
			where = "[" + m[1] + "] (untagged, so this is a release commit)"
		}
	}
	return []string{"source changed but CHANGELOG.md " + where + " is empty"}, nil
}

// countEntries counts the bullet points in the section under heading.
func countEntries(text, heading string) int {
	_, rest, ok := strings.Cut(text, heading)
	if !ok {
		return 0
	}
	if next := strings.Index(rest, "\n## ["); next >= 0 {
		rest = rest[:next]
	}
	n := 0
	for _, line := range strings.Split(rest, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "- ") {
			n++
		}
	}
	return n
}

func goFilesChanged() bool {
	rev := "HEAD~1"
	if last := lastTag(); last != "" {
		rev = last
	}
	// A non-zero status means either a difference or no such revision;
	// on the very first commit there is nothing to compare against, and
	// requiring the notes then is the safe way round.
	return exec.Command("git", "diff", "--quiet", rev, "--", "*.go").Run() != nil
}

func tagExists(tag string) bool {
	return exec.Command("git", "rev-parse", "-q", "--verify", "refs/tags/"+tag).Run() == nil
}
