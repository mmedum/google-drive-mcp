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

Two of these were found on the first two live writes, and neither could
have been caught here: the in-memory Drive the tests run against had been
agreeing with the design rather than with Google.

- **Creating a Google Doc, Sheet, Slides deck, Drawing or Form failed
  outright.** Drive refuses a pre-generated id for those formats
  ("Generated IDs are not supported for Docs Editors formats"), which
  the design had asserted since phase 0. Ids are now sent only where
  Drive takes them, and a test covers all five formats.
- **Creating a shortcut failed outright**, for the same reason one step
  further out: Drive refuses a pre-generated id for a shortcut too, with
  a different message and status ("The provided file ID is not usable").
  The first fix had enumerated the formats known to refuse; the rule now
  names the two known to accept — a folder, and anything that is not one
  of Drive's own types — so a Google-native type nobody has tried costs
  idempotency rather than the whole call.
- A file that grew between being measured and being read was uploaded
  truncated, and reported as complete. It is measured again and sent
  from the beginning.
- A create that cannot carry a pre-generated id is no longer retried
  after a 5xx. A 500 proves Google answered, not that it did nothing, so
  repeating one of those creates could leave two files. Whether a
  request may be repeated is now derived from its HTTP method — GET,
  PATCH, PUT and DELETE mean the same thing applied twice, a POST does
  not unless it carries an id that collapses the second attempt into the
  first — so a write added in a later phase and given no thought fails
  closed rather than inheriting permission to retry.
- A create no longer leaves a cached path answering for the wrong file.
  Putting a second `notes.txt` beside the first makes that path
  ambiguous, and the cached entry went on resolving to the older file —
  the one thing the id-is-the-contract rule exists to prevent.
- A resumable upload whose session answered `308` while storing nothing
  — what a proxy that strips the `Range` header looks like — sent the
  same chunk for as long as the deadline allowed. It gives up after
  eight rounds with no progress and says why.
- `keep_previous_revision` pinned the outgoing revision *before* the
  upload, so a failed upload left a revision kept forever that nothing
  had replaced and nothing would unpin. The pin happens after the
  content is replaced, and the result says so if it fails.
- An upload's response no longer drops the resource keys it carried, so
  a later call on a link-shared file still sends them.
- A rate limit is retried whatever the request is. Refusing to repeat a
  create that cannot carry an id was right for a 5xx, whose answer does
  not say whether the file was made, and wrong for a 429: being turned
  away proves the work was never begun. Every Docs-format create would
  otherwise have failed on the first rate limit instead of backing off.
- Opening a resumable upload session is retried again. A session is a
  URI, not a file — nothing exists until chunks are committed — so a 503
  on the opening request had been aborting an entire large upload before
  a byte was sent.
- `mime_type` naming one of Google's own formats is refused, and says
  which of the two things the caller meant: `kind` for an empty one,
  `convert_to` to turn content into one. It had been failing with a
  message about pre-generated ids, which the caller never mentioned.
- The id decision uses the type the file will actually be. Drive
  resolves a create's type as the body's or, failing that, the content's,
  and only the first was being consulted.
- The host allowlist stripped the port before matching, so an access
  token would have gone to `www.googleapis.com:8443`. It refuses a host
  carrying a port. Phase 1 is where this began to matter: fetching a
  revision's export link means sending credentials to a URL that arrived
  in a response body.

Refusals that were wrong, or right but useless:

- Drive answers `teamDrivesFolderMoveInNotSupported` with a 403, which
  the error mapping read as a plain refusal. Moving a My Drive folder
  into a shared drive is `[unsupported]` — a thing that cannot be done —
  rather than `[forbidden]`, which would have sent a model looking for
  permissions to change.
- `read_file` refused a text file over 20 MB while advising the caller
  to retry with `max_chars` and `offset` — which changed nothing, so a
  model following the advice looped on the same refusal. The read is a
  byte range and always was: a file's size no longer decides whether it
  can be read.
- `copy_file`, `create_file` and `upload_file` refuse a conversion Drive
  will not perform, naming what the file can become instead. Drive's own
  answer is "The requested conversion is not supported", which leaves a
  model to guess which half of the pair was wrong — a csv becomes a
  Sheet, not a Doc. The check reads the account's own `importFormats`
  and fails open, so a table that cannot be read never refuses a legal
  call.
- Reading past the end of a text file said so; reading past the end of a
  *blob* returned Google's bare `416`. Both now say the same thing.

Output that misled:

- A Google Doc, Sheet or Slides deck no longer reports a size. Drive
  says one byte for a new empty document and one byte for a long one:
  the field is metadata, not the size of anything that can be fetched,
  and it sat next to the list of formats that can be.
- A recursive listing was headed with the folder's parent rather than
  the folder it shows, so a tree and a flat listing of the same folder
  named different places. Both build the location the same way now, and
  so does the tree's own first line, which had been built by a third
  route and could contradict its header.
- A folder whose parent this account cannot see is no longer reported at
  the root of My Drive. Adding a name to a location that is not a path
  had been turning "(no folder this account can see)" into
  "My Drive/Orphan" — asserting a parent nobody has seen, which is the
  one claim the location type exists to avoid. The gap is shown where it
  is: "My Drive/…/Orphan".
- A file's kind reads with its article in every message that names it:
  "Notes is a Google Doc", not "Notes is Google Doc".

The gates themselves:

- The coverage floor derives its package list from the module rather
  than a hand-written one, with three packages exempt by name and
  reason. The hand-written list had silently omitted
  `internal/userconfig` — the profile file recording the account, the
  token location and the scopes — which had never been under the floor
  since phase 0, because an omission from such a list looks exactly like
  a package that does not exist. The gap it exposed is now tested.
- `goreleaser-action` was pinned by commit SHA while the goreleaser
  binary it installs floated across a major version. Pinned beside the
  SHA, as `cosign` and `syft` already were after the v0.0.1 signing
  failure taught the same lesson.
- The stdio smoke test checks that read-only mode *removes* the write
  tools, not only that it keeps the read ones.

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
