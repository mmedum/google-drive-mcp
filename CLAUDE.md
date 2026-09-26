# CLAUDE.md — google-drive-mcp project instructions

Project-specific rules for Claude Code in this repository. The user's
global instructions still apply; this file adds to them.

## Mission

A production-grade Go MCP server for Google Drive, distributed to other
people. The design, its evidence log, the decided constraints and the
phase plan live in `docs/architecture.md`. Read it before changing the
tool surface, the addressing model, the sharing policy or the transfer
paths. The server stops at the file boundary: what happens inside a
Google Doc, Sheet or Slides deck is out of scope.

## Hard rules

1. **Nothing internal, ever.** No organization names, file or folder ids
   or URLs, account emails, Cloud project ids, OAuth client ids or
   secrets, no names or content of real files, and no reference to any
   other project, repository, account, machine or tool the maintainers
   use. This holds for code, docs, fixtures, commit messages, pull
   requests and logs. Fixtures are synthetic. Logs carry truncated ids,
   counts and latencies only. Conventions are cited to primary sources.
2. **Stdout carries only MCP JSON-RPC frames.** This is the protocol, not
   a house preference. MCP's stdio transport says the server "MUST NOT
   write anything to its `stdout` that is not a valid MCP message", and
   "MAY write UTF-8 strings to its standard error (`stderr`) for logging
   purposes" —
   <https://modelcontextprotocol.io/specification/2025-06-18/basic/transports>.
   Logs use `slog` to stderr. A stray print corrupts the JSON-RPC stream
   and the client silently stops working, which is why this is a hard
   rule rather than a style note.

   `forbidigo` enforces it: `fmt.Print*` and `os.Stdout` are forbidden
   outside `main`, which names the process's streams once and passes them
   down as `io.Writer`. `scripts/` is excluded, being maintainer tooling
   rather than the server. Check the message text when verifying it — a
   settings block that fails to load leaves forbidigo on its defaults,
   firing, looking like it works.
3. **Ids are the contract.** A name or path that matches more than one
   item is `[ambiguous]` with the candidates; the server never takes the
   first match and never guesses an id.
4. **Never widen access silently.** Google's organization policy decides
   what may be shared; the server checks `capabilities.canShare` first,
   shows exposure before and after, needs `allow_anyone: true` for a
   public link, sends no mail unless `notify` is set, and treats
   ownership transfer as its own action. `GDRIVE_SHARING=off` removes
   the sharing tools.
5. **Never destroy without a way back by default.** Trash and restore
   are the default surface. Permanent delete, empty trash, delete
   revision, delete shared drive and delete comment are unregistered
   unless `GDRIVE_ENABLE_DESTRUCTIVE=true`. No bulk removal tool.
6. **Files only through `GDRIVE_LOCAL_DIR`.** Downloads land there and
   uploads read from there; unset means no file transfer. Stream in
   chunks; never hold a whole file in memory.
7. **Own wire types, raw REST.** Do not import `google.golang.org/api`;
   extend `internal/gdrive` instead. No code is imported from any other
   project.
8. **Branches and commits.** `main` is released code and is never
   pushed to directly, release commits included. Work on a short topic
   branch. Commit at milestones and at the end of each phase, with
   messages that say what and why. Pushing, opening the pull request
   and merging are the maintainer's.
9. **Verify conventions against primary sources** before adopting them;
   record the verdict in the evidence log in `docs/architecture.md`.

## Where things go

- `cmd/google-drive-mcp/` — subcommands and server wiring.
- `internal/config/` env + bound flags; `internal/credentials/` keyring →
  file → env; `internal/userconfig/` non-secret profile file;
  `internal/auth/` loopback OAuth.
- `internal/gdrive/` wire types; `internal/gapi/` raw REST client, with
  `drivetest/` the in-memory fake Drive for tests.
- `internal/ref/` file references and paths (no network);
  `internal/model/` the server's view of a file; `internal/render/` text
  output; `internal/service/` orchestration and policy;
  `internal/tools/` MCP tools; `internal/server/` SDK wiring and schema
  dump.
- `testdata/` synthetic fixtures and renderer goldens.

## Definition of done

`make check` (gofmt, vet, golangci-lint, race tests with the 80% floor,
govulncheck, stdio smoke, staleness check of README/docs/CHANGELOG
against the code) plus tests for new behavior, the live driver against
a scratch folder, `/simplify` and `/code-review high` on the changed
files with findings resolved or explained, and a look at
`--dump-schemas` for breaking changes. A phase ends with a release
commit on a topic branch, a pull request, CI green on three platforms,
a merge, and the tag pushed on its own; then it waits for an explicit
"go".

## Working across sessions

Each phase is one session. On a fresh session: read this file, the
status line and §16 and §17 of `docs/architecture.md`, `CHANGELOG.md`
under `[Unreleased]`, `git log --oneline -20` and `git status`; run
`make check`; then continue the phase §16 names on a topic branch. At
the end of a phase, say which version was tagged and stop.
