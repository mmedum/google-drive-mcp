# Changelog

All notable changes to this project are documented here. The format
follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and
this project uses [semantic versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.0.1] - 2026-09-05

The skeleton: the scaffolding every later change passes through, the
account and reference machinery, and the four read tools.

### Added

- `get_account`: the signed-in account, its storage, whether it has
  shared drives and which ones it can see, and which of this server's
  tools are registered.
- `get_file`: the file card — kind in plain words, folder and drive,
  link, size, owner, who can see it, and what this account may do with
  it. Follows no shortcut: it describes the shortcut and names its target.
- `list_folder`: one page of a folder's children, folders first in
  natural name order, or with `recursive: true` a tree bounded by
  `max_depth` and `max_items` that names the folders it did not enter.
- `search_files`: typed search over My Drive, files shared with you and
  every shared drive, by name, content, kind, folder, owner, star and
  date, with paging and Drive's `incompleteSearch` warning passed on.
- `login`, `logout`, `status` and `doctor` subcommands; `--version` and
  `--dump-schemas`.
- Loopback OAuth with PKCE, refresh token in the OS keyring with a `0600`
  file fallback that warns, and profiles for several accounts.
- A raw Drive v3 REST client over hand-written wire types, with retries
  that honour Google's own reasons and `Retry-After`, separate rate
  limiters for reads, writes and sharing, per-attempt deadlines, a
  Google-only host allowlist checked before credentials are attached, and
  resource keys remembered from URLs and responses and replayed on later
  calls.
- References: ids, every Drive and Docs URL shape, `root`, paths from My
  Drive, and `drive:Name/path` inside a shared drive. A path that matches
  more than one item is `[ambiguous]` with the candidates; a word that
  could be an id but names no file is retried as a name.
- `internal/gapi/drivetest`, an in-memory Drive with the real semantics
  of `name contains` (word prefixes, not substrings), one parent per
  file, one permission per principal, and per-request failure injection.
- The scaffolding: Makefile, golangci-lint, govulncheck, go-licenses,
  gitleaks, GoReleaser, CI on Linux, macOS and Windows, CodeQL,
  Dependabot, and the release workflow with Sigstore signing and build
  provenance.
- `scripts/gates`, the repository's own checks as Go: the coverage floor,
  the tool-schema diff against the last tag, the stdio smoke test, the
  staleness check, a leak check, and the pre-commit hook `make hooks`
  installs. Go is the only toolchain a contributor needs, and the code
  holding the gates shut is built, vetted, linted and tested like
  everything else.
- The leak check turns this project's first rule into something a build
  can enforce: no addresses outside the documentation domains, no
  strings shaped like a Drive id, no Drive links carrying one, and no
  compiled binaries. It runs in `make check`, in the pre-commit hook and
  in CI; `leaks history` walks every blob in every commit and is what to
  run before the repository goes public, since a leak removed from the
  tip is still in the log. gitleaks covers credentials; this covers what
  a live run against a real Drive can drag in.
- A test that fails if a file id, a name, an address, a search term or
  file content ever reaches a log. It runs the whole surface at debug
  level against unmistakable fixtures, which is what lets the issue
  template ask a reporter for a debug log without also asking them to
  audit it. Ids appear only as a six-character prefix, for correlating
  the lines of one call.
- CI on Linux, macOS and Windows, CodeQL, gitleaks and the leak check all
  green on the first run. Between them they caught two things the local
  gates did not: a slice sized from a query parameter in the in-memory
  Drive, and an unanchored host pattern in the leak check. Both fixed.
- Integration tests (`make integration`, build tag `integration`) that
  run against the signed-in account and assert the search rules the whole
  addressing design rests on — that `name contains` is a prefix match and
  not a substring one, and that `name =` ignores case — against Drive
  rather than against our model of it. They print no names, addresses or
  ids, and discover what to probe with from the account at run time.
- `scripts/livedrive`, which drives the built binary over stdio against a
  real account and replaces ids, links, addresses and the names beside
  them with stable placeholders. It says on every run that file and
  folder names are not redacted, because nothing distinguishes them from
  prose and a transcript believed to be clean and is not is worse than
  one nobody trusts.

[Unreleased]: https://github.com/mmedum/google-drive-mcp/compare/v0.0.1...HEAD
[0.0.1]: https://github.com/mmedum/google-drive-mcp/releases/tag/v0.0.1
