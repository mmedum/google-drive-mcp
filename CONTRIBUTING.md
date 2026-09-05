# Contributing

Thanks for helping. A few rules keep this project trustworthy for the
people who run it against their own Drive.

## Ground rules

- **Nothing deployer-specific enters the repository.** No organisation
  names, file or folder ids or URLs, account emails, Cloud project ids,
  OAuth client ids or secrets, and no names or content of real files.
  Test fixtures are synthetic. gitleaks runs in pre-commit and CI.
- **Stdout is for JSON-RPC.** Log with `slog` to stderr only, and log
  truncated ids, counts and latencies — never names, paths, emails,
  queries or content.
- **Ids are the contract.** A name or path that matches more than one
  item is `[ambiguous]` with the candidates; nothing takes the first
  match or invents an id.
- **Access is never widened silently, and nothing is destroyed without a
  way back.** New sharing paths check `capabilities.canShare` first and
  report exposure before and after; removal goes to the trash unless the
  deployer enabled the gated tools.
- **Errors are tool results**, formatted `[class] actionable message`.
- **Schemas stay boring**: flat structs, `omitempty` for optional fields,
  no unions, no dots in tool names.
- **Conventions are checked against primary sources** before they are
  adopted, and the verdict goes in the evidence log in
  `docs/architecture.md` §18.

## Branches and pull requests

`main` is released code: every version on the releases page was built
from a tag on it. Changes reach it through a pull request, never a direct
push — including the maintainer's own, and including release commits.

```
git switch -c short-topic-branch
# work, with make check green
git push -u origin short-topic-branch
gh pr create --fill
```

Merge once CI is green on all three platforms. CI is the gate that
matters: the test suite runs on Linux, macOS and Windows, and path
handling, permission bits and line endings differ between them.

## Definition of done

```
make check
```

runs gofmt, go vet, golangci-lint, the tests with the race detector and
an 80% coverage floor on core packages, govulncheck, the stdio smoke test
and the staleness check. Everything it runs is Go: the gates live in
`scripts/gates`, so they are built, vetted, linted and tested like the
rest. `make hooks` installs a pre-commit hook that runs the fast ones.
Also:

- Add or update tests (golden files with `go test ./internal/render -update`).
- Update `README.md`, `docs/`, and `CHANGELOG.md` under `[Unreleased]`
  when behaviour or the tool surface changes.
- Run `./google-drive-mcp --dump-schemas` and check the diff; a removed
  tool or field, or a new required field, is a breaking change.

## Layout

See `docs/architecture.md` §5. In short: `cmd/` is the entrypoint,
`internal/gapi` talks to Google (with `drivetest` as an in-memory Drive
for tests), `internal/ref` parses references and paths, `internal/model`
is the server's view of a file, `internal/render` produces text,
`internal/service` orchestrates and holds the policy, `internal/tools`
registers MCP tools.
