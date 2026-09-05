# Changelog

All notable changes to this project are documented here. The format
follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and
this project uses [semantic versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `read_file`: a file's text, straight back. A Google Doc as Google's
  own markdown export with inlined images stripped, a Sheet as csv (or
  tsv) of its first sheet, Slides as plain text, an Apps Script project
  as JSON, and a text file, log, CSV or source file as itself. Only the
  window shown is fetched — the head of a 200 MB log is one small
  request — and the header says which bytes you got and what to pass as
  `offset` for the next. A window never ends in half a character.
- `download_file`: a file written to `GDRIVE_LOCAL_DIR`, streamed in
  chunks and checked against Drive's own md5. A Google document is
  converted on the way out (docx, xlsx and pptx by default); an older
  version comes out through `revision`. The result says whether the
  checksum matched, and never overwrites a file that is already there.
- `create_file`: an empty Google Doc, Sheet, Slides deck, Drawing or
  Form, or a file written from inline text with an optional import
  conversion.
- `upload_file`: a local file sent to Drive, multipart up to 5 MB and
  resumable in 8 MiB chunks above it. An interrupted upload asks the
  session how much it stored and continues from there rather than
  starting again.
- `update_content`: new bytes for an existing file, keeping its id, its
  place and everything that points at it, with `keep_previous_revision`
  to pin the version being replaced and `expect_head_revision` to refuse
  a write onto a file that has moved on.
- `create_folder`, `update_file`, `move_file`, `copy_file`,
  `create_shortcut`, `trash_file` and `restore_file`. `update_file`
  reports every field before and after; `move_file` and the two trash
  tools take `dry_run`; `copy_file` converts as Google imports, which is
  how a PDF or a scan becomes text `read_file` can return.
- Local-directory confinement: `GDRIVE_LOCAL_DIR` is the one place files
  are read from and written to, checked after symlinks are resolved, so
  a link inside it pointing out of it is refused like any other outside
  path. Unset, the two transfer tools stay registered and explain what
  to set.
- The duplicate-name guard: `create_file`, `create_folder`,
  `upload_file`, `copy_file` and `create_shortcut` refuse a same-named
  sibling unless `allow_duplicate: true`, and name the item that is
  already there. Drive allows two; a retried call is how they appear.
- Write tools return both prose and a structured result, and the
  structured result carries the prose, because a client shows one form
  or the other and never both.
- `internal/gapi` grew the transfer half: streaming downloads with a
  byte range and a stall guard, exports, multipart and resumable
  uploads with `308` recovery, create, patch, copy, and the revision
  calls a content update needs. `drivetest` grew with it: `alt=media`
  with `Range`, exports, upload sessions that can store part of a chunk,
  the trash cascade, revisions, and the folder-move refusal.

### Changed

- Paging a Google Doc, Sheet or Slides deck through `read_file` no longer
  re-exports it for every window. Drive takes no byte range on an export,
  so each continuation had been paying for a full server-side export and
  then discarding the part it had already shown: reading a 1 MB document
  at the default window was 53 exports and 28 MB on the wire to deliver
  1 MB. The exported text is now kept for the file cache's few seconds,
  and the windows after the first cost nothing.
- A write no longer clears the whole path cache. Creating a file cannot
  falsify a path that already resolved, so only a rename, a move or a
  trash invalidates, and only the entries that led to that one file. Two
  creates into the same folder had been paying for the path walk twice,
  at 100 quota units per listing per segment.
- A folder read while resolving a destination is no longer read again
  while working out where the result landed, and the root of My Drive is
  no longer fetched just to recognise that it is the root.
- `download_file` no longer hashes a file it cannot compare — an export
  and an older revision have no checksum to check against — and copies
  in 256 KiB blocks rather than 32 KiB.
- A dry run says so on its first line ("would have moved: …") as well as
  in the body. The action a write reports is now a closed set, so the
  words a result uses and the words the JSON schema promises cannot
  drift apart; a test holds them together.
- A read of a Google Sheet or Slides deck says once, not twice, that its
  content belongs to another API.

### Fixed

- Drive answers `teamDrivesFolderMoveInNotSupported` with a 403, which
  the error mapping read as a plain refusal. Moving a My Drive folder
  into a shared drive is now `[unsupported]` — a thing that cannot be
  done — rather than `[forbidden]`, which would have sent a model
  looking for permissions to change.
- The host allowlist stripped the port before matching, so an access
  token would have gone to `www.googleapis.com:8443`. It now refuses a
  host carrying a port. Phase 1 is where this began to matter: fetching
  a revision's export link means sending credentials to a URL that
  arrived in a response body.
- A file that grew between being measured and being read was uploaded
  truncated, and reported as complete. It is now measured again and sent
  from the beginning.
- `read_file` refused a text file over 20 MB while advising the caller to
  retry with `max_chars` and `offset` — which changed nothing, so a
  model following the advice looped on the same refusal. The read is a
  byte range and always was: a file's size no longer decides whether it
  can be read, and the head of a 200 MB log is one small request, as the
  description said all along.
- A create no longer leaves a cached path answering for the wrong file.
  Putting a second `notes.txt` beside the first makes that path
  ambiguous, and the cached entry went on resolving to the older file —
  the one thing the id-is-the-contract rule exists to prevent.
- A resumable upload whose session answered `308` while storing nothing
  — what a proxy that strips the `Range` header looks like — sent the
  same chunk for as long as the deadline allowed. It now gives up after
  eight rounds with no progress and says why.
- `keep_previous_revision` pinned the outgoing revision *before* the
  upload, so a failed upload left a revision kept forever that nothing
  had replaced and nothing would unpin. The pin happens after the
  content is replaced, and the result says so if it fails.
- An upload's response no longer drops the resource keys it carried, so
  a later call on a link-shared file still sends them.
- Reading past the end of a text file said so; reading past the end of a
  *blob* returned Google's bare `416`. Both now say the same thing.
- **Creating a Google Doc, Sheet, Slides deck, Drawing or Form failed
  outright.** Drive refuses a pre-generated id for those formats
  ("Generated IDs are not supported for Docs Editors formats"), which
  the design had assumed since phase 0 and no test could have caught: a
  fake that accepts what Drive refuses agrees with the bug. Found on the
  first live write. Ids are now sent only where Drive takes them, the
  fake refuses one with Google's own sentence, and a test covers all
  five formats.
- A create that cannot carry a pre-generated id is no longer retried
  after a 5xx. A 500 proves Google answered, not that it did nothing, so
  repeating one of those creates could leave two files. The retry rule
  now reads a flag set where the request is built rather than the
  request's kind, so a write added later cannot inherit permission to
  repeat without someone deciding that it may.

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
