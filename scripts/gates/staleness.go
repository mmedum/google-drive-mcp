package main

import (
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
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
	// The destructive five ARE in that surface and are deliberately not
	// in the README's table: it names them in a paragraph instead,
	// because a row beside the ordinary tools is the wrong prominence for
	// them. checkReadmeTools knows them by name.
	env, err := fullSurfaceEnv()
	if err != nil {
		return err
	}
	dump, _, err := dumpSchemas(binary, env...)
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
	problems = append(problems, checkStatusVersions()...)
	problems = append(problems, checkScopesDocumented()...)

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

// documentedInProse are the tools the README describes in a paragraph
// rather than in its table, with the reason. A row beside the ordinary
// tools is the wrong prominence for a tool that destroys without a way
// back — but "not in the table" still has to be a decision the gate
// knows about, or a tool could go undocumented by looking like one of
// these.
var documentedInProse = map[string]string{
	"delete_file":     "permanent, skips the trash",
	"empty_trash":     "the whole account's trash",
	"delete_drive":    "an empty shared drive",
	"delete_revision": "one blob revision",
	"delete_comment":  "a thread or one reply",
}

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
		if _, prose := documentedInProse[name]; prose {
			// Named in a paragraph on purpose. It still has to be MENTIONED
			// somewhere, which is checked below.
			if !strings.Contains(string(readme), "`"+name+"`") {
				problems = append(problems, "README does not mention the registered tool "+name+
					" at all; it is meant to be described in prose rather than in the table")
			}
			delete(documented, name)
			continue
		}
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

// statusVersion matches the version a status line claims, in either of
// the two spellings the two documents use.
var statusVersion = map[string]*regexp.Regexp{
	"README.md":            regexp.MustCompile(`(?m)^\*\*Status: v([0-9]+\.[0-9]+\.[0-9]+)`),
	"docs/architecture.md": regexp.MustCompile(`(?m)^\*\*Status:\*\* phase [0-9]+ complete \([^)]*\), released as v([0-9]+\.[0-9]+\.[0-9]+)`),
}

// checkStatusVersions holds the two status lines to the newest version
// in the changelog.
//
// The README said "Status: v0.3.0, phase 3" for two whole releases, and
// promised Workspace labels as something that would "arrive in v0.4.0"
// after they had shipped. Nothing noticed, because the gate beside this
// one reads the README's tool TABLE — which was correct throughout — and
// checkArchitectureStatus above greps for one phase-0 placeholder. A
// document can be wrong about what it is while every list in it is right.
//
// Only the version is checked. The prose around it is a human's to keep
// honest; a number that has to match another number in the repository is
// the part a gate can hold.
func checkStatusVersions() []string {
	raw, err := os.ReadFile("CHANGELOG.md")
	if err != nil {
		return []string{"cannot read CHANGELOG.md: " + err.Error()}
	}
	m := versionHeading.FindStringSubmatch(string(raw))
	if m == nil {
		// Before the first release there is nothing to be stale against.
		return nil
	}
	released := m[1]

	var problems []string
	for _, path := range slices.Sorted(maps.Keys(statusVersion)) {
		doc, err := os.ReadFile(path) //nolint:gosec // a path this repository owns
		if err != nil {
			problems = append(problems, "cannot read "+path+": "+err.Error())
			continue
		}
		found := statusVersion[path].FindStringSubmatch(string(doc))
		if found == nil {
			problems = append(problems, path+" has no status line naming a version, "+
				"or it no longer has the shape this gate reads")
			continue
		}
		if found[1] != released {
			problems = append(problems, fmt.Sprintf(
				"%s says the status is v%s and the newest version in CHANGELOG.md is v%s",
				path, found[1], released))
		}
	}
	return problems
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

// checkScopesDocumented holds the README's setup step to the scopes
// login can actually request.
//
// A setup instruction that names fewer scopes than the code asks for is
// disprovable in one command, which for a setup step is the same as
// being wrong: the reader adds what they were told to, runs login, and
// is refused by a consent screen that does not carry the rest. This
// README named one scope where the code can ask for five — the label and
// activity pairs are requested only with their features on, which is
// exactly why nobody noticed.
//
// Found by the first outside person to install this family of servers,
// who checked the equivalent instruction in a sibling repository against
// its code and reported the gap. The sibling's instruction turned out to
// be right and unexplained; this one was neither.
func checkScopesDocumented() []string {
	source, err := os.ReadFile(filepath.Join("internal", "auth", "auth.go"))
	if err != nil {
		return []string{"cannot read internal/auth/auth.go: " + err.Error()}
	}
	readme, err := os.ReadFile("README.md")
	if err != nil {
		return []string{"cannot read README.md: " + err.Error()}
	}
	found := scopeConstant.FindAllStringSubmatch(string(source), -1)
	if len(found) == 0 {
		return []string{"found no scope constants in internal/auth/auth.go; has the naming changed?"}
	}
	var problems []string
	for _, m := range found {
		if !strings.Contains(string(readme), m[1]) {
			problems = append(problems, fmt.Sprintf(
				"login can request %s and README.md does not name it. A setup step that lists fewer "+
					"scopes than the code asks for sends somebody to a consent screen that will "+
					"refuse them", m[1]))
		}
	}
	return problems
}

// scopeConstant matches the Scope* constants internal/auth declares.
var scopeConstant = regexp.MustCompile(`Scope[A-Za-z]*\s*=\s*"(https://www\.googleapis\.com/auth/[a-z.]+)"`)
