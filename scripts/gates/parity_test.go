package main

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
)

// parityFiles are the files parity reads, by the path a row edits.
var parityFiles = []string{
	fxMakefile, parityGoreleaser, fxCI, fxRelease, fxPublish, fxCodeQL, workflowDir + "/assign-issues.yml",
}

// parityHookFile names the hook among a row's files; the gate reads it
// from precommitHook.
const parityHookFile = "pre-commit hook"

// parityRepoRoot is the repository, made absolute as the package loads,
// before a test can change directory.
var parityRepoRoot = func() string {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		panic(err)
	}
	return root
}()

// repoFile is the repository's file at the slash-separated path name, as
// text.
func repoFile(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(parityRepoRoot, filepath.FromSlash(name)))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(data)
}

// parityRepo is the repository's own parity inputs, as text, with the
// pre-commit hook under its own name.
func parityRepo(t *testing.T) map[string]string {
	t.Helper()
	files := map[string]string{parityHookFile: precommitHook}
	for _, f := range parityFiles {
		files[f] = repoFile(t, f)
	}
	return files
}

// parityRepoTags is what the tree's //go:build lines need, read once: every
// row reads the same tree.
var parityRepoTags = sync.OnceValues(func() ([]parityTagNeed, error) {
	files, err := docsFiles(parityRepoRoot)
	if err != nil {
		return nil, err
	}
	return parityTreeTags(parityRepoRoot, files)
})

// parityRun runs the gate over files, with the tree's tags and those of
// the extra Go files, and returns its problems, sorted.
func parityRun(t *testing.T, files, extraGo map[string]string) []string {
	t.Helper()
	r := parityCheck(parityInputsOf(t, files, extraGo), commands)
	sort.Strings(r.problems)
	return r.problems
}

// parityInputsOf is the gate's inputs read from files, with the tree's
// tags and those of the extra Go files.
func parityInputsOf(t *testing.T, files, extraGo map[string]string) parityInputs {
	t.Helper()
	tree, err := parityRepoTags()
	if err != nil {
		t.Fatal(err)
	}
	needs := slices.Clone(tree)
	if extraGo != nil {
		more, err := parityTreeTags(docsTree(t, extraGo), slices.Sorted(maps.Keys(extraGo)))
		if err != nil {
			t.Fatal(err)
		}
		needs = append(needs, more...)
	}
	var flows []workflowFile
	for _, f := range parityFiles {
		if text, ok := files[f]; ok && strings.HasPrefix(f, workflowDir) {
			flows = append(flows, parseWorkflow(f, text))
		}
	}
	return parityInputs{mk: parseMakefile(files[fxMakefile]), flows: flows,
		goreleaser: parseGoreleaser(files[parityGoreleaser]), hook: files[parityHookFile], needs: needs, prereqOnly: parityPrereqOnly}
}

type parityCase struct {
	name  string
	edits []docsEdit
	// extraGo are Go files whose //go:build lines join the tree's.
	extraGo map[string]string
	want    []string
}

func checkParityCases(t *testing.T, cases []parityCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parityRun(t, applyEdits(t, parityRepo(t), tc.edits...), tc.extraGo)
			if !slices.Equal(got, tc.want) {
				t.Errorf("problems:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(tc.want, "\n  "))
			}
		})
	}
}

// dropCheck removes one prerequisite from the Makefile's `check:` line.
// Editing the whole file by substring would hit the target's own
// definition first and leave `check:` untouched.
func dropCheck(t *testing.T, files map[string]string, target string) {
	t.Helper()
	for _, line := range strings.Split(files[fxMakefile], "\n") {
		if !strings.HasPrefix(line, "check:") {
			continue
		}
		fields := strings.Fields(line)
		kept := slices.DeleteFunc(slices.Clone(fields), func(f string) bool { return f == target })
		if len(kept) == len(fields) {
			t.Fatalf("`check:` does not list %q, so removing it proves nothing", target)
		}
		files[fxMakefile] = strings.Replace(files[fxMakefile], line, strings.Join(kept, " "), 1)
		return
	}
	t.Fatal("no `check:` line in the Makefile")
}

// TestParityAcceptsThisRepository is the gate run against the real
// files, so a drift introduced later fails here as well as in CI, and the
// failures below prove more than that it fails on everything.
func TestParityAcceptsThisRepository(t *testing.T) {
	if got := parityRun(t, parityRepo(t), nil); got != nil {
		t.Errorf("the repository as it stands:\n  %s", strings.Join(got, "\n  "))
	}
	needs, err := parityRepoTags()
	if err != nil {
		t.Fatal(err)
	}
	if len(needs) != 1 || needs[0].String() != "-tags=integration" || needs[0].file != "internal/service/integration_test.go" {
		t.Errorf("the tree's tags = %v, want one need of -tags=integration", needs)
	}
	t.Chdir("../..")
	var out strings.Builder
	if err := parity(&out, nil); err != nil {
		t.Errorf("parity = %v\n%s", err, out.String())
	}
}

// TestParityCatchesDriftInEitherDirection is the point of the gate: the
// two lists live in different files, and whichever one you are editing
// is the one you remember. Each row is a drift, named.
func TestParityCatchesDriftInEitherDirection(t *testing.T) {
	const tagged = "internal/service/integration_test.go builds only with -tags=integration (//go:build integration) and "
	const gitleaks = "`make gitleaks` runs \"go run github.com/zricethezav/gitleaks/v8@v8.30.1 git --log-opts=HEAD . --config .gitleaks.toml --redact --no-banner\""
	stale := "      - run: go run ./scripts/gates staleness ./google-drive-mcp\n"
	ack := `run: go run ./scripts/gates schema-ack "$PR_BASE_REF" "$PR_HEAD"`
	for _, tc := range []struct {
		name   string
		remove string
		edits  []docsEdit
		want   []string
	}{
		{name: "CI does not vet the build-tagged code", edits: []docsEdit{
			replace(fxCI, "      - run: go vet -tags=integration ./...\n", ""),
		}, want: []string{
			"`make vet` runs \"go vet -tags=integration ./...\" and ci.yml never does",
			tagged + "ci.yml vets no pass that builds it; it compiles only on a maintainer's machine",
		}},
		{name: "make check does not scan for vulnerabilities", remove: "vuln", want: []string{
			".github/workflows/ci.yml:92 runs `go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./...` and `make check` does not",
		}},
		{name: "a gate runs in CI and not in make check", remove: "staleness", want: []string{
			"the staleness gate runs in make check by the dispatch, and no target `make check` runs calls it",
		}},
		{name: "a gate runs in make check and not in CI", edits: []docsEdit{replace(fxCI, stale, "      - run: true\n")},
			want: []string{"the staleness gate runs in make check by the dispatch, and ci.yml never calls it"}},
		{name: "a gate nothing runs", remove: "staleness", edits: []docsEdit{replace(fxCI, stale, "      - run: true\n")},
			want: []string{
				"the staleness gate runs in make check by the dispatch, and ci.yml never calls it",
				"the staleness gate runs in make check by the dispatch, and no target `make check` runs calls it",
			}},
		{name: "the pull-request gate is gone from CI", edits: []docsEdit{replace(fxCI, ack, "run: true")},
			want: []string{"the schema-ack gate runs on pull requests by the dispatch, and ci.yml's pr job never calls it"}},
		{name: "the pull-request gate commented out", edits: []docsEdit{replace(fxCI, "        "+ack, "        # "+ack)},
			want: []string{"the schema-ack gate runs on pull requests by the dispatch, and ci.yml's pr job never calls it"}},
		{name: "the pull-request gate moved out of the pr job", edits: []docsEdit{
			replace(fxCI, ack, "run: true"),
			replace(fxCI, "run: go run ./scripts/gates docs\n", "run: go run ./scripts/gates docs\n      - run: go run ./scripts/gates schema-ack origin/main HEAD\n"),
		}, want: []string{"the schema-ack gate runs on pull requests by the dispatch, and ci.yml's pr job never calls it"}},
		{name: "the pull-request gate in make check", edits: []docsEdit{
			replace(fxMakefile, "check: fmt", "check: schema-ack fmt"),
		}, want: []string{
			"the schema-ack gate runs on pull requests by the dispatch, and `make check` runs it, where there is no merge-base",
		}},
		{name: "the linter's action gone from CI", edits: []docsEdit{
			replace(fxCI, "      - uses: golangci/golangci-lint-action@", "      - uses: some/other-action@"),
		}, want: []string{
			"`make lint` runs \"go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run\", " +
				"and ci.yml neither runs it nor uses golangci/golangci-lint-action, which stands in for it",
		}},
		{name: "a check only CI runs", edits: []docsEdit{
			replace(fxCI, "      - name: licenses\n", "      - run: go run honnef.co/go/tools/cmd/staticcheck@v0.6.1 ./...\n      - name: licenses\n"),
		}, want: []string{".github/workflows/ci.yml:93 runs `go run honnef.co/go/tools/cmd/staticcheck@v0.6.1 ./...` and `make check` does not"}},
		{name: "CI runs a tool at another version", edits: []docsEdit{
			replace(fxCI, "govulncheck@v1.7.0", "govulncheck@v1.7.1"),
		}, want: []string{
			".github/workflows/ci.yml:92 runs `go run golang.org/x/vuln/cmd/govulncheck@v1.7.1 ./...` and `make check` does not",
			"`make vuln` runs \"go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./...\" and ci.yml never does",
		}},
		{name: "release.yml writes no release notes", edits: []docsEdit{
			replace(fxRelease, `run: go run ./scripts/gates release-notes "${REF_NAME}" > "${RUNNER_TEMP}/release-notes.md"`, "run: true"),
		}, want: []string{
			"the release-notes gate runs in release by the dispatch, and neither .goreleaser.yaml nor release.yml and the workflows it calls call it",
		}},
		// The release is release.yml and what it calls: a release gate any
		// other workflow calls is not the release's.
		{name: "release notes written by another workflow", edits: []docsEdit{
			replace(fxRelease, `run: go run ./scripts/gates release-notes "${REF_NAME}" > "${RUNNER_TEMP}/release-notes.md"`, "run: true"),
			replace(fxCodeQL, "          languages: go\n", "          languages: go\n      - run: go run ./scripts/gates release-notes v1.0.0\n"),
		}, want: []string{
			"the release-notes gate runs in release by the dispatch, and neither .goreleaser.yaml nor release.yml and the workflows it calls call it",
		}},
		{name: "goreleaser packs no bundle", edits: []docsEdit{
			replace(parityGoreleaser, "post: go run ./scripts/gates mcpb-pack {{ .Version }} dist", "post: true"),
		}, want: []string{
			"the mcpb-pack gate runs in release by the dispatch, and neither .goreleaser.yaml nor release.yml and the workflows it calls call it",
		}},
		// The footer is release-note prose, which the release does not run.
		{name: "goreleaser names the bundle's gate only in its footer", edits: []docsEdit{
			replace(parityGoreleaser, "post: go run ./scripts/gates mcpb-pack {{ .Version }} dist", "post: true"),
			replace(parityGoreleaser, "  footer: |\n", "  footer: |\n    go run ./scripts/gates mcpb-pack {{ .Version }} dist\n"),
		}, want: []string{
			"the mcpb-pack gate runs in release by the dispatch, and neither .goreleaser.yaml nor release.yml and the workflows it calls call it",
		}},
		{name: "goreleaser runs the bundle's gate through an alias", edits: []docsEdit{
			replace(parityGoreleaser, "post: go run ./scripts/gates mcpb-pack {{ .Version }} dist", "post: *pack"),
		}, want: []string{
			".goreleaser.yaml:43: post: an anchor, an alias or a tag; write the value out",
			"the mcpb-pack gate runs in release by the dispatch, and neither .goreleaser.yaml nor release.yml and the workflows it calls call it",
		}},
		{name: "a manual gate nothing runs", edits: []docsEdit{
			replace(fxMakefile, "\t$(GO) run ./scripts/gates api-diff\n", "\t@true\n"),
		}, want: []string{
			"the api-diff gate is manual by the dispatch, and no Makefile target or hook runs it: " +
				"a manual gate nobody can find is a gate that never fires",
		}},
		{name: "the hook runs no gate", edits: []docsEdit{
			replace(parityHookFile, "exec go run ./scripts/gates precommit", "exit 0"),
		}, want: []string{
			"the precommit gate is manual by the dispatch, and no Makefile target or hook runs it: " +
				"a manual gate nobody can find is a gate that never fires",
		}},
		{name: "a manual gate in make check", edits: []docsEdit{
			replace(fxMakefile, "\t$(GO) run ./scripts/gates docs\n", "\t$(GO) run ./scripts/gates docs\n\t$(GO) run ./scripts/gates api-diff\n"),
		}, want: []string{"the api-diff gate is manual by the dispatch, and `make check` runs it; record it as inCheck"}},
		{name: "a gate the dispatch does not have", edits: []docsEdit{
			replace(fxMakefile, "\t$(GO) run ./scripts/gates docs\n", "\t$(GO) run ./scripts/gates docs\n\t$(GO) run ./scripts/gates ghost\n"),
		}, want: []string{"something runs `gates ghost`, which the dispatch does not have"}},
		// Every workflow but ci.yml is read for gate calls, not a list of them.
		{name: "a gate the dispatch does not have, in another workflow", edits: []docsEdit{
			replace(fxCodeQL, "          languages: go\n", "          languages: go\n      - run: go run ./scripts/gates ghost\n"),
		}, want: []string{"something runs `gates ghost`, which the dispatch does not have"}},
		{name: "a prerequisite a second check rule adds", edits: []docsEdit{
			replace(fxMakefile, ".PHONY: clean\n", "check: ghostcheck\n\n.PHONY: clean\n"),
		}, want: []string{"check: names ghostcheck, which the Makefile does not define"}},
		{name: "the secret scan gone from CI", edits: []docsEdit{replace(fxCI, "run: make gitleaks\n", "run: true\n")},
			want: []string{gitleaks + " and ci.yml never does"}},
		// A variable set on the line, or a flag, changes what the recipe runs.
		{name: "CI makes the secret scan with its version set", edits: []docsEdit{
			replace(fxCI, "run: make gitleaks\n", "run: make gitleaks GITLEAKS=github.com/zricethezav/gitleaks/v8@v8.30.1\n"),
		}, want: []string{gitleaks + " and ci.yml never does"}},
		// A redirection changes where the output goes, not what runs.
		{name: "CI makes the secret scan with its output redirected", edits: []docsEdit{
			replace(fxCI, "run: make gitleaks\n", "run: make gitleaks 2>&1 > gitleaks.log\n"),
		}},
		// make runs what a target depends on first.
		{name: "CI makes a target whose prerequisite runs a check make check does not", edits: []docsEdit{
			replace(fxMakefile, ".PHONY: check\n", "extra: static\nstatic:\n\t$(GO) run honnef.co/go/tools/cmd/staticcheck@v0.6.1 ./...\n\n.PHONY: check\n"),
			replace(fxCI, "run: make gitleaks\n", "run: make gitleaks extra\n"),
		}, want: []string{".github/workflows/ci.yml:124 runs `go run honnef.co/go/tools/cmd/staticcheck@v0.6.1 ./...` and `make check` does not"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := applyEdits(t, parityRepo(t), tc.edits...)
			if tc.remove != "" {
				dropCheck(t, files, tc.remove)
			}
			if got := parityRun(t, files, nil); !slices.Equal(got, tc.want) {
				t.Errorf("problems:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(tc.want, "\n  "))
			}
		})
	}
}

// Commenting a step out, naming a gate in a step's name or env, or quoting
// it in an echo is not the same as running it; a flag before the gates
// package does not hide one.
func TestParityReadsWhatRuns(t *testing.T) {
	const notInCI = "the staleness gate runs in make check by the dispatch, and ci.yml never calls it"
	stale := "      - run: go run ./scripts/gates staleness ./google-drive-mcp\n"
	checkParityCases(t, []parityCase{
		{"a gate's step commented out", []docsEdit{
			replace(fxCI, stale, "      # - run: go run ./scripts/gates staleness ./google-drive-mcp\n"),
		}, nil, []string{notInCI}},
		{"a gate named in a step's name", []docsEdit{
			replace(fxCI, stale, "      - name: go run ./scripts/gates staleness ./google-drive-mcp\n        run: true\n"),
		}, nil, []string{notInCI}},
		{"a gate named in a step's env", []docsEdit{
			replace(fxCI, stale, "      - name: check\n        run: true\n        env:\n          NOTE: go run ./scripts/gates staleness\n"),
		}, nil, []string{notInCI}},
		{"a gate quoted in an echo", []docsEdit{
			replace(fxCI, stale, "      - run: echo \"go run ./scripts/gates staleness ./google-drive-mcp\"\n"),
		}, nil, []string{notInCI}},
		{"a gate inside a run block, after another command", []docsEdit{
			replace(fxCI, stale, "      - run: |\n          set -e\n          go run ./scripts/gates staleness ./google-drive-mcp\n"),
		}, nil, nil},
		{"a gate named by the step after a run block", []docsEdit{
			replace(fxCI, stale, "      - run: |\n          set -e\n      - name: go run ./scripts/gates staleness ./google-drive-mcp\n        run: true\n"),
		}, nil, []string{notInCI}},
		{"a gate run past its build flags, and in a command substitution", []docsEdit{
			replace(fxCI, "run: go run ./scripts/gates staleness", "run: go run -trimpath ./scripts/gates staleness"),
			replace(fxRelease, `run: go run ./scripts/gates release-notes "${REF_NAME}" > "${RUNNER_TEMP}/release-notes.md"`,
				`run: NOTES="$(go run ./scripts/gates release-notes "${REF_NAME}")"`),
		}, nil, nil},
		{"a target named only in a Makefile comment", []docsEdit{
			replace(fxMakefile, "check: fmt", "# check: vuln\ncheck: fmt"),
			replace(fxMakefile, " vuln licenses", " licenses"),
		}, nil, []string{
			".github/workflows/ci.yml:92 runs `go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./...` and `make check` does not",
		}},
		{"a recipe spaced and quoted otherwise than CI's line", []docsEdit{
			replace(fxMakefile, "\t$(GO) vet ./...\n", "\t$(GO)  vet './...'\n"),
		}, nil, nil},
		{"a recipe line's prefixes, which are not part of the command", []docsEdit{
			replace(fxMakefile, "\t$(GO) vet -tags=integration ./...\n", "\t@+$(GO) vet -tags=integration ./...\n"),
		}, nil, nil},
		{"a Makefile line the reader does not read", []docsEdit{
			replace(fxMakefile, "GO       ?= go\n", "ifndef GO\nGO       ?= go\nendif\n"),
		}, nil, []string{
			`Makefile:3: "ifndef GO" is a directive or an assignment, which this reader does not read`,
			`Makefile:5: "endif" is a directive or an assignment, which this reader does not read`,
		}},
		{"a step the workflow reader does not read", []docsEdit{
			replace(fxCI, stale, "      - {name: staleness, run: go run ./scripts/gates staleness ./google-drive-mcp}\n"),
		}, nil, []string{
			".github/workflows/ci.yml:165: an item of steps: is not a block mapping; write it one key a line",
			notInCI,
		}},
		{"a gate named only in a recipe's comment", []docsEdit{
			replace(fxMakefile, "\t$(GO) run ./scripts/gates docs\n", "\t$(GO) run ./scripts/gates pins # was: $(GO) run ./scripts/gates docs\n"),
		}, nil, []string{"the docs gate runs in make check by the dispatch, and no target `make check` runs calls it"}},
		{"a command beside a gate call on one recipe line", []docsEdit{
			replace(fxMakefile, "\t$(GO) run ./scripts/gates docs\n", "\t$(GO) run ./scripts/gates docs && $(GO) vet -tags=foo ./...\n"),
		}, nil, []string{"`make docs` runs \"go vet -tags=foo ./...\" and ci.yml never does"}},
		{"a command beside a gate call on one CI line", []docsEdit{
			replace(fxCI, "run: go run ./scripts/gates docs\n", "run: go run ./scripts/gates docs && go vet ./...\n"),
		}, nil, nil},
	})
}

// A gate both sides call takes the same arguments on both, the Makefile's
// variables expanded. An omitted [BINARY] is the binary the gate defaults
// to, and no other omitted argument is.
func TestParityHoldsAGateCallsArguments(t *testing.T) {
	checkParityCases(t, []parityCase{
		{"a coverage floor the Makefile sets apart from CI's", []docsEdit{
			replace(fxMakefile, "COVER_MIN ?= 80", "COVER_MIN ?= 85"),
		}, nil, []string{
			".github/workflows/ci.yml:71 calls the coverage gate with `cov.out 80` and `make check` never does",
			"`make cover` calls the coverage gate with `cov.out 85` and ci.yml never does",
		}},
		// Only the binary compares as a path; every other argument is a word.
		{"a profile written another way", []docsEdit{
			replace(fxMakefile, "coverage cov.out $(COVER_MIN)", "coverage ./cov.out $(COVER_MIN)"),
		}, nil, []string{
			".github/workflows/ci.yml:71 calls the coverage gate with `cov.out 80` and `make check` never does",
			"`make cover` calls the coverage gate with `./cov.out 80` and ci.yml never does",
		}},
		{"a binary other than the one the gate defaults to", []docsEdit{
			replace(fxMakefile, "\t$(GO) run ./scripts/gates smoke $(BIN)\n", "\t$(GO) run ./scripts/gates smoke ./old/google-drive-mcp\n"),
		}, nil, []string{
			".github/workflows/ci.yml:163 calls the smoke gate with `./google-drive-mcp` and `make check` never does",
			"`make smoke` calls the smoke gate with `./old/google-drive-mcp` and ci.yml never does",
		}},
		{"a gate call whose output is redirected", []docsEdit{
			replace(fxCI, "run: go run ./scripts/gates smoke ./google-drive-mcp\n",
				"run: go run ./scripts/gates smoke ./google-drive-mcp 2>&1 | tee smoke.log\n"),
		}, nil, nil},
		{"the default binary left out in CI", []docsEdit{
			replace(fxCI, "run: go run ./scripts/gates smoke ./google-drive-mcp\n", "run: go run ./scripts/gates smoke\n"),
		}, nil, nil},
		// exec looks a name with no slash up on PATH, so a bare name is
		// another binary than ./ before it.
		{"a bare binary name in CI", []docsEdit{
			replace(fxCI, "run: go run ./scripts/gates smoke ./google-drive-mcp\n", "run: go run ./scripts/gates smoke google-drive-mcp\n"),
		}, nil, []string{
			".github/workflows/ci.yml:163 calls the smoke gate with `google-drive-mcp` and `make check` never does",
			"`make smoke` calls the smoke gate with `./google-drive-mcp` and ci.yml never does",
		}},
		{"the binary written as a path that cleans to it", []docsEdit{
			replace(fxCI, "run: go run ./scripts/gates smoke ./google-drive-mcp\n", "run: go run ./scripts/gates smoke ./cmd/../google-drive-mcp\n"),
		}, nil, nil},
		{"the history scan in CI and the tree scan in make check", []docsEdit{
			replace(fxCI, "run: go run ./scripts/gates leaks\n", "run: go run ./scripts/gates leaks history\n"),
		}, nil, []string{
			".github/workflows/ci.yml:128 calls the leaks gate with `history` and `make check` never does",
			"`make leaks` calls the leaks gate with no arguments and ci.yml never does",
		}},
	})
}

// make carries on past a failure that a - prefix or .IGNORE tells it to
// ignore, so a command `make check` runs that way passes where CI, which
// runs it bare, fails. The command is still CI's in the same words.
func TestParityRefusesACommandWhoseFailureMakeIgnores(t *testing.T) {
	const vuln = "\t$(GO) run $(GOVULNCHECK) ./...\n"
	checkParityCases(t, []parityCase{
		{name: "a - before a check's command", edits: []docsEdit{replace(fxMakefile, vuln, "\t-$(GO) run $(GOVULNCHECK) ./...\n")},
			want: []string{"`make vuln` runs \"go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./...\" behind a - prefix: " +
				"make ignores this command's failure"}},
		{name: "a - among the other prefixes, before a gate call", edits: []docsEdit{
			replace(fxMakefile, "\t$(GO) run ./scripts/gates docs\n", "\t@ -$(GO) run ./scripts/gates docs\n"),
		}, want: []string{"`make docs` runs \"go run ./scripts/gates docs\" behind a - prefix: make ignores this command's failure"}},
		{name: "a - before a command of a prerequisite check reaches", edits: []docsEdit{
			replace(fxMakefile, "\tCGO_ENABLED=0 $(GO) build ", "\t-CGO_ENABLED=0 $(GO) build "),
		}, want: []string{"`make build` runs \"CGO_ENABLED=0 go build -trimpath -ldflags=\\\"-s -w -X github.com/mmedum/google-drive-mcp/v3/internal/version.Version=dev\\\" " +
			"-o ./google-drive-mcp ./cmd/google-drive-mcp\" behind a - prefix: make ignores this command's failure"}},
		{name: "a - before a command make check does not run", edits: []docsEdit{
			replace(fxMakefile, "\t$(GO) run ./scripts/gates api-diff\n", "\t-$(GO) run ./scripts/gates api-diff\n"),
		}},
		{name: ".IGNORE naming a check target", edits: []docsEdit{
			replace(fxMakefile, ".PHONY: check\n", ".IGNORE: install vuln\n\n.PHONY: check\n"),
		}, want: []string{".IGNORE names vuln: make ignores the failure of every command `make vuln` runs"}},
		{name: ".IGNORE with no prerequisite", edits: []docsEdit{replace(fxMakefile, ".PHONY: check\n", ".IGNORE:\n\n.PHONY: check\n")},
			want: []string{".IGNORE names no target: make ignores the failure of every command"}},
		{name: ".IGNORE naming a target make check does not run", edits: []docsEdit{
			replace(fxMakefile, ".PHONY: check\n", ".IGNORE: install\n\n.PHONY: check\n"),
		}},
	})
}

// What `make check` runs, ci.yml runs on a push to main as well. Its
// pull-request job skips a push, so a check only it runs never runs on
// main.
func TestParityHoldsMakeCheckToWhatAPushRuns(t *testing.T) {
	const prOnly = "only in its pr job, which a push to main does not run"
	const tagged = "internal/service/integration_test.go builds only with -tags=integration (//go:build integration) and "
	ack := "        run: go run ./scripts/gates schema-ack \"$PR_BASE_REF\" \"$PR_HEAD\"\n"
	docs := "      - run: go run ./scripts/gates docs\n"
	gitleaks := "      - name: gitleaks\n        run: make gitleaks\n"
	vet := "      - run: go vet -tags=integration ./...\n"
	lint := "      - uses: golangci/golangci-lint-action@ba0d7d2ec06a0ea1cb5fa41b2e4a3ab91d21278a # v9.3.0\n" +
		"        with:\n          version: v2.14.0\n          install-mode: goinstall\n"
	moved := func(step string) []docsEdit {
		return []docsEdit{replace(fxCI, step, ""), replace(fxCI, ack, ack+step)}
	}
	checkParityCases(t, []parityCase{
		{"a check gate moved into the pr job", moved(docs), nil, []string{
			"the docs gate runs in make check by the dispatch, and ci.yml calls it " + prOnly,
		}},
		{"a make target moved into the pr job", moved(gitleaks), nil, []string{
			"`make gitleaks` runs \"go run github.com/zricethezav/gitleaks/v8@v8.30.1 git --log-opts=HEAD . --config .gitleaks.toml " +
				"--redact --no-banner\", and ci.yml runs it " + prOnly,
		}},
		{"a vet pass moved into the pr job", moved(vet), nil, []string{
			"`make vet` runs \"go vet -tags=integration ./...\", and ci.yml runs it " + prOnly,
			tagged + "ci.yml vets no pass that builds it; it compiles only on a maintainer's machine",
		}},
		{"the linter's action moved into the pr job", moved(lint), nil, []string{
			"`make lint` runs \"go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run\", and ci.yml runs it " + prOnly,
		}},
	})
}

// The release is release.yml and every workflow of this repository it
// calls, and they call in turn, each once.
func TestTheReleaseIsReleaseYMLAndWhatItCalls(t *testing.T) {
	flows := make([]workflowFile, 0, 4)
	for name, text := range map[string]string{
		"release.yml": "jobs:\n  a:\n    uses: ./.github/workflows/publish.yml\n  b:\n    uses: ./.github/workflows/publish.yml\n" +
			"  c:\n    uses: o/r/.github/workflows/remote.yml@" + pinSHA + " # v1\n  d:\n    uses: ./.github/workflows/gone.yml\n",
		"publish.yml": "jobs:\n  a:\n    uses: ./.github/workflows/sign.yml\n",
		"sign.yml":    "jobs:\n  a:\n    uses: ./.github/workflows/release.yml\n",
		"ci.yml":      "jobs:\n  a:\n    runs-on: x\n",
	} {
		flows = append(flows, parseWorkflow(workflowDir+"/"+name, text))
	}
	release := releaseWorkflows(flows)
	got := make([]string, 0, len(release))
	for i := range release {
		got = append(got, release[i].path)
	}
	want := []string{fxRelease, workflowDir + "/publish.yml", workflowDir + "/sign.yml"}
	if !slices.Equal(got, want) {
		t.Errorf("releaseWorkflows = %q, want %q", got, want)
	}
}

// The tags are the checkout's as git lists it: a scratch file git ignores
// cannot change the verdict.
func TestParityReadsTheTagsOfTheCheckout(t *testing.T) {
	isolateGit(t)
	dir := t.TempDir()
	t.Chdir(dir)
	for name, body := range map[string]string{
		".gitignore":                  "/scratch/\n",
		fxMakefile:                    "check:\n",
		fxCI:                          "jobs:\n",
		parityGoreleaser:              "version: 2\n",
		"internal/probe/live_test.go": "//go:build live\n\npackage probe\n",
		"scratch/soak_test.go":        "//go:build soak\n\npackage scratch\n",
	} {
		write(t, filepath.FromSlash(name), body)
	}
	gitIn(t, "init", "-q", "-b", "main")
	in, err := readParityInputs()
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(in.needs))
	for _, n := range in.needs {
		got = append(got, n.file+": "+n.String())
	}
	if want := []string{"internal/probe/live_test.go: -tags=live"}; !slices.Equal(got, want) {
		t.Errorf("the checkout's tags = %q, want %q", got, want)
	}
}

// TestParityRefusesToCompareNothing keeps the check from passing because
// it read the wrong file: two empty lists are equal, so each count has a
// floor.
func TestParityRefusesToCompareNothing(t *testing.T) {
	r := parityCheck(parityInputs{mk: parseMakefile("check:\n"), flows: []workflowFile{parseWorkflow(fxCI, "jobs:\n")},
		prereqOnly: parityPrereqOnly}, map[string]command{})
	want := []string{
		"go build is excused as CI-only, and ci.yml runs no go build; drop the excuse",
		"build is excused as a prerequisite and the Makefile does not define it; drop the excuse",
		"read 0 check: prerequisites, want at least 15: the reader is not seeing its input",
		"read 0 ci.yml command lines, want at least 20: the reader is not seeing its input",
		"read 0 gates in check, want at least 10: the reader is not seeing its input",
		"read 0 distinct gate calls, want at least 15: the reader is not seeing its input",
		"read 0 //go:build lines that need -tags (integration), want at least 1: the reader is not seeing its input",
	}
	if !slices.Equal(r.problems, want) {
		t.Errorf("problems:\n  %s\nwant:\n  %s", strings.Join(r.problems, "\n  "), strings.Join(want, "\n  "))
	}
}

// A prerequisite excuse that outlived its target, or names one check:
// runs as a gate, is refused. The excuse goes in through a copy of the
// table, so no test writes package state.
func TestParityRefusesAStaleExcuse(t *testing.T) {
	for _, tc := range []struct {
		name, target, want string
	}{
		{"a target that was renamed", "gone", "gone is excused as a prerequisite and the Makefile does not define it; drop the excuse"},
		{"a gate check names", "docs", "docs is excused as a prerequisite and check: names it as a gate"},
		{"a target check does not reach", "install", "install is excused as a prerequisite and nothing `make check` runs depends on it; drop the excuse"},
		{"no reason", "build", "build is excused as a prerequisite with no reason"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := parityInputsOf(t, parityRepo(t), nil)
			in.prereqOnly = maps.Clone(parityPrereqOnly)
			in.prereqOnly[tc.target] = "a reason"
			if tc.target == "build" {
				in.prereqOnly[tc.target] = " "
			}
			got := parityCheck(in, commands).problems
			if want := []string{tc.want}; !slices.Equal(got, want) {
				t.Errorf("problems = %q, want %q", got, want)
			}
		})
	}
}

// A CI-only excuse for a subcommand ci.yml does not run is refused: an
// excuse nothing uses would excuse the next such command unread.
func TestParityRefusesAnUnusedCIExcuse(t *testing.T) {
	checkParityCases(t, []parityCase{
		{"ci.yml builds nothing with go build", []docsEdit{
			replace(fxCI, "      - run: go build ./...\n", ""),
			replace(fxCI, "      - run: go build -o google-drive-mcp ./cmd/google-drive-mcp\n", "      - run: true\n"),
		}, nil, []string{"go build is excused as CI-only, and ci.yml runs no go build; drop the excuse"}},
	})
}

// Every excuse carries a reason, the same rule the coverage floor and the
// leak allowlist keep.
func TestParityExcusesHaveReasons(t *testing.T) {
	for _, excuses := range []map[string]string{parityPrereqOnly, parityCIOnly} {
		for name, reason := range excuses {
			if strings.TrimSpace(reason) == "" {
				t.Errorf("%q is excused from parity with no reason", name)
			}
		}
	}
}

func TestParityHoldsTheTreesTagsToAVetPass(t *testing.T) {
	const vet = "\t$(GO) vet -tags=integration ./...\n"
	const ciVet = "      - run: go vet -tags=integration ./...\n"
	checkParityCases(t, []parityCase{
		{"a file an OS tag and a custom tag build, which no pass vets", nil,
			map[string]string{"internal/e2e/e2e_test.go": "//go:build e2e && linux\n\npackage e2e\n"},
			[]string{
				"internal/e2e/e2e_test.go builds only with -tags=e2e (//go:build e2e && linux) and `make check` vets no pass that builds it; a contributor cannot see it break",
				"internal/e2e/e2e_test.go builds only with -tags=e2e (//go:build e2e && linux) and ci.yml vets no pass that builds it; it compiles only on a maintainer's machine",
			}},
		{"two tags set by one pass, the flag anywhere among vet's arguments", []docsEdit{
			replace(fxMakefile, vet, vet+"\t$(GO) vet -v -tags=soak,slow ./...\n"),
			replace(fxCI, ciVet, ciVet+"      - run: go vet -v -tags=soak,slow ./...\n"),
		}, map[string]string{"internal/soak/soak_test.go": "//go:build soak && slow\n\npackage soak\n"}, nil},
		{"a file either of two tags builds, with one of them vetted", nil,
			map[string]string{"internal/probe/probe.go": "//go:build integration || evals\n\npackage probe\n"}, nil},
		{"a test that turns vet off, which is not a vet pass", []docsEdit{
			replace(fxMakefile, vet, "\t$(GO) test -vet=off -tags=integration ./...\n"),
		}, nil, []string{
			".github/workflows/ci.yml:86 runs `go vet -tags=integration ./...` and `make check` does not",
			"`make vet` runs \"go test -vet=off -tags=integration ./...\" and ci.yml never does",
			"internal/service/integration_test.go builds only with -tags=integration (//go:build integration) and `make check` vets no pass that builds it; a contributor cannot see it break",
		}},
		// A pass compiles a file when the file's line holds with exactly the
		// pass's tags: one more tag can keep it out.
		{"a file another tag keeps out, vetted only by a pass with that tag", []docsEdit{
			replace(fxMakefile, vet, "\t$(GO) vet -tags=integration,evals ./...\n"),
		}, map[string]string{"internal/probe/probe.go": "//go:build integration && !evals\n\npackage probe\n"}, []string{
			".github/workflows/ci.yml:86 runs `go vet -tags=integration ./...` and `make check` does not",
			"`make vet` runs \"go vet -tags=integration,evals ./...\" and ci.yml never does",
			"internal/probe/probe.go builds only with -tags=integration (//go:build integration && !evals) and `make check` vets no pass that builds it; a contributor cannot see it break",
		}},
	})
}

// The tags each file needs: none where the go command sets them, and each
// smallest custom set otherwise, once per //go:build expression.
func TestParityTreeTags(t *testing.T) {
	files := map[string]string{
		"a/live.go":         "//go:build live && unix\n\npackage a\n",
		"a/other.go":        "//go:build !windows\n\npackage a\n",
		"a/windows.go":      "//go:build windows\n\npackage a\n",
		"a/solaris.go":      "//go:build solaris || illumos || aix\n\npackage a\n",
		"a/e2e.go":          "//go:build e2e && !experimental\n\npackage a\n",
		"a/race.go":         "//go:build evals || !race\n\npackage a\n",
		"a/arch.go":         "//go:build amd64.v2 && go1.22\n\npackage a\n",
		"a/exp.go":          "//go:build goexperiment.synctest\n\npackage a\n",
		"a/instrumented.go": "//go:build msan && asan\n\npackage a\n",
		"a/racy.go":         "//go:build race\n\npackage a\n",
		"a/mixed.go":        "//go:build (a && b) || c\n\npackage a\n",
		"a/live2.go":        "//go:build live\n\npackage a\n",
		"a/ignored.go":      "//go:build ignore\n\npackage a\n",
		"a/both.go":         "//go:build integration && slow\n\npackage a\n",
		"a/either.go":       "//go:build alpha || beta\n\npackage a\n",
		"a/plain.go":        "package a\n",
		"a/notes.txt":       "//go:build prose\n",
		"a/testdata/x.go":   "//go:build fixture\n\npackage x\n",
		".hidden/x.go":      "//go:build hidden\n\npackage x\n",
	}
	needs, err := parityTreeTags(docsTree(t, files), slices.Sorted(maps.Keys(files)))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, n := range needs {
		got = append(got, n.file+": "+n.String())
	}
	want := []string{
		"a/both.go: -tags=integration,slow",
		"a/e2e.go: -tags=e2e",
		"a/either.go: -tags=alpha or -tags=beta",
		"a/live.go: -tags=live",
		"a/live2.go: -tags=live",
		"a/mixed.go: -tags=c or -tags=a,b",
	}
	if !slices.Equal(got, want) {
		t.Errorf("parityTreeTags = %q, want %q", got, want)
	}
}

func TestParityVetPassesReadTheTagsFlagAnywhere(t *testing.T) {
	got := parityVetPasses([]string{
		"go vet -tags=integration ./...",
		"go vet -v -tags a,b ./... && go test -tags=evals ./...",
		`go vet --tags="x" ./...; go vet ./...`,
		"go vet --tags y ./...",
		"make vet",
		"go test -vet=off -tags=integration ./...",
		"go vet ./... -tags=z",
		"go vet -vettool /usr/local/bin/shadow -tags=sh ./...",
		"go vet -c 2 -printf.funcs Wrapf -tags c1 ./...",
		"go vet -tags=old -tags=new ./...",
	})
	want := [][]string{{"integration"}, {"a", "b"}, {"x"}, {"y"}, {"sh"}, {"c1"}, {"new"}}
	if !slices.EqualFunc(got, want, slices.Equal[[]string]) {
		t.Errorf("parityVetPasses = %q, want %q", got, want)
	}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
