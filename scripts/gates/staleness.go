package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path"
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
	binary := arg(args, 0, defaultBinary)
	// A CHANGELOG rule that cannot run holds back no other check.
	problems, unrun := checkChangelog(".")
	if unrun != nil && !errors.As(unrun, new(couldNotRun)) {
		return unrun
	}

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

	if len(problems) > 0 {
		for _, p := range problems {
			_, _ = fmt.Fprintln(out, p)
		}
		if unrun != nil {
			_, _ = fmt.Fprintln(out, unrun)
		}
		return fmt.Errorf("%d documentation problem(s)", len(problems))
	}
	if unrun != nil {
		return unrun
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
// tool that no longer exists. The table and the prose are read with fenced
// code blanked, since a row or a name in an example documents nothing.
func checkReadmeTools(registered []string) []string {
	readme, err := os.ReadFile("README.md")
	if err != nil {
		return []string{"cannot read README.md: " + err.Error()}
	}
	text := strings.Join(docsProse(string(readme)), "\n")
	documented := map[string]bool{}
	for _, m := range readmeToolRow.FindAllStringSubmatch(text, -1) {
		documented[m[1]] = true
	}
	var problems []string
	for _, name := range registered {
		if _, prose := documentedInProse[name]; prose {
			// Named in a paragraph on purpose. It still has to be MENTIONED
			// somewhere, which is checked below.
			if !strings.Contains(text, "`"+name+"`") {
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
	// The permission rules are a settings file a person copies, so they are
	// read from the fenced block they sit in.
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
// refuses one with. Fenced code is an example, which documents nothing.
func configDocProblems(settings []string, text string) []string {
	text = strings.Join(docsProse(text), "\n")
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

// checkChangelog holds the checkout at dir to changelogRule, measured from
// lastTag, the newest release HEAD reaches. No such tag, or a git or go
// call that cannot answer, is couldNotRun, never "source changed".
func checkChangelog(dir string) ([]string, error) {
	list, err := git("-C", dir, "tag")
	if err != nil {
		return nil, changelogUnrun(dir, err)
	}
	tags := strings.Fields(list)
	last, err := lastTag(dir, "HEAD")
	if err != nil {
		return nil, changelogUnrun(dir, err)
	}
	if last == "" {
		return nil, changelogUnrun(dir, errors.New("no release tag (vX.Y.Z) HEAD reaches is in the checkout to measure from"))
	}
	changed, err := shippedChangedSince(dir, last)
	if err != nil {
		return nil, changelogUnrun(dir, err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, changelogPath))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", changelogPath, err)
	}
	tagged := func(version string) bool { return slices.Contains(tags, "v"+version) }
	return changelogRule(string(raw), changed, tagged), nil
}

// changelogUnrun is why the CHANGELOG rule could not run in the checkout at
// dir. A shallow clone fetches no tag and too little history, so the
// reason names one.
func changelogUnrun(dir string, err error) error {
	if shallow, _ := git("-C", dir, "rev-parse", "--is-shallow-repository"); shallow == "true" {
		err = fmt.Errorf("%w; the checkout is a shallow clone: check it out with fetch-depth: 0", err)
	}
	return couldNotRun{fmt.Errorf("the CHANGELOG rule: %w", err)}
}

// changelogRule requires a change to the binary's source to be written
// up. Normally that means entries under [Unreleased]; on a release commit
// those notes have just moved under the new version heading, which is only
// acceptable while that version has no tag yet.
func changelogRule(text string, sourceChanged bool, tagged func(version string) bool) []string {
	if !sourceChanged {
		return nil
	}
	doc := readChangelog(text)
	if doc.entries("Unreleased") > 0 {
		return nil
	}
	where := "[Unreleased]"
	if v := doc.versions(); len(v) > 0 && !tagged(v[0].label) {
		if doc.entries(v[0].label) > 0 {
			return nil
		}
		where = "[" + v[0].label + "] (untagged, so this is a release commit)"
	}
	return []string{"the binary's source changed but " + changelogPath + " " + where + " is empty"}
}

// lastTag is the newest release rev reaches in the checkout at dir: of the
// tags on its history, the highest vX.Y.Z by semver, a prerelease included,
// so a newer release on another line is not rev's. It is empty when rev
// reaches no release, which is a state, not a failure; a git error is one.
func lastTag(dir, rev string) (string, error) {
	tags, err := git("-C", dir, "tag", "--merged", rev)
	if err != nil {
		return "", err
	}
	if v := stalenessNewestRelease(strings.Fields(tags)); v != "" {
		return "v" + v, nil
	}
	return "", nil
}

// stalenessNewestRelease is the newest release tag's version by semver
// order, a prerelease included, or "" when there is none. git's version
// sort puts v2.0.0-rc.1 after v2.0.0, so the order is semver's.
func stalenessNewestRelease(tags []string) string {
	newest := ""
	for _, tag := range tags {
		v, ok := strings.CutPrefix(tag, "v")
		if ok && changelogVersion.MatchString(v) && (newest == "" || changelogCompare(v, newest) > 0) {
			newest = v
		}
	}
	return newest
}

// shippedChangedSince reports whether the commits since..HEAD of the
// checkout at dir change what users get: a file shippedSource has, or
// go.mod outside its require and tool lines. Commits are compared, as CI
// sees them.
func shippedChangedSince(dir, since string) (bool, error) {
	out, err := git("-C", dir, "diff", since+"..HEAD", "--name-only", "--no-renames", "-z")
	if err != nil {
		return false, err
	}
	var shipped *shippedSource
	for _, file := range strings.Split(out, "\x00") {
		switch file {
		case "":
		case "go.mod":
			if changed, err := goModChangedSince(dir, since); err != nil || changed {
				return changed, err
			}
		default:
			if shipped == nil {
				if shipped, err = listShipped(dir); err != nil {
					return false, err
				}
			}
			if shipped.has(file) {
				return true, nil
			}
		}
	}
	return false, nil
}

// shippedSource is what the binary is built from in this module, on this
// platform: its package directories and the files they embed, each
// slash-separated from the module root.
type shippedSource struct{ dirs, embeds map[string]bool }

// has reports whether a file is one users get: any file but a test in one
// of the binary's package directories, an embedded file, or the bundle's
// manifest or launcher. The README and LICENSE the bundle carries are
// docs, so they count for nothing. Every file of a package directory
// counts: the go command builds a package from assembly, a .syso and C as
// well as Go (`go help filetype`). A package
// counts by its directory, so a file of it another platform builds counts,
// and so does one removed from it. The packages are the host's, in the
// checkout: a package only another platform's files import, or a directory
// the head removes whole, is not one.
func (s *shippedSource) has(file string) bool {
	return file == mcpbManifestPath || file == alongside[linuxLauncher] || s.embeds[file] ||
		!strings.HasSuffix(file, "_test.go") && s.dirs[path.Dir(file)]
}

// listShipped asks the go command what ./cmd/... is built from in the
// module at dir. A package of another module is a dependency, which
// go.mod's require lines name.
func listShipped(dir string) (*shippedSource, error) {
	const what = "go list -deps ./cmd/..."
	cmd := exec.Command("go", "list", "-deps", "-json=Dir,EmbedFiles,Module", "./cmd/...")
	cmd.Dir = dir
	out, err := cmd.Output()
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		return nil, fmt.Errorf("%s: %s", what, strings.TrimSpace(string(exit.Stderr)))
	case err != nil:
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	s := &shippedSource{dirs: map[string]bool{}, embeds: map[string]bool{}}
	for dec := json.NewDecoder(bytes.NewReader(out)); dec.More(); {
		var p struct {
			Dir        string
			EmbedFiles []string
			Module     *struct {
				Main bool
				Dir  string
			}
		}
		if err := dec.Decode(&p); err != nil {
			return nil, fmt.Errorf("%s: %w", what, err)
		}
		if p.Module == nil || !p.Module.Main {
			continue
		}
		rel, err := filepath.Rel(p.Module.Dir, p.Dir)
		if err != nil {
			return nil, err
		}
		pkg := filepath.ToSlash(rel)
		s.dirs[pkg] = true
		for _, f := range p.EmbedFiles {
			s.embeds[path.Join(pkg, f)] = true
		}
	}
	// An empty listing is not reading the binary, and would excuse every
	// change.
	if len(s.dirs) == 0 {
		return nil, fmt.Errorf("%s named no package of this module, so it is not reading the binary", what)
	}
	return s, nil
}

// goModChangedSince reports whether go.mod at HEAD in the checkout at dir
// differs from since's outside its require and tool lines: a go, toolchain
// or replace line changes what the binary is built with.
func goModChangedSince(dir, since string) (bool, error) {
	before, err := git("-C", dir, "show", since+":go.mod")
	if err != nil {
		return false, err
	}
	after, err := git("-C", dir, "show", "HEAD:go.mod")
	if err != nil {
		return false, err
	}
	return goModBuildLines(before) != goModBuildLines(after), nil
}

// goModBuildLines is a go.mod's lines, each trimmed and the blank ones
// dropped, without its require and tool directives, a block's included
// (https://go.dev/ref/mod#go-mod-file-require). A tool directive adds a
// package `go tool` runs (https://go.dev/ref/mod#go-mod-file-tool). ( is a
// token of its own, so `require(` opens a block as `require (` does
// (https://go.dev/ref/mod#go-mod-file-lexical).
func goModBuildLines(text string) string {
	var kept []string
	inBlock := false
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		verb, paren := "", false
		if len(fields) > 0 {
			verb, _, paren = strings.Cut(fields[0], "(")
		}
		switch {
		case inBlock:
			inBlock = len(fields) == 0 || !strings.HasPrefix(fields[0], ")")
		case len(fields) == 0:
		case verb == "require" || verb == "tool":
			inBlock = paren || len(fields) > 1 && strings.HasPrefix(fields[1], "(")
		default:
			kept = append(kept, strings.Join(fields, " "))
		}
	}
	return strings.Join(kept, "\n")
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
