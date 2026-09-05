# Architecture — google-drive-mcp

**Status:** phase 1 written (2026-09-05), awaiting its live run and then
its release as v0.1.0. Phase 0 (v0.0.1) built the scaffolding, every
gate, `login/logout/status/doctor`, the `gapi` core, the `drivetest` fake
beneath it, `ref`, `model`, `render`, and the four read tools. Phase 1
adds the other twelve: `read_file` and `download_file`, `create_file`,
`upload_file` and `update_content`, and `create_folder`, `update_file`,
`move_file`, `copy_file`, `create_shortcut`, `trash_file` and
`restore_file`, with local-directory confinement, the duplicate-name
guard, and the transfer half of `gapi` — streaming downloads with a byte
range, exports, and resumable uploads that recover through the protocol.
All of it is green against the fake, including a forced interruption
mid-upload and a session that stores only part of a chunk; **none of it
is confirmed against Drive yet.** Spikes C and the write half of E run
with the live driver's `-write` mode before the release (§16). §16 has
the phase plan, §17 the decisions that are not to be reopened, §17a the
deferred cleanups, §17b where this repository differs from the shared
standard, and §18 the evidence log.

This document is the plan. It is written so that whoever picks the work
up can start from the repository alone: read the status line above, §16
for the phase, §17 for anything still open, and begin.

## 1. Mission and scope

A production-grade, Go, stdio MCP server that lets Claude work in **Google
Drive** the way a careful colleague does: find a file and know where it
lives and who can see it, keep folders in order, move and copy things,
get content in and out, share without widening access by accident, follow
what changed, and manage shared drives. Single binary, per-user OAuth
against the user's own Google account, Workspace or consumer.

**The repository is self-contained and meant to be distributed.** Every
deployer creates their own Google Cloud project and OAuth client; nothing
deployer-specific is baked into the code, the repository or the release
artifacts (§13).

**Scope is every file, at the file boundary.** In: everything the Drive
API v3 does to files, folders, shortcuts, shared drives, permissions,
revisions, comments, the changes feed, access requests, and, on
Workspace, labels and approvals. Out (**decided**): what happens *inside*
a Google Doc, Sheet or Slides deck. Those have their own APIs (Docs,
Sheets, Slides) and are a job for servers built on them. This server
reads such files through Google's export, says so, and never edits
their content.

Tool names are chosen so that a client can connect this server next to
one for Docs or Sheets without a collision: `search_files` and
`download_file` here, where a document server would say
`search_documents` and `export_document`.

### Why build it (research summary, verified 2026-09-05)

- **Google's official Drive MCP** (`drivemcp.googleapis.com/mcp/v1`,
  Developer Preview, rolling out since 2026-05-01) is remote HTTP only,
  needs a Web-application OAuth client with the host's callback URL, asks
  for `drive.readonly` plus `drive.file`, and exposes eight tools:
  `search_files`, `list_recent_files`, `get_file_metadata`,
  `get_file_permissions`, `read_file_content`, `download_file_content`,
  `create_file`, `copy_file`. No folders as such, no move, rename, trash,
  restore, sharing changes, shared-drive management, revisions, changes
  feed, comments or resumable upload. Its text reading of PDFs, Office
  files and images is done server-side and is worth matching where GA
  APIs allow (§7.1).
- **Anthropic's Drive connector in claude.ai** adds `share_file` (writer,
  commenter or reader, by email only, no control over the notification
  mail), `update_file` (title and parent) and `trash_file`. Still no
  restore, link-sharing control, revisions, changes or shared drives.
- **The open-source servers** fail in the same places, per their own
  issue trackers: shared drives invisible and search returning nothing
  despite the full `drive` scope (piotr-agier/google-drive-mcp #137,
  isaacphi/mcp-gdrive #2); search results unsorted (#167); a stalled
  token refresh hanging every later call (#169); a stale OAuth client
  kept after re-authentication (#168); `anyone` and `domain` grants
  wrongly requiring an email (#131); downloads buffering the whole file
  in memory until the process dies (taylorwilsdon/google_workspace_mcp
  #994); downloaded files never cleaned up (#995); a `console.log` on
  stdout breaking the protocol (#29); large files unreadable (#17); write
  tools that do not say what they changed (#1031).

### Non-goals

- Not an editor for Docs, Sheets or Slides content.
- Not a sync client. No `watch` or push channels: they need a public
  HTTPS endpoint. Polling the changes feed is the offered path.
- Not multi-tenant hosted. Stdio only (**decided**); the composition root
  stays transport-agnostic so this can change later.
- Not a domain-admin tool. No `useDomainAdminAccess`; the server acts as
  the signed-in person and sees what they see.
- Not client-side encryption (`generateCseToken`), not Apps Script, not
  the Drive UI integration, not the `appDataFolder`.

## 2. Hard constraints from the platform

Verified against the Drive API v3 reference and guides on 2026-09-05.

| Constraint | Consequence |
|---|---|
| A file has **one parent** (since 2020). A move is `files.update` with `addParents` and `removeParents`. A folder cannot be moved from My Drive into a shared drive (`teamDrivesFolderMoveInNotSupported`); a file can. My Drive allows 100 levels of nesting and 500 000 items per folder. | `move_file` is one call and reports old and new location; the folder-into-shared-drive case is refused up front with the alternative spelled out. |
| **Search is not substring search.** `name contains 'x'` matches names whose words *start* with x. Verified live (spike A, §18): the match ignores case, every word of x must prefix *some* word of the name, and **their order is irrelevant** — "Decentralized Identity" and "Identity Decentralized" return the same set. `name = 'x'` matches the whole name and **also ignores case**. `fullText contains` is looser than whole-token matching. Single quotes and backslashes are escaped with a backslash. | `search_files` builds the query from typed fields and says in its description and in an empty result what "contains" means. Path resolution uses `name =`, so two siblings differing only in case are `[ambiguous]`, not a coin toss. |
| Shared drives need `supportsAllDrives=true` on nearly every method, `includeItemsFromAllDrives` plus `corpora` on listings, and `driveId` for one drive. `corpora=allDrives` can return `incompleteSearch=true`. A shared drive's root folder id is the drive id. | Every request carries `supportsAllDrives`. Listings default to `allDrives` and report `incomplete_search`. |
| **Quota is in units**: a read costs 5, a list 100, a download 200, an edit 50; 325 000 units per minute per user, 1 000 000 per minute per project. Over the limit Google answers 403 `userRateLimitExceeded` / `rateLimitExceeded` or 429; the guide says truncated exponential backoff. Sharing has its own `sharingRateLimitExceeded`. | A list is twenty reads. Listings are paged and budgeted; path resolution is cached; sharing has its own limiter; retries honour the reasons above. |
| **Uploads**: simple or multipart up to 5 MB; resumable above that, in chunks that are multiples of 256 KiB, resumed with `Content-Range: bytes */total` and a `308` carrying the received `Range`. 5 TB per file, 750 GB per user per day. Import conversion is asked for with `mimeType` in the metadata: Word, ODT, HTML, RTF, plain text and Markdown to a Doc; Excel, ODS, CSV, TSV to a Sheet; PowerPoint, ODP to Slides; images and PDF to a Doc with OCR. | `create_file` (inline text) uses multipart; `upload_file` streams from disk, multipart under 5 MB and resumable above, and recovers from a cut connection inside the call. `convert_to` is one field. |
| **Downloads**: `files.get?alt=media` for blobs, with `Range` for partial reads and `acknowledgeAbuse` for flagged files; `files.export` for Workspace documents, capped at **10 MB**, no `Range`; `files.download` (a long-running operation, valid 24 h) for Google Vids and for *revisions* of Docs and Sheets. Export formats: Docs to docx, odt, rtf, pdf, txt, html, zip, epub, md; Sheets to xlsx, ods, pdf, zip, csv, tsv; Slides to pptx, odp, pdf, txt; Drawings to pdf, jpg, png, svg. | `read_file` fetches only the byte window it shows; `download_file` streams to disk and verifies the md5; old revisions of Docs go through `files.download`. |
| **Permissions**: roles `owner`, `organizer`, `fileOrganizer` (shared drives only), `writer`, `commenter`, `reader`; types `user`, `group`, `domain`, `anyone`; **one permission per principal**; `expirationTime` only for users and groups, at most one year out; `sendNotificationEmail` defaults to true for users and groups and cannot be turned off for an ownership transfer; making someone owner needs `transferOwnership=true`; on consumer accounts the transfer is pending until the new owner accepts (`pendingOwner`); `allowFileDiscovery` applies to `domain` and `anyone`; in shared drives `permissionDetails.inherited` marks permissions that can only be removed at their source. | `share_file` updates an existing grant instead of failing, defaults `notify` to off, treats ownership as its own explicit action, and reports exposure before and after. Inherited permissions are refused with the source named. |
| **Trash**: only the owner can trash a My Drive file (`insufficientFilePermissions` otherwise); trashing a folder trashes its contents; the trash empties after 30 days; `files.delete` is permanent and skips the trash; `files.emptyTrash` too. `trashedTime` and `trashingUser` exist only in shared drives. | `trash_file` and `restore_file` are the default surface; permanent deletion and emptying the trash are gated (§12). |
| **Comments**: every `comments` call except delete must pass `fields`; anchors written through the API are shown as unanchored by the editors; unanchored comments render only on Workspace documents (a PDF's comments exist through the API but do not show in the previewer). Resolving is a reply with `action: resolve`. | One Drive backend; the value here is comments on files that are not Docs. |
| **Revisions**: `revisions.list` may be incomplete for busy Docs, Sheets and Slides; `keepForever` pins at most 200; `revisions.delete` works only on blob files and never on the last revision; unpinned blob revisions go after 30 days or once 100 have piled up; a Doc's old content comes from `files.download` with `revisionId`, not from `revisions.get`. | `list_revisions` says so in its output; `manage_revision` keeps or unkeeps; deleting a revision is gated. |
| **Changes**: `changes.getStartPageToken`, then `changes.list` with the token, oldest first, `includeRemoved`, `restrictToMyDrive`, `driveId`; the last page carries `newStartPageToken`. Token lifetime is not documented. | `list_changes` without a token hands one out; with a token it lists and returns the next one. |
| **Resource keys**: link-shared files under the 2021 security update need `X-Goog-Drive-Resource-Keys: id/key,…`. Keys arrive in `resourceKey`, `shortcutDetails.targetResourceKey` and the `resourcekey` URL parameter. | The reference parser keeps keys from URLs; the client remembers keys from every response and sends them on later calls for that id. |
| **Scopes**: `drive` and `drive.readonly` are *restricted*; `drive.file` is non-sensitive but reaches only files the app created or the user opened with it through the Picker, which a CLI has no way to show. `drive.metadata` is restricted too. An External OAuth app in Testing gets 7-day refresh tokens; an Internal (Workspace) consent screen does not. | Full mode asks for `drive`; read-only asks for `drive.readonly`; labels add `drive.labels` (§10). Fine for a per-user app each deployer owns. The README covers Internal vs Testing. |
| **Shared drives** exist only on Workspace editions; consumer accounts cannot create one (`about.canCreateDrives`). `drives.create` requires a `requestId` so a retry cannot create two. `drives.delete` needs an empty drive and an organizer. Members are permissions on the drive id. | `manage_drive` sends a fresh UUID as `requestId` and keeps it for the retry; `delete_drive` is gated; membership goes through `share_file` with the drive as the target. |
| `files.generateIds` returns ids that `files.create` and `files.copy` accept in the body, making them **idempotent** — **except for the Docs Editors formats, which refuse one** (§18, live). | Every create, upload and copy carries a pre-generated id where Drive takes one; a new Doc, Sheet, Slides deck, Drawing or Form cannot, and is therefore never retried. |
| **Labels** are Workspace metadata; definitions live in the separate Drive Labels API (`drivelabels.googleapis.com`, scopes `drive.labels`, `drive.labels.readonly`); the Drive API applies them with `files.modifyLabels` and reads them with `includeLabels`. **Approvals** are a new resource on files (`approvals.start/approve/decline/cancel/comment/reassign/get/list`). **Access proposals** cannot be created through the API, only listed and resolved by approvers (`accessproposals.list/get/resolve`). | Labels and approvals are Phase 4 (Workspace); access requests are Phase 3. |
| Listings return at most 1 000 per page; `nextPageToken` stays valid for hours. `orderBy` keys: `createdTime`, `folder`, `modifiedByMeTime`, `modifiedTime`, `name`, `name_natural`, `quotaBytesUsed`, `recency`, `sharedWithMeTime`, `starred`, `viewedByMeTime`. | Pages are capped well under 1 000; `list_folder` sorts `folder,name_natural`; `search_files` defaults to `modifiedTime desc`. |
| Claude Code truncates tool results above 25 000 tokens and warns at 10 000. | Reads and listings are budgeted with continuation. |

### What the API cannot do (so we don't promise it)

Copy a folder (only files; a recursive copy is many calls and comes in
Phase 3 with a budget); watch a file without a public endpoint; give a
file two parents; export a Workspace document above 10 MB; export one
sheet of a spreadsheet (csv and tsv take the first sheet; anything else
is a Sheets API read this server does not offer); read the text of a PDF or image without first
converting it to a Doc (which OCRs it, and creates a file); create an
access request; remove an inherited shared-drive permission on the child;
undo a permanent delete.

## 3. Requirements distilled from other servers' failures

1. Shared drives from day one: every call supports them, listings include
   them, incomplete searches are reported (#137, #2).
2. Deterministic order and honest pagination on every listing (#167).
3. Everything is bounded: token refresh, request bodies, listings, file
   sizes, time (#169; the go-sdk v1.8 hardening work is about the same
   class of bug).
4. Login rewrites the stored client-secret path and account so a
   re-authentication cannot keep stale state (#168).
5. Principals are users, groups, domains or anyone, each with its own
   rules; an email is required only where Google requires it (#131).
6. Content streams to and from disk in chunks and is never held whole in
   memory (#994); every file written is named in the result and lands in
   one directory the deployer chose (#995).
7. Stdout carries only JSON-RPC (#29).
8. Large text is budgeted with continuation (#17).
9. Every write says what changed and what the state is now (#1031).
10. Conventions checked against the MCP specification, Anthropic's tool
    guidance and the observed behaviour of Claude Code (§18): flat strict
    schemas, snake_case verb_noun names, `[class] message` errors, dry
    run on writes, destructive tools unregistered unless enabled, read
    tools return text only.

## 4. Core design bets

1. **Ids are the contract; names are resolved out loud.** Every `file`
   argument takes an id, any Google URL, or a path (§6). A name or path
   that matches more than one item is `[ambiguous]` with every candidate
   listed; the model picks an id. The server never takes the first match.
2. **Location is always shown.** Every file in a result carries its folder
   path (`My Drive/Projects/2026` or `Marketing (shared drive)/Q3`),
   because names are not unique in Drive and a file's meaning depends on
   where it sits.
3. **Access is never widened silently.** Sharing tools report who could
   see the file before and after. `anyone` links need `allow_anyone:
   true` on the call *and* a deployer policy that permits them.
   Notification mail is off unless asked for. Ownership transfer is its
   own explicit action. A deployer can limit sharing to the account's own
   domain, or turn the sharing tools off (§7.4).
4. **Nothing is destroyed without a way back, by default.** Trash and
   restore are the default surface. Permanent delete, empty trash, delete
   revision, delete shared drive and delete comment exist only with
   `GDRIVE_ENABLE_DESTRUCTIVE=true`. Replacing a file's content can pin
   the previous revision first.
5. **Content that is not text goes through disk, never through the
   context window.** Text-like content comes back inline under a budget.
   Everything else streams to `GDRIVE_LOCAL_DIR` and is checksum-verified;
   uploads read only from that directory. When it is unset there is no
   file transfer at all; inline text still works both ways.
6. **Listings are budgeted and cost-aware.** One page per call; a
   recursive walk has depth and item budgets and says where it stopped.
   A list costs twenty reads, and the design counts them.
7. **Writes are idempotent wherever the API allows.** Creates, uploads
   and copies carry pre-generated ids; shared-drive creation carries a
   request id; metadata patches are idempotent by nature. A retry after
   a network failure cannot make two of anything.
8. **Raw REST with our own wire types** in `internal/gdrive`. The
   generated `google.golang.org/api` module is not a dependency: it
   drags in gRPC, OpenTelemetry and the cloud auth stack for a binary
   that only needs JSON and streaming HTTP.
9. **Results say what changed.** Write tools return text and JSON, and the
   JSON carries everything the text does. Read tools return text only,
   because Claude Code 2.1 shows the model only the structured form when
   both are present (§18).
10. **The model sees what the Drive UI shows.** Kind in plain words
    ("Google Sheet", "PDF", "folder", "shortcut to a Google Doc"), owner,
    a sharing summary, link state, what the signed-in person can do
    (edit, share, trash, delete, download), starred, trashed, size,
    modified by whom and when.

## 5. Module layout

```
cmd/google-drive-mcp/     main: login / logout / status / doctor subcommands, server default,
                          --version, --dump-schemas
internal/config/          GDRIVE_* env with flags bound to the same names; typed enums; validated at start
internal/credentials/     refresh token: OS keyring → 0600 file under os.UserConfigDir() with a logged
                          warning → GDRIVE_REFRESH_TOKEN env override
internal/userconfig/      non-secret profile file: client_secret path, account email, token location, scopes
internal/auth/            loopback OAuth (127.0.0.1:<random>, PKCE), scope sets (full / read-only / labels)
internal/gdrive/          Drive API wire types (File, Permission, Revision, Comment, Reply, Drive, Change,
                          About, AccessProposal, Label, Operation), no dependencies
internal/gapi/            raw REST client: client.go (retry, limiters, slog, host allowlist, resource
                          keys), files.go, upload.go (multipart, resumable), download.go (streaming,
                          Range, md5), permissions.go, drives.go, revisions.go, comments.go,
                          changes.go, proposals.go, labels.go, errors.go. No MCP imports.
internal/gapi/drivetest/  an in-memory Drive behind httptest for network-free tests: files with parents,
                          trash, permissions, revisions, shared drives, a changes log, resumable upload
                          sessions with 308 semantics, alt=media with Range, error injection
internal/ref/             file references: ids, every Google URL shape (with resourcekey), the path
                          syntax; parsing only, no network
internal/model/           the server's view of a file: kind names, location path, sharing summary,
                          capability words, sizes and times as people read them
internal/render/          text renderers: file card, listing, tree, permissions, revisions, changes,
                          comments; budgets and continuation
internal/service/         orchestration: resolve refs and paths (short cache), search, listing and
                          walking, content in and out, organising, the sharing policy, trash, drives,
                          history, comments, access requests, labels
internal/server/          SDK wiring; schema dump through an in-memory client session
internal/tools/           one file per area: files.go, content.go, organise.go, access.go, drives.go,
                          history.go, comments.go, resources.go, tools.go
internal/version/
testdata/                 synthetic fixtures (§14) and golden outputs
docs/
scripts/gates/            the repository's own checks, as Go: coverage floor, schema diff,
                          stdio smoke, staleness, pre-commit; never shipped
scripts/livedrive/        drives the built binary against a real account, redacting ids,
                          links and addresses before anything is printed
```

**One language.** Everything the repository runs on itself is Go. The
gates were shell with Python embedded in them at first; that put a second
toolchain on `make check` for JSON parsing Go does natively, and left the
code holding the gates shut as the only code in the repository that was
neither vetted, linted nor tested. As Go packages under `scripts/` they
are all three, and a contributor needs one toolchain.

Dependencies, all pinned: `modelcontextprotocol/go-sdk` v1.7.0 (with
`google/jsonschema-go`), `golang.org/x/oauth2` v0.36.0,
`zalando/go-keyring` v0.2.8, `golang.org/x/time` v0.15.0. Nothing else.
There is no markdown here to parse and no diff to compute.

**Scaffolding.** The Makefile, the gates under `scripts/gates` (coverage
floor, schema diff, staleness check, stdio smoke, and the pre-commit
hook they install), `.golangci.yml`, the CI, CodeQL and
release workflows, `.goreleaser.yaml`, `.gitleaks.toml`,
Dependabot, the issue templates,
`.gitattributes`, `.gitignore`, `SECURITY.md`, `CONTRIBUTING.md` and
`docs/development.md` are written in Phase 0, before the first tool, so
every later commit passes through the same gates (§12, §13). The
repository is self-contained: it imports no code from any other project
and refers to none.

Toolchain, newest as of 2026-09-05: Go 1.27.1 (`go 1.27.1` in go.mod,
see §17), go-sdk v1.7.0 (v1.8.0-pre.2 exists; it hardens input bounds
and adds `SupportedProtocolVersions`, and Dependabot will propose it when
it is final), golangci-lint v2.13.2, govulncheck v1.7.0, go-licenses
v1.6.0, gitleaks v8.30.1, GoReleaser v2.18.

## 6. Addressing: file references and paths (`internal/ref`)

Every tool that points at something takes a `file` (or `folder`,
`drive`, `parent`) string in one of these forms:

```
1AbC…                                            an id
https://drive.google.com/file/d/<id>/view?resourcekey=<key>
https://drive.google.com/drive/folders/<id>      (also /drive/u/0/folders/<id>)
https://drive.google.com/drive/drives/<id>       a shared drive
https://docs.google.com/document/d/<id>/edit     any Docs, Sheets, Slides, Forms, Drawings URL
root  |  my_drive                                the root of My Drive
/Projects/2026/Budget.xlsx                       a path from the root of My Drive
drive:Marketing/Campaigns/Q3                     a path from a shared drive, by name or id
```

**Path resolution** walks one segment at a time with
`'<parent>' in parents and name = '<segment>' and trashed = false`. No
match is `[not_found] nothing named "Q3" in Marketing/Campaigns; the
folder holds 14 items, list_folder shows them`. More than one match is
`[ambiguous]` with each candidate's id, kind and modification time, and
the instruction to pass an id. A shortcut in the middle of a path is
followed to its target and the result says so. Resolved `(parent, name)`
pairs are cached for 60 seconds per process, so a burst of calls on one
path costs one walk. Trashed items are skipped; a trashed item is reached
by id.

**Shortcuts.** Read and content tools follow a shortcut to its target and
say so in the header. Organising, sharing and trash tools act on the
shortcut itself, because that is the thing sitting in the folder the
person is looking at, and the result says "this is a shortcut to X".

**Resource keys.** A key seen in a URL, or in any response
(`resourceKey`, `shortcutDetails.targetResourceKey`), is remembered for
the process and sent as `X-Goog-Drive-Resource-Keys` on every later call
for that id.

Errors carry the fix, in a `[class] message` form: `auth`, `forbidden`, `not_found`, `ambiguous`, `exists`, `invalid`,
`unsupported`, `blocked`, `rate_limited`, `server`, `network`,
`ambiguous_outcome`, `unexpected`.

## 7. Reading, content, organising, sharing

### 7.1 Read path

- `get_file` returns a **file card**: name, kind, id, link, location
  path, size, created and modified (by whom), owner, sharing summary
  ("shared with 3 people: 2 can edit, 1 can view; anyone with the link
  can view"), what the signed-in person can do (from `capabilities`, in
  words), starred, trashed (with who and when in shared drives),
  description, shortcut target, head revision id, md5, custom
  properties, labels when present, the export formats a Workspace
  document offers, and for a Google Doc or Sheet a note that its content
  is edited through the Docs or Sheets API, which this server does not
  offer. One
  `files.get` with a fixed `fields` list; for shared-drive items a
  `permissions.list` for the summary. Cheap; call it first.
- `search_files` builds the Drive query from typed fields: `name` (word
  prefixes), `text` (whole words or a quoted phrase in the content),
  `kind` (`folder`, `doc`, `sheet`, `slides`, `form`, `drawing`, `pdf`,
  `image`, `video`, `audio`, `shortcut`, `office`, `any`) or a raw
  `mime_type`, `in_folder` (direct children only; Drive cannot recurse in
  a query, and the description says so), `drive` (one shared drive),
  `scope` (`all`, `my_drive`, `shared_with_me`), `owner` (`me` or an
  email), `starred`, `trashed`, `modified_after`, `modified_before`,
  `created_after`, `order_by` (`modified`, `name`, `created`, `recency`,
  `viewed`, `size`), `limit` (default 25, max 200), `page_token`, and
  `raw_query` for the rest of the syntax, ANDed in. Each hit shows kind,
  name, id, location, modified time and who. Locations need the parents'
  names, which the listing does not carry: up to 20 distinct parents per
  page are looked up (cached), the rest show ids. `incomplete_search`
  from Google is passed on as a line the model can act on.
- `list_folder` lists one page of a folder's children, folders first,
  natural name order (`orderBy=folder,name_natural`), with `kind` and
  `page_token`. `recursive: true` walks breadth-first under `max_depth`
  (default 3, max 10) and `max_items` (default 200, max 2 000) and
  renders a tree with per-folder counts; when a budget stops it, the
  output names the folders it did not enter. The description states the
  cost.
- `read_file` returns text: a Google Doc as Google's markdown export
  (base64 images stripped), a Sheet as csv of the first sheet (the
  header says which sheet, that `format: tsv` and `download_file` exist,
  and that any other sheet or range is a Sheets API read this server
  does not offer), Slides as plain text, Apps Script as JSON, and
  text-like blobs (`text/*`, JSON, XML, YAML, CSV, Markdown, source code
  by extension, any size) through `alt=media` with a `Range` header
  covering only the window shown (`offset`, `max_chars`, default 20 000
  characters, maximum 400 000), so the head of a 200 MB log is one small
  request. There is deliberately **no size limit** on this path: the
  range bounds the transfer, and a limit here would refuse a file the
  tool can in fact read (§18). PDFs, Office files and images are `[unsupported]` with the two
  ways forward: `download_file`, or `copy_file` with `convert_to: doc`
  (Google's import, which OCRs PDFs and images) and then `read_file` on
  the copy. The header comment carries name, kind, size, head revision,
  the range shown and `continue_from`.
- `download_file` writes to `GDRIVE_LOCAL_DIR/<safe name>-<short
  id>.<ext>`: a blob through `alt=media`, streamed to disk in chunks, md5
  checked against the metadata, refused above `GDRIVE_MAX_DOWNLOAD`; a
  Workspace document through `files.export` with `format` (defaults:
  docx, xlsx, pptx, png; `pdf` on request); an old `revision` through
  `files.download` for Docs and Sheets and `revisions.get?alt=media` for
  blobs; `acknowledge_abuse: true` for a flagged file, echoed as a
  warning. Returns path, bytes, whether the checksum matched, kind.
- `list_permissions`, `list_revisions`, `list_changes`, `list_drives`,
  `list_comments`, `list_access_requests` and `get_account` (who you are,
  storage used and limit, whether you can create shared drives, the
  sharing policy in effect) are text listings, budgeted.

### 7.2 Content in

- `create_file`: `name`, `parent` (default My Drive root), and either
  `kind` (`doc`, `sheet`, `slides`, `drawing`, `form`) for an empty
  Workspace file, or `content` (inline text) with `mime_type` (default
  `text/plain`) and optional `convert_to` (`doc`, `sheet`, `slides`)
  through Google's import. Plus `description`, `starred`. Multipart
  upload with a pre-generated id **where Drive takes one**: the Docs
  Editors formats refuse it (§18), so those creates are the ones that
  are not idempotent, and the duplicate-name guard is what catches a
  double. Refuses content above 5 MB (use `upload_file`).
- `upload_file`: `local_path` (inside `GDRIVE_LOCAL_DIR` after symlinks
  are resolved), `name` (default the file name), `parent`, `mime_type`
  (default: by extension, then by sniffing), `convert_to`, `ocr_language`,
  `description`. Up to 5 MB multipart, above that resumable in 8 MiB
  chunks (a multiple of 256 KiB), each chunk under its own timeout. A cut
  connection is handled inside the call: query the session with
  `Content-Range: bytes */total`, read the `Range` from the `308`,
  continue. The blob's md5 is compared with the metadata Drive returns
  (conversions have none, and the result says so). Returns the file card.
- `update_content`: `file` plus `content` or `local_path`, `mime_type`,
  `keep_previous_revision` (pins the current head with
  `revisions.update keepForever` before uploading; Drive keeps it 30 days
  anyway), `expect_head_revision` (a best-effort check, since Drive v3 has
  no preconditions). Refused for Workspace documents (`[unsupported] this
  is a Google Doc; its content is edited through the Docs API, or upload
  a new file with convert_to`), folders and shortcuts. Returns old and new head revision
  ids and sizes.

### 7.3 Organising

- `create_folder`: `name`, `parent`, `description`, `color`. Drive allows
  two folders of one name side by side; this tool does not, unless
  `allow_duplicate: true`: an existing sibling of that name is `[exists]`
  with its id (§17).
- `update_file`: `name`, `description`, `starred`, `color` (folders),
  `properties` (a map; an empty value deletes a key),
  `copy_requires_writer_permission`, `writers_can_share`. Patch semantics:
  only the fields passed change. Returns before and after per field.
- `move_file`: `file`, `to` (a folder, `root`, or a shared drive). Checks
  the single-parent rule, refuses a My Drive folder bound for a shared
  drive with the API's reason and the alternative ("create the folder in
  the shared drive, then move the files"), supports `dry_run`. Returns
  old path and new path.
- `copy_file`: `file`, `name` (default "Copy of …", as Drive does), `to`
  (default the source's folder, as Drive does), `convert_to` (import
  conversion, OCR for PDFs and images, `ocr_language`),
  `keep_revision_forever`. Uses a pre-generated id. Folders are refused
  in Phase 1 with the reason; Phase 3 adds `recursive: true` with an item
  budget. (`copy_comments` was in this list until Phase 1 checked: v3's
  `files.copy` has no such parameter — §18.)
- `create_shortcut`: `target`, `parent`, `name` (default the target's).
- `trash_file` and `restore_file`: the result names the item, says
  "folder with its contents" when it is one, reminds that the trash
  empties after 30 days, and shows the location after a restore.
  `dry_run` on both.

### 7.4 Sharing (`internal/service/access.go`)

**Who decides what may be shared.** Google does, per organisation: the
Workspace admin's external-sharing setting (off, allowlisted domains, or
on, per organisational unit) is enforced by the Drive backend on every
API call, and shared drives add their own restrictions. This server does
not copy those rules. What it adds is what only the server can add:
brakes on the agent doing something the person is *allowed* to do but
did not ask for. The deployer sets `GDRIVE_SHARING`:

| Value | Meaning |
|---|---|
| `all` (default) | Every principal type the account may share with. `anyone` still needs `allow_anyone: true` on the call. |
| `off` | `share_file`, `unshare_file` and shared-drive membership changes are not registered. `list_permissions` stays. |

Before any sharing call the server reads `capabilities.canShare` on the
file and refuses with `[forbidden] you cannot change sharing on this
file` when it is false, as the sharing guide asks. A grant the
organisation's policy blocks comes back from Google as 400
`invalidSharingRequest` ("ACL change not allowed"), 400
`shareOutNotPermittedForContent` (a file flagged as restricted content)
or 403 `domainPolicy`; the server maps that family to `[blocked] your
organisation's sharing policy does not allow this` with Google's own
message attached. The exact shape of a policy refusal is observed live
in Phase 2 (§16) and recorded in §18.

`share_file` takes `file` (or a shared drive, for membership),
`principal` (`someone@example.com`, `domain:example.com`, `anyone`),
`role` (`reader`, `commenter`, `writer`, `file_organizer`, `organizer`,
`owner`), `notify` (default false; Google's default is true, and an
ownership transfer forces it on, which the result says), `message`,
`expires` (RFC 3339 or a duration like `30d`; users and groups only, at
most a year), `discoverable` (`allowFileDiscovery` for domain and anyone;
default false), `transfer_ownership: true` (required for `owner`; on a
consumer account the result explains the pending acceptance), and
`dry_run`. A principal that already has a grant is updated, and the
result says "changed from reader to writer". The text shows exposure
before and after; the JSON carries the permission id and the new sharing
summary. `unshare_file` takes `principal` or `permission_id`, and
`remove_link: true` for the `anyone` or `domain` link grant; an inherited
shared-drive permission is refused with the source named.

Publishing a revision to the web (`revisions.update published`) is an
exposure with no audience control and is deliberately not offered.

### 7.5 Shared drives

`list_drives` shows name, id, the signed-in person's role, hidden state
and restrictions, with `include_hidden`. `manage_drive` takes `action`
(`create`, `rename`, `hide`, `unhide`, `restrict`), `name`, the four
restriction flags (`domain_users_only`, `members_only`,
`copy_requires_writer_permission`, `admin_managed`), `dry_run`. Members
are added and removed through `share_file` and `unshare_file` with the
drive as the target. `delete_drive` is gated and needs an empty drive.

### 7.6 History

- `list_revisions`: newest first, with id, time, who, size, whether it is
  kept forever, and the mime type; the output repeats Google's caveat
  that the list can be incomplete for busy Docs. These are Drive revision
  ids, not the Docs API's revision tokens; content comes through
  `download_file` with `revision`.
- `manage_revision`: `action: keep | unkeep` (`keepForever`).
  `delete_revision` is gated.
- `list_changes`: with no `page_token` it returns a start token and says
  "call again later with it"; with one it lists the changes since (file
  changes with kind, name, location, removed or trashed, time; drive
  changes), then `new_start_token`. `drive` limits it to one shared
  drive; `my_drive_only` and `include_removed` (default true) map to the
  API's flags. Tokens are opaque and the model keeps them for the
  session; `search_files` with `modified_after` is the stateless
  alternative.

### 7.7 Comments (Phase 3)

`list_comments` (threads with replies, resolved state, deleted on
request), `add_comment` (unanchored; the description says that a comment
pinned to a passage of a Google Doc is a Docs API feature this server
does not offer), `reply_comment` (`action: reply | resolve | reopen |
edit`), and the gated `delete_comment`. One Drive backend; the reason
to have it here is every file that is not a Doc.

### 7.8 Access requests (Phase 3)

`list_access_requests` lists pending proposals on a file (who, the role
asked for, their message, when); `resolve_access_request` accepts with a
role or denies, with `notify`. Only approvers can, and the API cannot
create one.

### 7.9 Labels and approvals (Phase 4, Workspace)

`list_labels` reads definitions through the Drive Labels API (enabled
with `GDRIVE_LABELS=true`, which adds `drive.labels.readonly` and, in
full mode, `drive.labels` at login); `manage_labels` applies, removes or
sets fields on a file; `get_file` shows `labelInfo`. Approvals
(`list_approvals`, `manage_approval`) and the Drive Activity API
(`list_activity`, "who did what to this file") are verified live first
and added if they behave as documented.

## 8. Tool surface

snake_case verb_noun, no dots. Claude Code prefixes `mcp__<server>__`.
"Gated" means registered only with `GDRIVE_ENABLE_DESTRUCTIVE=true`;
gated tools also set `_meta["anthropic/requiresUserInteraction"]`.
`GDRIVE_READ_ONLY=true` registers only the readOnly rows and requests
`drive.readonly`. `GDRIVE_SHARING=off` removes the rows marked *sharing*.

| Tool | Purpose | Annotations | Phase |
|---|---|---|---|
| `get_account` | Who is signed in, storage used and limit, can create shared drives, sharing policy in effect | readOnly | 0 |
| `get_file` | The file card (§7.1) | readOnly | 0 |
| `search_files` | Typed search across My Drive, shared with me and shared drives | readOnly | 0 |
| `list_folder` | One page of children, or a budgeted tree | readOnly | 0 |
| `read_file` | Text of a file, budgeted with continuation | readOnly | 1 |
| `download_file` | Blob, export or old revision to `GDRIVE_LOCAL_DIR`, checksum-verified | readOnly | 1 |
| `create_file` | Empty Workspace file, or inline text with optional conversion | — | 1 |
| `upload_file` | A local file, multipart or resumable, optional conversion | — | 1 |
| `update_content` | Replace a blob's content; new revision, old one kept | — | 1 |
| `create_folder` | New folder; refuses a duplicate name unless allowed | — | 1 |
| `update_file` | Rename, describe, star, colour, properties, sharing switches | idempotent | 1 |
| `move_file` | Move to a folder or shared drive; single parent; dry run | idempotent | 1 |
| `copy_file` | Copy, optionally converting (OCR); Phase 3 adds recursive | — | 1, 3 |
| `create_shortcut` | Shortcut to a file or folder | — | 1 |
| `trash_file`, `restore_file` | Reversible removal and its undo | idempotent | 1 |
| `list_permissions` | Who has access, how, inherited from where, link state | readOnly | 2 |
| `share_file` | Grant or change access, link sharing, ownership transfer; policy-checked; before/after | *sharing* | 2 |
| `unshare_file` | Revoke a grant or a link | *sharing* | 2 |
| `list_drives` | Shared drives with your role and restrictions | readOnly | 2 |
| `manage_drive` | Create, rename, hide, unhide, restrict a shared drive | — | 2 |
| `list_revisions` | Version history with the incompleteness caveat | readOnly | 2 |
| `manage_revision` | Keep or unkeep a revision | idempotent | 2 |
| `list_changes` | The changes feed: start token, then changes since | readOnly | 2 |
| `list_comments` | Threads on any file | readOnly | 3 |
| `add_comment`, `reply_comment` | Unanchored comment; reply, resolve, reopen, edit | — | 3 |
| `list_access_requests`, `resolve_access_request` | Pending access requests; accept or deny | readOnly / — | 3 |
| `list_labels`, `manage_labels` | Workspace labels | readOnly / — | 4 |
| `delete_file` | Gated: permanent, skips the trash | destructive | 2 |
| `empty_trash` | Gated: everything in the trash, or one shared drive's | destructive | 2 |
| `delete_drive` | Gated: an empty shared drive | destructive | 2 |
| `delete_revision` | Gated: one blob revision | destructive | 2 |
| `delete_comment` | Gated: a thread or one reply | destructive | 3 |

There is deliberately no bulk delete and no bulk share: one item per
call, so "remove these forty files" is forty approvals in the client. A
folder is the unit for bulk operations, and trashing one is reversible.

**Resources** (Phase 3). `gdrive://{file}` is the text `read_file` gives
(markdown for a Doc, csv for a Sheet, plain text otherwise) under one
400 000-character budget; `gdrive://{file}/meta` is the file card;
`gdrive://{folder}/children` is the first page of a listing as markdown.
Templates with a shared prefix do not shadow each other in go-sdk v1.7.0:
a template matches through an anchored RFC 6570 pattern in which a
variable excludes `/`. No static resource list (that would be a
Drive listing) and no subscriptions (no push without a public endpoint).

**Results.** Read tools return a text block only. Write tools return text
and JSON, and the JSON carries the file card and, for sharing, the
before and after summaries.

## 9. Confidentiality, security, safety

**Nothing internal leaves the user's machine or enters the repository**
(**decided**).

- The repository never contains organisation names, file or folder ids,
  URLs, account emails, Cloud project ids, OAuth client ids or secrets,
  names or content of real files, or any reference to other projects,
  accounts, machines or tooling its maintainers use. Conventions are
  cited to primary sources. Fixtures are synthetic. gitleaks
  runs in pre-commit and CI with rules for Google client ids, client
  secrets and refresh tokens on top of its defaults.
- The server talks only to Google: `www.googleapis.com`,
  `*.googleapis.com`, `*.googleusercontent.com` (download URIs that
  `files.download` hands back), `oauth2.googleapis.com`,
  `accounts.google.com`. Every URL is checked against that allowlist
  before credentials are attached. No telemetry, no update checks.
- Logs never carry file names, paths, emails, queries or content. They
  carry truncated ids, counts, byte counts, latencies and error classes.
  `doctor` and `status` print the account to the terminal, never to the
  log.
- Files are written only under `GDRIVE_LOCAL_DIR` (cleaned, symlinks
  resolved, must stay inside) and read only from there. Unset means no
  file transfer. `GDRIVE_MAX_DOWNLOAD` caps a download (default 1 GiB).
- The sharing policy is enforced server-side; annotations are hints the
  client may not trust (spec), which is why gates are in the server.
- The refresh token lives in the keyring or a 0600 file (warned); the
  client secret file is user-owned and never logged. `logout` revokes and
  deletes.

| Risk | What limits it |
|---|---|
| Acting on the wrong file | Ids are the contract; a name or path that matches more than one item is refused; every result shows the location. |
| Exposing a file to the world or to the wrong domain | The organisation's own sharing policy, enforced by Google on every call; `allow_anyone` per call; before-and-after exposure in every sharing result; `dry_run`; `GDRIVE_SHARING=off`; no publish-to-web at all. |
| Unwanted email to people | `notify` is off unless asked; the result says when Google forced it on. |
| Mass deletion | Trash is the only default removal and it is reversible; permanent deletion and emptying the trash are gated; no bulk tool. |
| Copying private files onto disk | Downloads only under `GDRIVE_LOCAL_DIR`, size-capped, named in the result. |
| Uploading files the person did not mean to share | Uploads only from `GDRIVE_LOCAL_DIR`; inline content is what the model wrote. |
| Instructions hidden in file content | Read tools are read-only and content is returned as data; the server never acts on what a file says; the client's per-call approval covers writes. |
| Runaway listings and quota exhaustion | Page and tree budgets, per-process limiters, backoff honouring Google's reasons. |
| A stalled request hanging the server | Every call, token refresh and chunk has a deadline; bodies and frames are bounded. |
| Secrets in the repository | gitleaks in pre-commit and CI; synthetic fixtures. |

## 10. Auth, config, process model

- **Google account and Cloud project.** Each deployer runs the server
  under a Cloud project they own, with a dedicated Desktop-app OAuth
  client so its tokens can be revoked independently. Setup in the order
  `doctor` checks it: project → enable the **Google Drive API** (and the
  Drive Labels API when labels are on) → consent screen (**Internal** for
  Workspace, **External + Testing** with the user added for consumer
  accounts, which means weekly `login`) → add the scopes → Desktop-app
  client → download `client_secret.json` → `login` → `doctor`.
- **No Developer Preview is needed.** Everything this server uses is GA.
  Google's own Drive MCP is in the preview programme and is a comparison
  (Phase 0 spike B), not a dependency.
- **Scopes.** Full: `drive`. Read-only: `drive.readonly`. With
  `GDRIVE_LABELS=true`: plus `drive.labels.readonly`, and `drive.labels`
  in full mode. A missing-scope 403 becomes `[forbidden] missing scope …;
  re-run google-drive-mcp login`.
- **OAuth flow**: Google's documented desktop flow, loopback
  `127.0.0.1:<random port>` with PKCE. **Refresh token storage**, in the
  order `gh` uses: the OS keyring (Secret Service, Keychain, Credential
  Manager); on error a 0600 file under
  `os.UserConfigDir()/google-drive-mcp/` with a stderr warning;
  `GDRIVE_REFRESH_TOKEN` overrides both. **Profiles** (`GDRIVE_PROFILE`)
  keep separate secrets, tokens and accounts under `GDRIVE_CONFIG_DIR`.
- **Settings** (`GDRIVE_*` env, each with a bound flag): `PROFILE`,
  `CLIENT_SECRET`, `REFRESH_TOKEN`, `CONFIG_DIR`, `LOG_LEVEL`,
  `LOG_FORMAT`, `READ_ONLY`, `ENABLE_DESTRUCTIVE`, `SHARING`
  (`all | off`), `LOCAL_DIR`, `MAX_DOWNLOAD` (default `1GiB`),
  `HTTP_TIMEOUT` (default `60s`, applied per attempt and per transfer
  chunk), `LABELS`. Operational flags: `--version`, `--dump-schemas`.
- **Startup**: warm the token off the startup path; on failure log and
  **keep serving** with `[auth]` errors on every tool. `doctor` checks
  credentials, the token exchange, granted scopes, `about.get` (account,
  storage, `canCreateDrives`), the shared drives the account can see,
  the sharing setting in effect, that `GDRIVE_LOCAL_DIR`
  exists and is writable, the Labels API when enabled, and, given a file
  reference, `files.get` and the resolved path.
- **Transport**: stdio (**decided**).

## 11. Reliability

- **Retries.** Reads and listings retry on 429, 403 `rateLimitExceeded`
  and `userRateLimitExceeded`, 5xx and network errors with exponential
  backoff and full jitter, capped at 30 s, five attempts, honouring
  `Retry-After`. Metadata patches, moves and permission updates retry the
  same way: they are idempotent. Creates, uploads and copies retry
  because they carry a pre-generated id; a duplicate-id answer after an
  ambiguous failure is confirmed with a `files.get` and reported as
  success. **A create that cannot carry one is not retried at all**:
  Drive refuses a generated id for the Docs Editors formats, so a 500
  arriving after a new Doc was made would otherwise produce a second
  one. The request carries the exception rather than the rule naming the
  kind, so a write added later cannot inherit permission to retry
  without someone deciding that it may. `permissions.create` is not idempotent, so after a network
  failure the server lists permissions and checks before it retries.
  Resumable uploads recover through the protocol itself.
- **Limiters.** Reads and listings 10/s, burst 20. Writes 5/s, burst 10.
  Sharing 1/s, burst 3 (`sharingRateLimitExceeded` is its own quota).
  Transfers are bandwidth-bound, not count-bound.
- **Deadlines.** `GDRIVE_HTTP_TIMEOUT` per attempt and per 8 MiB chunk;
  the whole call under the MCP request context. The token refresh runs
  under the same deadline (a wrapped token source), so a stalled refresh
  cannot hang later calls.
- **Memory.** Streaming in both directions; `read_file` fetches only the
  window it shows; a listing holds one page; a tree walk holds its
  budget.
- **Caching.** Reference and path cache 60 s; file metadata coalesced
  for 5 s keyed by id; writes invalidate. Never for correctness.
- **Targets, checked by benchmarks against the fake in Phase 3.**
  `get_file` in at most two calls; a search page in one call plus at
  most twenty cached parent lookups; a path of depth *d* in *d* listing
  calls, then cached; a 1 GiB download through the fake at network speed
  with a flat memory profile; a 10 000-item tree rendered under 50 ms.

## 12. Distribution and setup

- **Artifacts.** GoReleaser builds for linux, darwin and windows on amd64
  and arm64 from a `v*` tag; `checksums.txt` is signed with a keyless
  Sigstore certificate; every archive gets a build provenance
  attestation and an SBOM; builds stamp the commit's time so a rebuilt
  tag is byte-identical; the release is never a draft. `go install
  …/cmd/google-drive-mcp@latest` is the second path, with the version
  read from build info.
- **Branches and pull requests.** `main` is released code. Every change
  reaches it through a pull request, the maintainer's own and release
  commits included, and merges once CI is green on all three platforms.
  Tags are pushed directly and one at a time (GitHub drops tag events
  past the third in one push); the release workflow takes it from there,
  and `workflow_dispatch` re-runs a release against a tag when needed.
- **Workflow hardening**, verified against GitHub's hardening guide and
  the OpenSSF Scorecard checks (§18): every action pinned to a full
  commit SHA with the version in a trailing comment; `permissions:
  contents: read` at the top of every workflow, raised only in the job
  that needs more; `persist-credentials: false` on every checkout; a
  CodeQL workflow on pushes, pull requests and weekly; Dependabot for Go
  modules and actions, weekly, grouped.
- **Client configuration** documented for Claude Code, Claude Desktop
  and Cursor; all three pass only `command`, `args` and `env`, which is
  why configuration is env-first.
- **Setup guide** in the README, in the order `doctor` checks it (§10).
- **Versioning.** Semantic versions; Keep a Changelog; the schema diff in
  CI classifies tool removals, renames and new required fields as
  breaking.
- **Documentation set.** README, this file, `docs/configuration.md`,
  `docs/security.md`, `docs/development.md`, `CONTRIBUTING.md`,
  `SECURITY.md`, `CHANGELOG.md`. Apache-2.0.

## 13. Testing

- **Unit, table-driven, no network.** `internal/gapi/drivetest` is an
  in-memory Drive behind `httptest`: files with parents and kinds, trash
  with cascade, permissions with the one-per-principal rule and inherited
  shared-drive grants, revisions, shared drives, a changes log, resumable
  upload sessions that honour `Content-Range` and answer `308` with the
  received `Range`, `alt=media` with `Range`, export stubs, and error
  injection (429, each 403 reason, 5xx, a connection cut at byte *N*).
  Every service area is tested against it. Renderer goldens live in
  `testdata/golden/`. Specific tables: every URL shape in `internal/ref`;
  path resolution (missing, ambiguous, shortcut, trashed sibling, cache);
  the sharing policy matrix (policy × principal type × role × account
  kind); local-dir confinement (`..`, symlink escape, absolute paths);
  upload chunking and resume (cut at byte *N*, expect one status query
  and continuation from the `Range`); download md5 mismatch (error, file
  removed); tree budgets; duplicate-name guard; idempotent create after
  an injected network failure.
- **Coverage floor** 80% per core package: `config`, `credentials`,
  `auth`, `gapi`, `ref`, `model`, `render`, `service`, `tools`, `server`.
- **Schema dump and diff** in CI against the last tag.
- **Stdio smoke** without credentials: initialise with the newest
  protocol version the SDK offers and with `2025-11-25`, list tools and
  resource templates, call `get_file` and expect an `[auth]` tool error.
- **Integration** (`//go:build integration`, `GDRIVE_INTEGRATION=1`):
  creates one folder "google-drive-mcp test (safe to delete)" in My Drive,
  runs every area inside it, and trashes the folder at the end. With
  `GDRIVE_TEST_WORKSPACE=1` it also creates and deletes a scratch shared
  drive and exercises domain sharing and labels. Sharing with a second
  address uses `GDRIVE_TEST_SHARE_WITH` from the environment and is
  skipped when unset. Access requests need a request made in the UI and
  are checked by hand. Ids and emails go only to the terminal.
- **Live driver** `scripts/livedrive`: every tool and every action
  over stdio against the scratch folder, with ids, URLs and addresses
  replaced by placeholders before anything is printed. Run with each
  `GDRIVE_SHARING`
  value and with `GDRIVE_ENABLE_DESTRUCTIVE` on and off before a phase is
  called done. Every `isError=True` must be an expected refusal.
- **Agent evals** `scripts/evals` (Phase 3), about twelve tasks
  through `claude -p` with only this server's tools: find a file and say
  who can see it; build a folder structure and move files into it; share
  with someone as commenter without emailing them; download a PDF; upload
  a markdown file as a Google Doc; say what changed in a shared drive
  since a token; restore a trashed file; handle a duplicate name; read
  the tail of a large text file; rename and describe; list a shared drive
  as a tree; refuse an `anyone` link the person did not ask for. Scored
  on the end state read back through the server and on the trace: no
  invented ids, ambiguity handled by asking or listing, `allow_anyone`
  never passed unasked.
- **Benchmarks** (`make bench`): path resolution, tree rendering of a
  10 000-item fake, a 1 GiB stream through the fake with a memory
  assertion.
- **CI on Linux, macOS and Windows** from the first commit: path
  handling, permission bits and line endings differ between them, and
  `.gitattributes` pins LF so goldens compare byte for byte.

## 14. Confirmed decisions and their consequences

| Decision | Consequence in the design |
|---|---|
| Deployer-owned Cloud project with a dedicated OAuth client; nothing internal in the repository | §9, §10, §12 |
| File boundary only; the content of Docs, Sheets and Slides is edited through their own APIs, not here | `read_file` exports; `update_content` refuses Workspace documents and says which API edits them |
| Ids are the contract; paths and names resolved out loud, never guessed | §6; `[ambiguous]` and `[not_found]` carry the candidates and the fix |
| Location shown everywhere | `internal/model` computes paths with a cache; the cost is counted (§7.1) |
| Google's sharing policy is the authority; the server adds only agent brakes: `canShare` checked first, `anyone` double-gated, notifications off by default, ownership explicit, `GDRIVE_SHARING=off` available | §7.4 |
| Trash by default, destruction gated, no bulk removal tool | §8, §9 |
| One local directory for transfers, unset means none | §7.1, §7.2, §9 |
| Pre-generated ids and request ids for idempotent writes | §11 |
| Raw REST, own wire types, no generated client | §5 |
| Stdio only | No HTTP auth design |
| The repository is self-contained: no code imported from, and no reference to, any other project or environment | §5, §9 |
| Phases end in a tagged release and wait for an explicit "go" | §16 |

## 15. Reliability of the plan itself: what must be verified live

Everything in §2 comes from the reference. Five things have behaviour
the reference does not pin down. The first — the exact semantics of
`name contains` and `name =` — was spiked on 2026-09-05 and **refuted
two claims this document made** (§18); the rest run at the start of the
phase that builds the code they test (§16). The lesson is recorded
rather than smoothed over: a documented API can be wrong about itself,
and a fake built from the documentation inherits the error. Every rule in
the fake that a live run has not confirmed is a rule the tests are only
agreeing with themselves about.

## 16. Delivery phases

Each phase ends in a tagged release and waits for an explicit "go".
How a phase is closed is written down at the end of this section, so
that whoever picks the work up next starts from the repository alone.

**Phase 0 — skeleton and spikes (v0.0.1). Done 2026-09-05.** Scaffolding (§5, §12):
Makefile, golangci, govulncheck, go-licenses, gitleaks, goreleaser, the
CI, CodeQL and release workflows with pinned actions and scoped tokens,
Dependabot, templates, the pull request flow in `CONTRIBUTING.md`.
`login/logout/status/doctor`; `config`, `credentials`, `userconfig`,
`auth`; `gapi` core with `about.get`, `files.get`, `files.list`,
`generateIds`; `ref` parser; `model` and `render` for the file card and
listings; `drivetest` with files, parents and listings; tools
`get_account`, `get_file`, `search_files`, `list_folder`; smoke, schema
dump, staleness, coverage floor all green on three platforms; a live run
of the four tools. Spikes, results into §18:

- **A. Search semantics**, live: **done 2026-09-05**, results in §18.
- **B. Google's Drive MCP**, only where a Cloud project enrolled in the
  Developer Preview Program is available, otherwise skipped: connect a
  client once, dump its tools and schemas, and record
  what `read_file_content` returns for a PDF, an Office file and an
  image. This keeps §1 honest and shows the bar for `read_file`. Nothing
  depends on it.
- **C. Resumable upload**, live: 8 MiB chunks, a forced interruption,
  the `308` and `Range` recovery, md5 verification; multipart under 5 MB;
  Markdown to Doc conversion.
- **D. Resource keys**, live: a link-shared file from another account
  fails without the header and succeeds with it.
- **E. Shared drives**, live on a Workspace account: `drives.list`,
  create with `requestId`, membership through permissions on the drive,
  move a file in and out, the folder-move refusal.
- **F. Ownership transfer**: Workspace direct transfer live; consumer
  `pendingOwner` only if a consumer test account exists.

A ran on 2026-09-05 and is recorded in §18; it refuted two things this
document asserted, and the fake and a tool description were wrong with
them.

B-F are **moved to the phase that builds the code they test**, because
they cannot be run here: C needs `upload_file`, the write half of E needs
`manage_drive` and `move_file`, and F needs `permissions.create` — none
of which phase 0 builds. A spike whose subject does not exist yet is a
spike that gets skipped and then forgotten. So:

- **C (resumable upload)** and the write half of **E (shared drives)**
  run at the start of phase 1, before `upload_file` and `move_file` are
  designed against assumptions.
- **F (ownership transfer)** runs at the start of phase 2, before
  `share_file` is.
- **D (resource keys)** runs whenever a link-shared file from a second
  account is available; the client already sends the header, and the
  test in `internal/gapi` covers the mechanism.
- **B (Google's Drive MCP)** needs a Cloud project in the Developer
  Preview Programme. Nothing depends on it.

**Phase 1 — content and organisation (v0.1.0). Written 2026-09-05;
not yet verified live.** `read_file`,
`download_file`, `create_file`, `upload_file`, `update_content`,
`create_folder`, `update_file`, `move_file`, `copy_file`,
`create_shortcut`, `trash_file`, `restore_file`; local-dir confinement;
`drivetest` grows uploads, downloads, trash and revisions; the live
driver covers every tool; `docs/configuration.md` and the README tool
table match the code (the staleness gate enforces it).

What is left before the tag:

- **The live run.** `go run ./scripts/livedrive -bin ./google-drive-mcp
  -write -parent <a scratch folder>` makes one scratch folder, exercises
  every write tool inside it, and trashes it again. That run is spike C
  (a 6 MiB upload takes the resumable path) and the write half of spike
  E if the scratch folder is in a shared drive. Whatever it refutes goes
  into §18 and is fixed before the release commit.
- Then the release: the status line and this entry updated, the
  `CHANGELOG.md` notes moved under `[0.1.0]`, a "Release 0.1.0" commit, a
  pull request, CI green on three platforms, the merge, and the tag.

**Phase 2 — access, shared drives, history (v0.2.0).** `list_permissions`,
`share_file`, `unshare_file` with the policy; `list_drives`,
`manage_drive`; `list_revisions`, `manage_revision`, `list_changes`;
gated `delete_file`, `empty_trash`, `delete_drive`, `delete_revision`.
The sharing matrix and the inherited-permission cases in the fake; live
against the scratch folder and the scratch shared drive, including one
share that the organisation's policy blocks (an external address on an
organisational unit with external sharing off, if the admin can set one
up) so the `[blocked]` mapping in §7.4 is built from a real response.

**Phase 3 — collaboration, resources, evals, performance (v0.3.0).**
Comments and access requests; `gdrive://` resources; `copy_file
recursive` and the tree budgets; the agent evals and the fixes they
force; `make bench` and the numbers in §11; `/simplify` and
`/code-review high` over the whole tree with findings resolved or
recorded in §17a.

**Phase 4 — Workspace extras and the rest of the API (v0.4.0).** Labels;
approvals and Drive Activity verified live and added if they behave;
`files.download` for Vids; `ocr_language`, `use_content_as_indexable_text`,
`viewed` marking, property search. Then the discovery document
(`www.googleapis.com/discovery/v1/apis/drive/v3/rest`) is diffed against
what the client calls, and every GA method is either used or listed here
as deliberately out (`channels`, `watch`, `generateCseToken`, `apps`,
`useDomainAdminAccess`).

**v1.0.0** waits for use in anger and a further eval round with a second
client.

### Closing a phase

1. `make check` green on all three platforms; the live driver run, and
   in Phase 3 the evals; the review passes done, with findings resolved
   or recorded in §17a with the reason.
2. The status line updated, the phase marked done in §16 with the date,
   what was verified added to §18, the release entry written in
   `CHANGELOG.md`.
3. Released per `docs/development.md`: a "Release N.N.N" commit on a
   topic branch, a pull request, CI green, merge, then the tag pushed on
   its own.
4. Stop and wait for "go".

## 17. Open decisions

None. The five choices below were decided on 2026-09-05 and are kept
here so they are not reopened.

1. **Sharing.** Google's organisation policy is the authority (§7.4).
   `GDRIVE_SHARING` is `all` or `off`; an `internal` mode that copied the
   admin's rules was dropped because the admin console and the Drive
   backend already enforce them per organisational unit, the sharing
   guide asks apps to check `canShare` rather than re-implement policy,
   and the MCP specification places consent at the host and access
   controls, not policy copies, at the server.
2. **Duplicate-name guard.** `create_folder`, `create_file` and
   `upload_file` refuse a same-named sibling unless `allow_duplicate:
   true`. One listing per create.
3. **One local directory.** `GDRIVE_LOCAL_DIR` serves both directions;
   unset means no file transfer.
4. **Sheets in `read_file`.** The first sheet as csv, and the output says
   so. Per-sheet and per-range reads are a Sheets API feature and belong
   to a server built on that API; this one does not add the scope.
5. **Go directive.** `go 1.27.1`, the current point release.
   `GOTOOLCHAIN=auto` fetches it where an older 1.27 is installed.

## 17b. Deviations from the shared Go MCP server standard

The standard the sibling Go MCP servers run on was adopted here on
2026-09-05. Almost all of it was already true or has been made true;
what follows is where this repository deliberately differs, so that a
difference is a decision rather than a drift.

| The standard says | Here | Why |
|---|---|---|
| A test fails if **an id** appears in a log | Full ids never appear; a **six-character prefix** does, at debug level | A retry, its backoff and its outcome are separate log lines, and without a correlation key a failure cannot be traced to the call that caused it. Six characters of a 33-character id cannot be looked up, cannot be pasted into a URL, and identify nothing on their own. Everything else the standard names — names, titles, addresses, queries, content, and whole ids — is absent and stays absent: `TestLogsCarryNoTraceOfWhatWasTouched` runs the whole surface at debug level against unmistakable fixtures and fails on any of them. *(No longer a deviation: the standard's wording is being changed to the intent it always had — a log must not identify or reconstruct its subject.)* |
| The staleness gate **deliberately fails** between the release commit and the tag | It passes, by accepting notes under an untagged version heading | Our release flow (§12) puts the release commit on a topic branch and requires CI green *before* the merge and therefore before the tag exists. A gate that fails there fails the release pull request. The gate still refuses an undocumented change: with the notes removed it fails, which is tested |
| Errors use the classes `invalid`, `not_found`, `auth`, `conflict`, `unavailable`, `unsupported` | Thirteen classes, including `ambiguous`, `blocked`, `rate_limited` and `ambiguous_outcome` | Drive's failures are not the same set. `ambiguous` is the whole addressing design (§4.1), `blocked` is the organisation's sharing policy refusing something Google permits in general (§7.4), and `ambiguous_outcome` is a write whose result is unknown. Collapsing them into `invalid` would lose the distinction a model needs to decide what to do next |

## 17a. Deferred cleanups

Raised by the phase-0 review passes and deliberately not done in phase 0.

- Spike A ran (§18). C (resumable upload), the write half of E (shared
  drives) and F (ownership transfer) are still unrun: the code they test
  exists as of phase 1 and behaves against `drivetest`, including a
  forced interruption and a session that stores part of a chunk, but no
  fake can prove Drive agrees. They run with the live driver's `-write`
  mode against a scratch folder.
- `internal/gapi/drivetest` implements the semantics of `name contains`
  from the reference. Spike A is what confirms the fake and Drive agree.
- **Two places know a file's text form.** `service.readPlan` maps a
  Google kind onto an export MIME type; `service.defaultExport` maps the
  same kinds onto a download format. They answer different questions and
  agree by hand. They belong with the one MIME registry below.
- **One MIME registry.** Five tables now carry MIME knowledge in three
  layers: `model.googleKinds`/`blobKinds` (mime to display name),
  `service.kindMimes` (kind name to query clause, which retypes the six
  Office types), `gapi.exportMimeToName` (export mime to short name), and
  phase 1 added `service.readPlan`'s export choice and
  `service.defaultExport` (Google kind to download format). The short
  names are a user-facing vocabulary living in `gapi`, which is supposed
  to speak only wire types, and the list of them is retyped by hand into
  `download_file`'s schema tag. Adding a kind is now four edits in three
  packages, and a typo shows up as a search that silently matches
  nothing. One registry keyed by MIME — display name, kind group, export
  name, the format a read takes and the format a download defaults to —
  would remove that, and the tool description would be generated from it
  rather than written out. It stays a phase-3 job: phase 2 adds no MIME
  knowledge, so the shape is already as clear as it will get, but the
  change touches every layer and is not worth doing twice.
- **A pre-generated id costs a round trip per create.** Every
  `create_file`, `upload_file`, `create_folder`, `copy_file` and
  `create_shortcut` blocks on a `files.generateIds` call for exactly one
  id, between resolving the parent and writing. `GenerateIDs` already
  takes a count, so a small pool would take that round trip off nine
  creates in ten, and the call is independent of both the parent
  resolution and the duplicate check, so it could also simply run
  alongside them. Unused ids are believed harmless — Drive documents no
  cost to leaving one unused — but "believed" is the word: this belongs
  with the phase-3 benchmarks (§11), where the saving can be measured
  and the belief checked live, rather than being added on the strength of
  a reading.
- **Schema descriptions from one source.** A Go struct tag cannot be
  composed from a constant, so the paragraph describing what a `file`
  argument accepts is written out per tool, and so are the kind and order
  lists. A templated tag that nothing expanded once shipped `${…}` to the
  model. Tests now assert that no schema carries a placeholder and that
  the kind and order lists match `service.Kinds()` and
  `service.OrderBys()`, which closes the hole; composing the descriptions
  after `mcp.AddTool` would close the duplication as well, and is worth
  doing once several more tools take a `file`.
- **Bounded concurrency for listings.** A tree walk issues one
  `files.list` per folder in series and a search page up to twenty
  `files.get` in series, with no data dependency between siblings. Quota
  is unchanged; the cost is wall-clock, roughly 2-3 s per call at 100 ms
  round trip. A fan-out of 4-8 stays inside the read limiter and would
  cut that. It belongs with the phase-3 benchmarks (§11), not before
  them: the shared item budget becomes shared mutable state the moment
  the walk is concurrent.
- **`model.Account`.** `render.Account` is the only place outside
  `internal/gdrive`'s own users that touches a wire type, and it parses
  Drive's stringified int64 storage counts itself. The tool-surface half
  of that problem is fixed (`get_account` now reports the names
  `tools.Register` actually registered), but the storage arithmetic still
  wants a model type.

## 18. Evidence log: conventions checked, changed, or rejected

Verified 2026-09-05 against the Drive API v3 reference and guides on
developers.google.com, the go-sdk release notes, the Go 1.27 release
notes, GitHub's Actions hardening guide, the OpenSSF Scorecard checks,
and the issue trackers named in §1. "(convention)" marks a practice that
was checked rather than assumed.

| Convention | Verdict | Effect |
|---|---|---|
| Google has an official Drive MCP (my assumption: no) | Refuted: `drivemcp.googleapis.com/mcp/v1`, Developer Preview, eight tools, remote HTTP, Web OAuth client with the host's callback, `drive.readonly` + `drive.file` | §1 states what it does; spike B records its schemas; this server stays stdio, per-user, full-scope |
| The Drive API has no preview-only features this server would want (my assumption) | Confirmed for everything in §8: files, permissions, drives, revisions, comments, changes, access proposals, labels and approvals are GA methods in the v3 reference | No preview enrolment in the setup guide; an enrolled project is needed only for spike B, which is skipped without one |
| Quotas are per-request counts (inherited from the Docs limits) | Refuted: Drive charges units per method (read 5, list 100, download 200, edit 50; 325 000 per minute per user) | Listings are the expensive call; budgets and caches in §7.1 and §11 |
| `name contains` is substring search (a common assumption in the OSS servers) | **Confirmed refuted, live (spike A, 2026-09-05)**: `name contains 'nferences'` returns 0 against an account where `name contains 'Conferences'` returns 6. A prefix match on words, not a substring match | Typed fields with the semantics in the description; `name =` for paths |
| `name contains` requires its words to be consecutive and in order (my reading of the reference, and what `drivetest` implemented) | **Refuted live (spike A)**: "Decentralized Identity" and "Identity Decentralized" return the same 28 items, and "Decentralized Ide" returns 48. Every word must prefix *some* word of the name; order and adjacency are irrelevant | `drivetest` corrected; the `search_files` description had told the model the opposite, and was corrected with it |
| `name = 'x'` is an exact, case-sensitive match (§2 as written, and what `drivetest` implemented) | **Refuted live (spike A)**: Conferences, conferences and CONFERENCES each return the same single item. It matches the whole name and ignores case | `drivetest` corrected. It matters beyond the fake: path resolution uses `name =`, so two siblings differing only in case both match one lookup and are `[ambiguous]` rather than one silently winning. The fake had been returning one, so that path was never tested |
| `fullText contains` matches whole tokens only | **Refuted live (spike A)**: `fullText contains 'nferences'` returns 4. Drive's content index is looser than whole-token matching, in a way the reference does not describe | `drivetest` keeps the stricter whole-token rule deliberately: a fake that matches *less* than Drive fails a test production would pass, which gets noticed, while one that matches more hides a query that finds nothing |
| `incompleteSearch` fires often enough with `allDrives` to need handling (my assumption) | Refined live (spike A): it did not fire once, including on a 200-result search across 21 shared drives. Rare, not absent | The handling stays — Google documents it and it is one line of output — but it is not a common path |
| A file can have several parents (older Drive) | Refuted: single parent since 2020; `addParents`/`removeParents`; a My Drive folder cannot move into a shared drive | `move_file` design in §7.3 |
| Uploads of any size go in one request | Refuted: 5 MB for simple and multipart; resumable above, 256 KiB multiples, `308` recovery | `upload_file` in §7.2; spike C |
| Exports have no size limit | Refuted: 10 MB for `files.export`; `files.download` (long-running, 24 h) exists for Vids and for revisions of Docs and Sheets | `download_file` chooses the method by kind and revision |
| Permission types need an email (OSS bug #131) | Refuted: `domain` needs `domain`, `anyone` needs nothing; users and groups need `emailAddress` | Principal syntax in §7.4 |
| Notification mail is optional everywhere | Refined: default true for users and groups, cannot be off for an ownership transfer | `notify` default false; the result says when Google forced it |
| Ownership transfers are immediate | Refined: Workspace yes; consumer accounts go through `pendingOwner` and acceptance | Explained in the result; spike F |
| Inherited shared-drive permissions can be removed on the file | Refuted: `permissionDetails.inherited` grants are removed at their source | `unshare_file` names the source |
| Anyone can trash a file they can edit | Refuted: My Drive trash needs the owner; shared drives depend on role | The file card shows `can trash` from `capabilities` |
| `revisions.get?alt=media` downloads old Docs content (my assumption) | Refuted by the revisions guide: Docs and Sheets revisions come through `files.download` with `revisionId`; `files.export` takes no revision | `download_file revision:` branches by kind |
| Comments need no `fields` parameter | Refuted by the comments guide: mandatory on every `comments` call except delete | Sent on every call |
| Link-shared files open by id alone | Refuted: resource keys, `X-Goog-Drive-Resource-Keys` | §6; spike D |
| `drive.file` is enough for a per-user CLI | Refuted: it reaches only files the app created or the user opened through the Picker | Full mode uses `drive` |
| `files.create` cannot be made idempotent | Refuted: `files.generateIds` ids go in the body of `create` and `copy` | §11; retries are safe |
| A pre-generated id works for every create (§11 and §7.2 as written, and what phase 1 shipped) | **Refuted live, 2026-09-05, on the first `create_file`**: `403 Generated IDs are not supported for Docs Editors formats.` A folder takes one — same run — so the refusal is per format, not per Google-native type | Ids are sent only where Drive takes them. A new Doc, Sheet, Slides deck, Drawing or Form is created without one, which makes it the one create that is *not* idempotent, so it is also never retried on a 5xx: a 500 can arrive after the file exists. `drivetest` now refuses the id with Google's own sentence, and a test drives all five formats |
| A write may be retried on a 5xx because writes carry pre-generated ids (phase 0's `retryable`, which named the request kind) | Refuted by the finding above, and by a sibling project hitting the same shape from the other end: a rule stated for "writes" and tested through one kind of write silently stops covering a second kind added later. Here the second kind was a create that *cannot* carry an id | `retryable` reads a per-request `unsafeToRepeat` instead of the kind, set where the create is built. A test asserts that a create without an id is attempted once and one with an id twice, and it fails against the old rule |
| Shared-drive creation can be retried freely | Refined: `requestId` is required and makes it idempotent | `manage_drive create` keeps the id for the retry |
| Shared drives are available to every account | Refuted: Workspace editions only; `about.canCreateDrives` | `get_account` and `doctor` report it; tests skip on consumer accounts |
| Labels are a Drive API feature | Refined: applied through the Drive API, defined through the separate Drive Labels API with its own scopes | Phase 4 with `GDRIVE_LABELS` |
| Access requests can be created through the API | Refuted: listed and resolved only | §7.8 |
| Approvals are part of Drive v3 (my assumption: no) | Confirmed present in the v3 reference (`approvals.*`), edition and behaviour unverified | Phase 4, verified live first |
| Drive Activity is in Drive v3 | Refuted: a separate API (`driveactivity.googleapis.com`, v2) with its own scopes; its overview page returned 404 during this check | Phase 4, verified before design |
| go-sdk latest is v1.7.0 | Confirmed (proxy, 2026-07-27); v1.8.0-pre.2 tagged 2026-09-04 hardens bounds and adds `SupportedProtocolVersions`; protocol `2026-07-28` supported | Pin v1.7.0; smoke tests two protocol versions |
| Go latest is 1.27.x | Confirmed: 1.27.1 is current; 1.27 brings generic methods, `encoding/json/v2` behind `encoding/json`, a `uuid` package (useful for `requestId`) | §17 item 5 |
| Read tools return text only (convention) | Confirmed by observation of Claude Code 2.1 (2026-09-03): when a result carries both a text block and `structuredContent`, the model is shown only the structured form, so a read returning both looked like metadata; the spec makes the text block the backwards-compatible form | Kept; write tools return both, and their JSON carries everything the text does |
| `[class] message` errors, flat schemas, snake_case names, server-side gating, keep serving without credentials, keyring → file → env, loopback PKCE, env-first config with bound flags (convention) | Confirmed against primary sources: the MCP spec requires only `isError` and treats annotations as untrusted, so gates must be server-side; `gh` falls back from the keyring to a file; Google's native-app guide requires loopback with PKCE (OOB blocked since 2023); Claude Code, Claude Desktop and Cursor pass only `command`, `args` and `env`; a server that exits at startup shows the person "failed to connect" and the model never learns why | Kept unchanged |
| Duplicate names are the model's problem (my first thought) | Changed in review of the OSS failures: a lost result creates the same folder twice and nothing refuses it | The guard in §7.3, pending §17 item 2 |
| Bulk operations belong in the tool surface | Rejected: one item per call keeps every removal and every share a visible approval; a folder is the unit of bulk work | §8 |
| The server should carry its own "internal only" sharing mode (my first design) | Refuted in review: the Workspace admin's external-sharing setting (off, allowlisted domains, on; per organisational unit) is enforced by Google on the API, the sharing guide's instruction to apps is to check `capabilities.canShare` before offering the action, and the MCP specification asks servers for "appropriate access controls" while putting consent at the host | `GDRIVE_SHARING` is `all` or `off`; `share_file` checks `canShare` first; policy refusals map to `[blocked]` |
| A policy-blocked share has one documented error | Refuted: the handle-errors guide lists 400 `invalidSharingRequest` ("ACL change not allowed") and 403 `domainPolicy`; 400 `shareOutNotPermittedForContent` is seen in the wild for restricted content; the external-sharing admin page says nothing about the API | Mapped as a family in §7.4; the real shape is recorded in Phase 2 |
| Pinning actions to a major tag (`@v7`) is enough | Refuted by GitHub's hardening guide, verified 2026-09-05: "Pinning an action to a full-length commit SHA is currently the only way to use an action as an immutable release"; a tag can be moved by anyone who can write to that repository ([Security hardening for GitHub Actions](https://docs.github.com/en/actions/security-for-github-actions/security-guides/security-hardening-for-github-actions)) | Every action pinned to a full SHA with the version in a trailing comment, which is also what Scorecard's Pinned-Dependencies check scores; Dependabot updates the SHAs and keeps the comment in step |
| Declaring `permissions` once at the top of a workflow is least privilege | Refined: Scorecard's Token-Permissions wants the top level read-only and write raised per job ([Scorecard checks](https://github.com/ossf/scorecard/blob/main/docs/checks.md)) | Top level `contents: read` everywhere; the release job raises `contents`, `id-token` and `attestations`; the CodeQL job raises `security-events` |
| A checkout may leave its token on disk for later steps | Rejected: nothing in these workflows pushes with it, and GitHub's guide treats persisted credentials as avoidable exposure | `persist-credentials: false` on every checkout |
| Tests and vulnerability scanning are enough static analysis | Refuted by Scorecard's SAST check, which names CodeQL | A CodeQL workflow on pushes, pull requests and weekly, so a rule added after a merge still reaches old code |
| go-sdk v1.7.0 negotiates protocol `2026-07-28` when a client asks for it (assumed from the release notes) | Refuted by reading `mcp/shared.go` and by driving the built binary: `negotiatedVersion` caps every `initialize` handshake at `2025-11-25`, because `initialize` is itself deprecated in `2026-07-28`. The newer version is reachable only through the newer handshake. | The stdio smoke test asks for both versions and asserts a working session, not a particular number; the version the server answers with is `2025-11-25` either way |
| A Drive id can be told from a file name by shape (assumed) | Refined: ids use the URL-safe base64 alphabet and run 28-44 characters for files and 19 for shared drives, so a floor of 15 separates them from names, which almost always carry a space, a dot or punctuation. The residue is real though: a name like `QuarterlyReviewNotes` clears the bar. | `internal/ref` treats a 15+ character word in that alphabet as an id; `internal/service` retries it as a name under My Drive when no file has that id, and an id lifted out of a URL is never retried that way |
| A truncated folder path can be shown with a trailing ellipsis (my first cut) | Rejected in review: `My Drive/2026/…` states that 2026 sits directly under My Drive, which is exactly the wrong claim to make when the ancestors are the part that is unknown | `model.Location` carries `Above`, and an unread ancestor renders where it actually is: `My Drive/…/2026` |
| A file in a shared drive with no permissions of its own is private (implied by the permission list) | Refuted: it is reachable by everyone with access to the drive. A summary reading "private to you" would be wrong in the direction that matters | `model.Sharing` carries the drive name and says "everyone with access to the shared drive X can see it" |
| A read tool's location line can be truncated with a trailing ellipsis when the walk runs out of budget (my first cut) | Rejected in the phase-0 review: `My Drive/2026/…` asserts that 2026 sits directly under My Drive, and the unknown part is the ancestors, not the descendants | `model.Location.Above` puts the gap where it is: `My Drive/…/2026` |
| A not-found path should report how many items the folder holds (§6 as written) | Refined in the phase-0 review: the extra listing costs 100 units and a bare count only prompts a second lookup. The same one call can name the siblings | `notFoundInFolder` names up to 12 siblings, folders marked, and points at `list_folder` beyond that |
| Falling back from an unknown id to a name lookup is free (my first cut) | Refuted by measurement: a 5-unit 404 became 205 units, and a stale or mistyped id is the commonest bad input a model produces. Drive ids are base64 of random bytes, so one with no digit essentially does not occur | The fallback fires only for a word with no digit in it, which is the shape a file name has and an id does not |
| A shared-drive listing may filter hidden drives in the client | Rejected in the phase-0 review: hiding a drive is a sidebar setting with no API equivalent, and the fiction had already propagated into the fake, which invented a query-string meaning for it | `gapi.ListDrives` returns what Drive returned; `internal/service` decides what `Drive.Hidden` means |
| A path is resolved by following whatever it names, shortcuts included (my first cut) | Refuted in the phase-0 review: a shortcut part-way along a path is a way through to a folder, but the last segment is the thing the caller named. Following it made `get_file "/Projects/Budget shortcut"` describe the target, silently, and cached the target's id for that path, so a later `trash_file` on that path would have destroyed the wrong file | `walk` follows only the intermediate segments; the final one obeys `ResolveOptions.FollowShortcut`, and the two answers are cached separately |
| An empty permission list means the file has no grants (implied by the API's shape) | Refuted in the phase-0 review: a list that was read and is empty, and one this account may not read, were the same `nil`. A shared-drive file with no grants of its own was reported as "sharing unknown" instead of "everyone with access to the drive can see it", understating exposure — the one direction §9 exists to prevent | `permissionsFor` returns whether the list was read at all, and `NewSharing` takes it as an argument |
| Rate limiting only has to gate the first attempt of a call | Refuted in the phase-0 review: retries are triggered by 429 and by Google's three rate-limit reasons, so exempting them pushes hardest exactly when Drive has asked for less. Four of five attempts bypassed the limiter | The limiter is taken inside the retry loop, once per attempt |
| An empty result page needs no footer | Refuted in the phase-0 review: Drive returns empty pages that carry a `nextPageToken`, and an `incompleteSearch` that matched nothing is the case where the warning matters most. Both were being suppressed | The footer (note, incomplete-search warning, continuation) is written whether or not the page had rows |
| Shell with a little Python is fine for the gates (my first cut) | Rejected: it put a Python interpreter on the `make check` path of a single-static-binary Go project, to parse JSON that Go parses natively, and the gate code was the only code here exempt from gofmt, vet, lint and tests. Porting it also found two defects the shell had masked — a coverage floor that folded `drivetest` into `internal/gapi`, and a server that exited non-zero when a client disconnected mid-request | `scripts/gates` and `scripts/livedrive` are Go packages, built and vetted with everything else; `pre-commit` (itself a Python tool) is replaced by a git hook that calls the same gate |
| `files.copy` can bring the comments with it (§7.3 as written) | **Refuted in phase 1** against the v3 reference: `files.copy` takes `ignoreDefaultVisibility`, `includeLabels`, `includePermissionsForView`, `keepRevisionForever`, `ocrLanguage` and `supportsAllDrives`, and nothing about comments. The parameter existed in v2 | `copy_comments` dropped from `copy_file` and from §7.3 |
| An old revision of a Docs editors file is fetched with `files.download` (§18, from the revisions guide) | Refined in phase 1: `files.download` is a long-running operation that hands back an `Operation` to poll, while the `Revision` resource itself carries `exportLinks` for exactly this — a direct URL per format, on a Google host the allowlist already permits. The simpler documented route was taken | `download_file revision:` reads the revision, then fetches its export link. `files.download` stays for Vids in phase 4. To be confirmed by the live run |
| Drive's structural refusals arrive with their own status | Refuted by the fake once it answered with Google's real reason: `teamDrivesFolderMoveInNotSupported` comes back as **403**, and the error mapping tested the status before the reason, so "this cannot be done" was reported as "you may not". A model told `[forbidden]` goes looking for permissions to change; there are none | The reason is matched before the generic 403, and the folder-move refusal is `[unsupported]` with the way round it. Phase 0's own tests had never seen the real reason: the fake refused the move without one |
| "Flat schemas" means every argument is a scalar (convention, phase 0) | Refined in phase 1: `update_file` has to tell "leave this alone" from "set it to false", which is a nullable boolean (`type: ["null", "boolean"]`), and Drive's custom properties are a map. Both are still one level deep — a model fills them in without building a structure | The rule is now "no nested objects and no arrays of objects"; the schema test checks scalars, nullable scalars, and maps of scalars, and nothing else |
| A resumable upload's chunk is either stored whole or not at all (implied by the protocol) | Refuted by the protocol itself: the `308` carries a `Range` naming what the session actually holds, which may be less than the chunk just sent. A client that assumes the whole chunk landed skips those bytes and uploads a corrupt file | The upload keeps the current chunk in memory and resends from wherever the session says it got to; `drivetest` has a `ChunkLimit` that makes it store part of a chunk, so the path is tested rather than assumed |
| A download can be bounded by the per-attempt timeout, like every other call | Refuted: `GDRIVE_HTTP_TIMEOUT` is 60 s by default and a large file cannot arrive inside it, so the deadline that protects a metadata call would abort every real download | The deadline covers the response headers; the body is guarded by a stall timer that runs only while a read is outstanding, so a connection that stops sending is cut and one that is merely slow is not |
| A file too large to hold in memory is too large to read (the "up to 20 MB" in §7.1 as written) | Refuted in the phase-1 review: `read_file` fetches a byte range, so the file's size never reaches the process. The guard refused a 200 MB log and advised retrying with `max_chars` and `offset`, which the guard ignored — a refusal whose own advice leads back to itself. The tool description, written from the design's other half, had promised the opposite | The limit is gone; a range request is bounded by the window, not by the file |
| A create cannot invalidate a cached path (my reasoning when narrowing the cache flush) | Refuted in the phase-1 review, and it was the more dangerous half: a create *makes* a path ambiguous. A second `notes.txt` beside the first leaves the cached `(folder, name)` entry answering with the older file's id, which is exactly what §4.1 exists to prevent, and for the full 60 s of the path cache | A write evicts the entry for its own `(parent, name)` whatever it did; only a rename, move or trash also evicts by value. The cache key is built in one function so an eviction cannot spell it differently from a lookup |
| A resumable upload makes progress or fails (implied by the protocol) | Refuted in the phase-1 review: a `308` whose `Range` header is absent reads as "nothing stored", which is also what a proxy that strips the header produces. Neither the backwards guard nor the too-far guard fires, so the same chunk is sent for as long as the deadline allows | A no-progress counter, tested against a fake session that answers 308 and stores nothing |
| A host allowlist may strip the port before matching (phase 0's `googleHost`) | Refined in phase 1, prompted by a sibling project's opposite reading: stripping means `https://www.googleapis.com:8443` is allowed. Google's endpoints carry no port, and phase 1 added a path that fetches a URL taken out of a **response body** (a revision's export link), which makes this check load-bearing rather than a formality | A host with a port is refused. Where a check that decides whether an access token leaves the machine is going to be wrong, it should be wrong in the direction of refusing |
| Pinning an action to a full commit SHA pins what that step does | Refuted live: the first `v0.0.1` release failed because `cosign-installer` was pinned by SHA while the cosign it installs was not, and cosign 3 had moved from `--output-signature`/`--output-certificate` to a single `--bundle`. The action was immutable; its effect was not | `cosign-release` and `syft-version` are named alongside the action SHAs. A step that installs a tool has two versions, and pinning one of them is the more dangerous half of the job, because it looks done |
| A release workflow that has never run can be trusted because its parts are pinned | Refuted by the same failure. It had been validated with `goreleaser check` and a full local `goreleaser build`, and still failed at the signing step, which only runs with an OIDC token in CI | The first tag of a project is a test of the release path as much as of the code; `docs/development.md` says to verify the published artifacts from outside rather than trust the workflow's own green tick |
| Direct pushes to `main` by the maintainer are fine for a one-person project | Rejected: `main` is released code, and a rule with an exception for the person who releases is not a rule; Scorecard's Branch-Protection asks for pull requests gated by a passing check, and its two-reviewer tier cannot apply to a single maintainer | Pull requests required with CI green on three platforms as the gate; the review count does not apply; tags pushed directly, one at a time |
