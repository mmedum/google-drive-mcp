# Contributing to google-drive-mcp

Thanks for considering a contribution. This is a Go MCP server for
Google Drive, run by people against their own Drive. Read this once
before your first pull request; CI enforces the rest.

## Before anything else

**Nothing from a real Drive may enter this repository** (D-07). Not
file or folder ids, or the links carrying them; not the names or
content of real files; not account addresses, organization names, Cloud
project ids, OAuth client ids or secrets. This holds for code, docs,
fixtures, goldens, issues, pull requests, commit and tag messages and
logs. Naming the sibling servers, such as google-sheets-mcp, is fine.

Fixtures are synthetic: an id carries a `-fixture` suffix and an
address is at `example.com` (D-08). gitleaks scans for credentials and
`make leaks` for addresses, Drive ids and Drive links. Both run in the
pre-commit hook, in `make check` and in CI.

**Commit under your GitHub no-reply address**, and credit co-authors
with theirs (`ID+USERNAME@users.noreply.github.com`, from GitHub's email
settings). The history is public and cannot be edited, and a work
address in it names your organization for good. `make leaks` does not
read the identity git records or a `Co-authored-by:` line, so this one
is on you.

## Reporting bugs and requesting features

Open a [GitHub issue](https://github.com/mmedum/google-drive-mcp/issues)
with what
[`docs/troubleshooting.md`](docs/troubleshooting.md#reporting-a-problem)
lists. Never paste a tool result: it is your Drive's content.

For anything security-sensitive, do not open a public issue. Follow
[`SECURITY.md`](SECURITY.md).

## Development setup

```bash
git clone https://github.com/mmedum/google-drive-mcp
cd google-drive-mcp
make build
make hooks
```

Go is the only prerequisite, at the version in `go.mod`;
`GOTOOLCHAIN=auto`, the default, fetches it. The Makefile runs
golangci-lint, govulncheck, go-licenses, gitleaks and actionlint through
`go run` at pinned versions. Everything the repository runs on itself is
Go, under `scripts/` (D-10), and GoReleaser builds only `./cmd/...`, so
none of it ships.

`make hooks` installs a pre-commit hook running
`go run ./scripts/gates precommit`: gofmt, vet, `make leaks`' scan and
gitleaks on the staged changes, at the version the Makefile pins.

## Gates that must pass

```bash
make check
```

`make check` runs the gates CI runs, and the `check` target in the
`Makefile` is the list. `make parity` fails when a command that list
runs and `ci.yml` differ, so a check you add goes in both, in the same
words (D-77). CI runs it on Linux, macOS and Windows, where paths,
permission bits and line endings differ (D-78).

`go run ./scripts/gates` lists the checks, and `go run ./scripts/gates
<name>` runs one. Each has tests of its own, and a check that reports a
number holds a floor on what it read, because "found nothing" and "read
nothing" print the same line (D-76). Before you trust a gate you
changed, break what it holds and watch it fail.

These are worth knowing before they fail on you:

- **The schema diff** compares the tool surface, the resource templates
  and the instructions with the newest release's, recorded in
  `testdata/schema-baseline.json` (D-64). It fails on a removed tool or
  resource, a field removed at any depth, an input that takes fewer
  types or loses a listed value, an output that may return another type
  or may be missing, and a newly required input. A break passes only
  when a `**Breaking:**` entry under `[Unreleased]` names the item, and
  ships only in a new major version. A changed description, annotation,
  title, `_meta` mark or the instructions is reported, since a client
  decides from those whether to ask.
- **`make leaks`** fails on addresses outside the documentation
  domains, Drive ids and Drive links, unless an allow-list entry gives
  its reason. Run `go run ./scripts/gates leaks history` before anything
  goes public: it reads every commit, and a file removed from the tip is
  still in the history.
- **`make api-coverage`** holds every method of the Drive, Drive Labels
  and Drive Activity discovery documents to a verdict in
  `testdata/api-coverage.tsv`: used, naming the code, or left out with a
  reason (D-69). `make api-fields` does the same for fields in
  `testdata/api-fields.tsv`. `make api-diff` refetches the snapshot and
  is run by hand.
- **`make docs`** holds the docs to their references. A decision in
  `docs/architecture.md` is `D-NN`; an ID is never reused, and the file
  ends with its `Next ID:` line. Cite a
  decision by ID, or a doc by its path and heading anchor, never by
  section number. What you verified about Google goes in the comment at
  the line that depends on it, with the source link and the live step
  that checks it, written `Checked by the live step "<name>".` An
  integration test it names, `TestLive<Name>`, has to exist too. Cite
  the test, not a subtest.
- **`make staleness`** holds the README's tool table, scopes and
  permission rules to the binary, and `docs/configuration.md` to every
  setting the code reads. It also wants a CHANGELOG entry when the
  binary's source changed since the newest release tag; a test, a script
  or a dependency bump needs none (D-66).
- **`make outcomes`** refuses a result sentence written from the
  request rather than from Drive's answer (D-29).
- **Renderer goldens.** `go test ./internal/render -update` rewrites
  them; reading the diff is the part that matters. `.gitattributes` pins
  LF, since they are compared byte for byte.

## Tests

Unit tests never touch the network. They run against
`internal/gapi/drivetest`, an in-memory Drive behind `httptest` that
refuses what Drive refuses and matches no more than Drive does (D-67).
It holds one parent per file, one permission per principal, inherited
shared-drive grants and the word-prefix matching of `name contains`, so
a query that assumes substring matching fails in the fake. Failures can
be injected per request, a cut connection included.

Coverage is floored per package over a list derived from `internal/`;
`cmd/` is outside it (D-74). Call counts, memory and linearity are
ordinary tests, and `make bench` measures time apart from `make check`
(D-75).

## Changing the tool surface

Add to a tool's arguments and fields; do not take away. On a pull
request, `make schema-ack` compares the surface at the merge-base and
the head, annotations included. If it changed, end one of the pull
request's commits with a footer naming each changed item:

```bash
git commit --allow-empty -m "Acknowledge the tool surface change" \
  -m "SCHEMA-CHANGE: update_file, move_file: destructiveHint true"
```

The names are the list before the first colon followed by white space;
the text after it describes the change and names nothing. Name a tool
by its name, a resource template by its URI, and the instructions as
`instructions`. Put a name with a comma in it in backticks, and keep the
list on one line: a wrapped line that starts `word: ` begins a new
footer, as Conventional Commits reads it. Names are separated by
commas, and a `Token #value` footer such as `Closes #12` acknowledges
nothing. If it breaks something a release shipped, name that item in a
`BREAKING CHANGE:` footer instead. A refusal prints the footer that
fixes it, to add in an empty commit, never an amend.

## Adding a tool

1. Put the behavior in `internal/service`, with tests against
   `drivetest`. Tools stay thin.
2. Register it in `internal/tools`, with a flat schema (D-65), a
   `snake_case` `verb_noun` name (D-03), exact annotations (D-45), and a
   description that says what it costs and what it refuses.
3. A read returns text alone. A write returns text and JSON, and the
   JSON carries everything the text does (D-25).
4. Errors are `[class] message`, from the classes `internal/service`
   declares (D-51).
5. Add it to the README's tool table, and any setting to
   `docs/configuration.md`, or `make staleness` fails.
6. Drive every option in `scripts/livedrive`, or record it in
   `testdata/live-cover.tsv` with the reason, or `make live-cover` fails.
7. Read `make schema-diff`, and add the `SCHEMA-CHANGE:` footer above.

## The live run

Every fake in the unit suite is written from what we believe Drive
does, and only a live run tests that belief (D-70). Anything touching
the write path or an API response shape needs one, and the transcript
has to be read. The driver checks that a call succeeded or was refused
as expected, not that the answer was true.

```bash
make live
make live LIVE_ARGS="-write -destructive"
```

- **Your account.** It builds the binary and drives it over stdio as
  the account your profile signed in to. With no mode it only reads;
  `-file REF` adds one file's card and a listing below it.
- **Named steps.** Every step has a name, printed above its tool and
  arguments, and an expected outcome. An expected refusal proves as much
  as a success. A comment at the code cites a step by its name.
- **Its own scratch space.** `-write` makes a folder named
  `google-drive-mcp livedrive scratch <time>`, under `-parent` or at the
  root of My Drive, and works only inside it (D-71).
- **Reading it.** Every line goes through one redacting transcript,
  which masks ids, links and addresses, and the names of anything the
  run did not make (D-72). `-raw` turns that off; never use it in a
  shared terminal. Read a transcript before sharing it.
- **Coverage.** `make live-cover` holds every tool option to a step
  that sends it or a row in `testdata/live-cover.tsv` saying why not.
  The run reports what it sent.

The modes, each off unless asked for. Every mode but `-unlistable` acts
inside the `-write` run, and the driver sets the server's switches each
needs.

| Flag | What it adds |
|---|---|
| `-write` | Every tool that changes Drive, in the scratch folder |
| `-drive NAME_OR_ID` | A file moved into an existing shared drive and back, read back each way, and the folder-move refusal |
| `-share ADDRESS` | A real grant to a second person on a scratch file, taken back and read back before the run ends |
| `-transfer ADDRESS` | With `-destructive`, a real ownership transfer of a scratch file. **It cannot be undone, and Google emails the address**: name an account you control. Without it the transfer runs as a dry run and its refusals |
| `-blocked ADDRESS` | A share the organization's policy should refuse, with Google's reason beside this server's class |
| `-unlistable REF` | A folder this account can see but not list, read and listed |
| `-labels` | The label tools; the Drive Labels API must be enabled and its scopes granted |
| `-activity` | `list_activity`; the Drive Activity API must be enabled and its scope granted |
| `-destructive` | Everything `GDRIVE_ENABLE_DESTRUCTIVE` registers: the permanent deletes, inside a shared drive the run creates and deletes, and the shares past the organization. It needs an account that may create shared drives, which `get_account` reports |

`-destructive` works in a shared drive of its own because `empty_trash`
cannot be scoped to a folder: anywhere else it takes the account's
whole trash. `TestEmptyTrashIsNamedInOnePlace` fails if the driver names
that tool anywhere but the one method that runs inside the drive.

**What it leaves behind.** The scratch folder goes to the trash at the
end, and Drive empties the trash after 30 days. A file handed over with
`-transfer` stays in the new owner's Drive, and only they can remove it.
The scratch shared drive is deleted, the `-share` grant taken back and
the `-drive` file brought back. When one of those fails, the run says so
in a line starting `!!`, and
[`docs/troubleshooting.md`](docs/troubleshooting.md) says what to do.

Ctrl-C, a `SIGTERM` or closing the terminal stops a run without
stranding what it made: no further step starts, and the cleanup still
runs. A second signal is ignored while that happens; `Ctrl-\` quits at
once and leaves everything where it is.

### Integration tests

```bash
make integration
```

They are tagged `integration`, off by default, and read-only. They use
the account your profile signed in to, and check that `drivetest` and
Drive agree on the rules the fake encodes: `name contains` matches the
beginnings of words, `name =` ignores case, a path resolves from the
root, shared drives list, a missing id is actionable, and a generated id
is usable. They print no file names, addresses or ids; a failure reports
a shape or a count. `make vet` compiles them, and `make check` runs
none.

### Evals

```bash
make evals
go run ./scripts/evals -bin ./google-drive-mcp -only share-as-commenter-quietly
```

Evals score whether a model can use the tools, which the driver cannot
show (D-73). Each task runs through `claude -p` with only this server's
tools, and is scored on the end state, read back through the server, and
on the trace. An id no result handed the agent is a failure, and so is
any call to a tool that shares past the organization. Everything
happens in one scratch folder, trashed at the end; `-keep` leaves it.
`-share ADDRESS` gives the sharing task an address inside your
organization. Evals need the `claude` command and a signed-in account,
and spend money, so `make check` does not run them.
`go test ./scripts/evals` checks the task table and needs no
credentials.

## Branch and commit conventions

- `main` is released code and takes pull requests only, the
  maintainer's and release commits included (D-81). Work on a short
  topic branch.
- Write the subject in the imperative, under 72 characters, saying what
  changes. Look at a recent `git log` for the tone.
- The body says the problem, why this change solves it, and the
  alternatives rejected. A fixed review finding is one line at the end
  of the body. A declined one goes in the pull request, or becomes a
  present-tense comment at the line it protects.
- The maintainer pushes, merges, tags and releases.

## Pull requests

1. Open against `main`, with a summary and a test plan.
2. CI must be green on Linux, macOS and Windows.
3. A maintainer reviews and merges.

For maintainers: give `main` a ruleset that requires a pull request and
the CI checks, and forbids force-push and deletion. Do not add a
required status check before the workflow producing it is on `main`: a
check that has never run blocks every pull request, the one adding it
included.

## Changelog

Every user-visible change gets an entry under `## [Unreleased]` in
`CHANGELOG.md`, in the same pull request that makes the change. Leave
out changes only contributors see, unless they must act on them.

- **Sections** are the [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
  set and nothing else: Added, Changed, Deprecated, Removed, Fixed and
  Security, in that order.
- **One line per change, 25 words or fewer, impact first.** Write for
  someone deciding whether to upgrade. The investigation belongs in the
  commit message.
- **Breaking changes** start with `**Breaking:**`, name the item in
  backticks, and may add one sentence saying what to do. Anything
  needing a new login, a new scope or a config change is breaking, and
  so is a tool moving behind a setting or out from behind one.
- **Security fixes** name the CVE or GHSA.

`release.yml` lifts the matching section verbatim into the release
notes, so the entry is the release note (D-82). Versioning follows
[SemVer](https://semver.org/spec/v2.0.0.html), read through the tool
surface: tool names, input and output schemas, annotations, resources
and documented behavior.

## Releasing

Maintainers release as [`docs/release.md`](docs/release.md) says.

## Code style

- Match the surrounding code. Do not reformat what you did not touch.
- Comments explain why: a constraint, an invariant, something Google
  does that the code has to work around. Do not narrate what the code
  does.
- Write plainly: lead with the outcome, one idea per sentence. This
  holds for comments, commit messages, the CHANGELOG and tool
  descriptions.
- Logs record the call, never the payload (D-06).

`CLAUDE.md` has the short version of the rules, and
[`docs/architecture.md`](docs/architecture.md) the reasoning, one
decision per D-ID, and the [codemap](docs/architecture.md#codemap).

## Code of conduct

Taking part in this project means agreeing to
[`CODE_OF_CONDUCT.md`](CODE_OF_CONDUCT.md), the Contributor Covenant
3.0. It says how to report a problem, and what happens then.

## License

By contributing, you agree that your contributions will be licensed
under the project's [Apache 2.0 License](./LICENSE).
