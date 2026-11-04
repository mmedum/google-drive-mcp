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

	"github.com/mmedum/google-drive-mcp/v3/internal/config"
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
	// The tools GDRIVE_ENABLE_DESTRUCTIVE registers ARE in that surface
	// and are deliberately not in the README's table: it names them in a
	// paragraph instead, because a row beside the ordinary tools is the
	// wrong prominence for them. checkReadmeTools knows them by name.
	dump, _, err := dumpSchemas(binary, fullSurfaceEnv()...)
	if err != nil {
		return err
	}
	problems = append(problems, checkReadmeTools(dump.names())...)

	problems = append(problems, checkConfigDocs(config.EnvVars())...)
	problems = append(problems, checkScopesDocumented()...)

	changelog, err := checkChangelog(".")
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
// back, or reaches past the organization — but "not in the table" still
// has to be a decision the gate knows about, or a tool could go
// undocumented by looking like one of these.
var documentedInProse = map[string]string{
	"delete_file":                "permanent, skips the trash",
	"empty_trash":                "the whole account's trash",
	"delete_drive":               "an empty shared drive",
	"delete_revision":            "one blob revision",
	"delete_comment":             "a thread or one reply",
	"share_by_link":              "anyone with the link, or a whole domain",
	"share_outside_organization": "someone outside the organization",
	"transfer_ownership":         "a new owner, for good",
	"lift_drive_restriction":     "a shared drive opened past the organization",
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
	return append(problems, checkPermissionRules(string(readme), registered)...)
}

// permissionRule is a Claude Code permission rule for one of this
// server's tools, under the server name the README's own `claude mcp
// add` gives it.
var permissionRule = regexp.MustCompile(`"mcp__google-drive__([a-z0-9_]+)"`)

// checkPermissionRules holds the README's Claude Code permission rules
// to the registered tools. A rule for a tool that does not exist matches
// nothing, so the person it was meant to ask is never asked. A README
// with no rule at all is reported too: the server asks nobody itself, so
// the rules are how a person gets asked, and a check that read none
// passes everything.
func checkPermissionRules(readme string, registered []string) []string {
	rules := permissionRule.FindAllStringSubmatch(readme, -1)
	if len(rules) == 0 {
		return []string{"README.md gives no Claude Code permission rule for this server (\"mcp__google-drive__...\")"}
	}
	var problems []string
	for _, m := range rules {
		if !slices.Contains(registered, m[1]) {
			problems = append(problems, "README.md gives a permission rule for "+m[1]+", which is not registered")
		}
	}
	return problems
}

// configDocPath is the page every setting is documented on.
const configDocPath = "docs/configuration.md"

// checkConfigDocs holds docs/configuration.md to every variable the
// server reads, which config.Vars records from the lookups themselves.
func checkConfigDocs(settings []string) []string {
	docs, err := os.ReadFile(configDocPath)
	if err != nil {
		return []string{"cannot read " + configDocPath + ": " + err.Error()}
	}
	return configDocProblems(settings, string(docs))
}

// configDocProblems holds the configuration page, text, to the variables
// the server reads, both ways: it names every one as a whole word, names
// no other, and says what a profile name is in the words the server
// refuses one with.
func configDocProblems(settings []string, text string) []string {
	documented := map[string]bool{}
	for _, at := range settingWord.FindAllStringSubmatchIndex(text, -1) {
		if word := text[at[2]:at[3]]; settingName.MatchString(word) && !strings.HasPrefix(text[at[3]:], "*") {
			documented[word] = true
		}
	}
	var problems []string
	for _, name := range settings {
		if !documented[name] {
			problems = append(problems, configDocPath+" does not document "+name)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(documented)) {
		if !slices.Contains(settings, name) {
			problems = append(problems, configDocPath+" documents "+name+", which the server does not read")
		}
	}
	if !strings.Contains(text, config.ProfileRule) {
		problems = append(problems, fmt.Sprintf("%s does not say a profile name is %q", configDocPath, config.ProfileRule))
	}
	return problems
}

// settingWord is a word that starts with the prefix, with no letter of
// either case, digit or _ before it, and all of it: a longer word that
// contains a name, such as GDRIVE_READ_ONLY_X, GDRIVE_LABELSx or
// xGDRIVE_LABELS, is not that name. The word is a setting's name when
// settingName matches it and no * follows, since GDRIVE_ENABLE_* is a
// pattern.
var (
	settingWord = regexp.MustCompile(`(?:^|[^A-Za-z0-9_])(` + config.EnvPrefix + `[A-Za-z0-9_]*)`)
	settingName = regexp.MustCompile(`^` + config.EnvPrefix + `[A-Z0-9_]+$`)
)

// versionHeading is a released version's heading in the changelog.
var versionHeading = regexp.MustCompile(`(?m)^## \[([0-9]+\.[0-9]+\.[0-9]+)\]`)

// checkChangelog holds the checkout at dir to changelogRule.
func checkChangelog(dir string) ([]string, error) {
	raw, err := os.ReadFile(filepath.Join(dir, changelogPath))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", changelogPath, err)
	}
	tagged := func(version string) bool { return tagExists(dir, "v"+version) }
	return changelogRule(string(raw), goFilesChanged(dir), tagged), nil
}

// changelogRule requires source changes to be written up. Normally that
// means content under [Unreleased]; on a release commit those notes have
// just moved under the new version heading, which is only acceptable
// while that version has no tag yet.
func changelogRule(text string, sourceChanged bool, tagged func(version string) bool) []string {
	if !sourceChanged {
		return nil
	}
	lines := strings.Split(text, "\n")
	if countEntries(lines, "Unreleased") > 0 {
		return nil
	}
	where := "[Unreleased]"
	if m := versionHeading.FindStringSubmatch(text); m != nil && !tagged(m[1]) {
		if countEntries(lines, m[1]) > 0 {
			return nil
		}
		where = "[" + m[1] + "] (untagged, so this is a release commit)"
	}
	return []string{"source changed but " + changelogPath + " " + where + " is empty"}
}

// countEntries counts the bullet points in the section for version of a
// changelog's lines.
func countEntries(lines []string, version string) int {
	from, to := changelogSection(lines, version)
	n := 0
	for _, line := range lines[from:to] {
		if strings.HasPrefix(strings.TrimSpace(line), "- ") {
			n++
		}
	}
	return n
}

// goFilesChanged reports whether Go source in the checkout at dir differs
// from the last tag, or from the commit before HEAD when there is none.
func goFilesChanged(dir string) bool {
	rev := "HEAD~1"
	if last := lastTag(dir); last != "" {
		rev = last
	}
	// A non-zero status means either a difference or no such revision;
	// on the very first commit there is nothing to compare against, and
	// requiring the notes then is the safe way round.
	return exec.Command("git", "-C", dir, "diff", "--quiet", rev, "--", "*.go").Run() != nil
}

func tagExists(dir, tag string) bool {
	return exec.Command("git", "-C", dir, "rev-parse", "-q", "--verify", "refs/tags/"+tag).Run() == nil
}

// checkScopesDocumented holds the README's setup step to the scopes
// login can actually request.
//
// A setup instruction that names fewer scopes than the code asks for is
// disprovable in one command, which for a setup step is the same as
// being wrong: the reader adds what they were told to, runs login, and
// is refused, on an External app, by a consent screen that does not
// carry the rest. The label and activity scopes are requested only with
// their features on, which is exactly why a missing one goes unnoticed.
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
					"scopes than the code asks for sends somebody with an External app to a consent "+
					"screen that will refuse them", m[1]))
		}
	}
	return problems
}

// scopeConstant matches the Scope* constants internal/auth declares.
var scopeConstant = regexp.MustCompile(`Scope[A-Za-z]*\s*=\s*"(https://www\.googleapis\.com/auth/[a-z.]+)"`)

// lastTag returns the most recent tag in the checkout at dir, or "" when
// there is none. Having no tags is the normal state before the first
// release, so it is not an error and this cannot fail.
func lastTag(dir string) string {
	out, err := exec.Command("git", "-C", dir, "describe", "--tags", "--abbrev=0").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
