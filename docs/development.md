# Development

## Prerequisites

- Go 1.27.1 or newer, and nothing else. `go.mod` names the exact point
  release, so `GOTOOLCHAIN=auto` (the default) fetches it if your
  installed Go is older.
- Optional, matching what CI pins: golangci-lint v2.13.2, govulncheck
  v1.7.0, go-licenses v1.6.0, gitleaks v8.30.1, GoReleaser v2.18.

Everything this repository runs on itself is Go, including the gates and
the live driver, so a contributor needs one toolchain and one language.
They live under `scripts/` as ordinary packages, which means the code
holding the gates shut is itself built, vetted, linted and tested;
GoReleaser builds only `./cmd/...`, so none of it ships.

Install the Go-based tools with the current toolchain, so they can read
the language version `go.mod` targets:

```
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.2
go install golang.org/x/vuln/cmd/govulncheck@v1.7.0
```

## The gates

```
make check
```

runs, in order: gofmt, `go vet` (including the integration-tagged tests,
so they keep compiling), golangci-lint, the tests with the race detector
and an 80% statement-coverage floor per core package, govulncheck, the
stdio smoke test, and the staleness check. It is the definition of done,
and CI runs the same gates on Linux, macOS and Windows.

Individually:

```
make build          # ./google-drive-mcp
make test           # race tests with coverage
make cover          # test, then enforce the floor
make lint
make vuln
make licenses       # allowed licences only
make smoke          # drive the binary over stdio, without credentials
make schemas        # write schemas.json
make schema-diff    # compare the tool surface with the last tag
make staleness      # docs must match the code
make bench
```

## Tests

Everything runs against `internal/gapi/drivetest`, an in-memory Drive
behind `httptest`. It implements the rules that actually bite — one
parent per file, one permission per principal, inherited shared-drive
grants, and the *word-prefix* semantics of `name contains` — so a query
that assumes substring matching fails in the fake rather than in
production. Failures can be injected per request, including a cut
connection, which is how the retry and backoff paths are exercised.

### The gates

```
go run ./scripts/gates coverage cov.out 80    statement-coverage floor per core package
go run ./scripts/gates schema-diff BINARY     tool surface against the last tag
go run ./scripts/gates smoke BINARY           drive the binary over stdio, no credentials
go run ./scripts/gates staleness BINARY       documentation must match the code
go run ./scripts/gates precommit              gofmt, vet and a secret scan
go run ./scripts/gates install-hooks          write the git pre-commit hook
```

`make hooks` installs a pre-commit hook that runs the `precommit` gate.
It skips the secret scan when gitleaks is not installed — a hook that
fails on a missing tool is a hook people disable — and CI runs gitleaks
unconditionally, so nothing reaches `main` unscanned.

Renderer output is compared against golden files:

```
go test ./internal/render -update
```

Goldens are compared byte for byte, which is why `.gitattributes` pins LF
on every platform.

Nothing in `testdata/` came from a real Drive. Fixture ids look like Drive
ids but carry a `-fixture` suffix; addresses are `example.com`.

### Integration tests

Tagged `integration` and off by default:

```
GDRIVE_INTEGRATION=1 go test -tags=integration ./...
```

They create one folder named "google-drive-mcp test (safe to delete)" in
My Drive, work only inside it, and trash it at the end. With
`GDRIVE_TEST_WORKSPACE=1` they also exercise a scratch shared drive.
Sharing tests need a second address in `GDRIVE_TEST_SHARE_WITH` and are
skipped when it is unset. Ids and addresses go to the terminal only.

### The live driver

```
make live                                   # or:
go run ./scripts/livedrive -bin ./google-drive-mcp -file /Projects
```

It drives the built binary over stdio against whichever account is
signed in, and prints every result with ids, links and addresses replaced
by stable placeholders, so a transcript can go into an issue or a commit
message. `-raw` turns that off; do not use it in a terminal you are
sharing. A phase is not done until this has run.

## Layout

See `docs/architecture.md` §5. The short version:

```
cmd/google-drive-mcp/     subcommands and server wiring
internal/config/          GDRIVE_* settings, validated once at start
internal/credentials/     refresh token: keyring, then file, then env
internal/userconfig/      the non-secret profile file
internal/auth/            loopback OAuth with PKCE
internal/gdrive/          Drive API wire types, no dependencies
internal/gapi/            the raw REST client, and drivetest beneath it
internal/ref/             references and paths, parsing only
internal/model/           the server's view of a file
internal/render/          text output
internal/service/         orchestration and policy
internal/tools/           MCP tools
internal/server/          SDK wiring and the schema dump
```

Dependency direction runs one way: `tools` → `service` → `gapi` →
`gdrive`. Nothing under `internal/gapi` imports MCP, and nothing imports
`google.golang.org/api`.

## Branches, pull requests and releases

`main` is released code and is never pushed to directly, release commits
included. Work on a short topic branch, open a pull request, and merge
once CI is green on all three platforms. See `CONTRIBUTING.md`.

A release, once the phase's work is merged:

1. On a topic branch, update `CHANGELOG.md`: move `[Unreleased]` into a
   version heading with today's date.
2. Update the status line and the phase table in `docs/architecture.md`,
   and add what was verified live to its evidence log.
3. Commit as `Release N.N.N`, open a pull request, wait for CI, merge.
4. Push the tag **on its own**, and only one at a time — GitHub drops tag
   events past the third in a single push:

   ```
   git switch main && git pull
   git tag -a v0.1.0 -m "v0.1.0"
   git push origin v0.1.0
   ```

5. The release workflow builds for three platforms and two architectures,
   signs `checksums.txt` with a keyless Sigstore certificate, attaches an
   SBOM per archive and a build provenance attestation, and publishes.
   `workflow_dispatch` re-runs it against a tag if it fails for a reason
   unrelated to the code.

## Adding a tool

1. Put the behaviour in `internal/service`, with tests against
   `drivetest`. Tools stay thin.
2. Register it in `internal/tools`, with a flat schema, `snake_case`
   `verb_noun` name, no dots, and a description that says what it costs
   and what it will refuse.
3. Read tools return a text block only. Write tools return text and JSON,
   and the JSON carries everything the text does.
4. Errors are `[class] actionable message`; the classes are listed in
   `internal/service`.
5. Add it to the README's tool table, document any new setting in
   `docs/configuration.md`, and add a `CHANGELOG.md` entry. `make
   staleness` fails until you do, in both directions: it also catches a
   documented tool that no longer exists.
6. Run `make schema-diff`. A removed tool or field, or a new required
   field, is a breaking change.
