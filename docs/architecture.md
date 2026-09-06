# Architecture — google-drive-mcp

**Status:** phase 5 complete (2026-09-06), released as v1.0.0. A default
build registers **31** tools; eight more exist behind a flag — the
destructive five, plus `list_labels` and `manage_labels` under
`GDRIVE_LABELS` and `list_activity` under `GDRIVE_ACTIVITY`. Those last
three each need a Google API enabled in the Cloud project AND a scope the
consent screen would otherwise not carry, which is why they are off by
default — 39 tools in all, and 13 in read-only mode. Phase 5 added no
tools. Every method of all three APIs is recorded as used on purpose or
left out on purpose, and a gate holds the record to the code.

**What phase 5 was for.** Everything was implemented; the question was
what had never actually been run. The destructive five had not, because
`empty_trash` cannot be scoped to a folder and reaching it meant emptying
a real account's trash. Neither had an approved approval, because an
approved file is locked and a scratch folder cannot be trashed around
one. Both wanted the same thing — a shared drive the driver makes and
destroys again — and building it took four runs, of which the first
three each found a defect.

**One mistake, six times.** The findings that mattered are all the same
mistake: **a message asserted an outcome from the REQUEST instead of
reading it from the response.** `list_activity` told every account with
activity enabled that Google had added a thirteenth action kind, on
entries that are ordinary. `lock_file` said "the file is LOCKED: nobody
can change its content" and printed it directly above a card showing no
restriction, with the next content change succeeding. `empty_trash` said
"everything that was in it is gone for good" — from a method the
reference gives no response at all — while a file trashed seconds
earlier survived and was restored on the next call. None of the three
could fail a test here, because the fakes were built from the same
belief as the code.

**And one about this document.** Phase 4 saw the right thing live and
recorded the wrong shape: it wrote that an activity with no action
arrives with `primaryActionDetail` MISSING, where Drive sends `{}`. A
probe that asks whether a member is falsy cannot tell those apart. The
code was then built to the record rather than to the response, so the
branch meant to fix it never ran, and both test fixtures were written
from the belief — the test passed while asserting the opposite of what
Drive does. §18 now keeps responses, or something a decoder derived from
one, rather than a sentence recalling it.

**Six eventually-consistent surfaces, not one.** The changes feed and the
property index were known. Phase 5 added four: `drives.list` after a
create (a drive unreachable by its own id, which stranded a scratch
shared drive in a real Workspace twice), a shared drive's trash count,
the trash surviving `emptyTrash`, and `revisions.get` after a write.
Eventual consistency is a property of Drive, not of one endpoint.

**Three claims about automatic guards turned out to be false**, all found
in one session: §17a described a test that does not exist, §18 described
a test that nothing reached, and the redaction coverage test said a new
renderer would fail it when the map is hand-written. Two are now true and
one is deleted. The pattern is worth naming: a guard that would be
expensive to make automatic gets described as though it were, because the
description is free.

**What phase 4 does not have:** `manage_labels` verified live, which
needs an administrator to publish one label; spike F and a policy-blocked
share, still blocked on a second account and an administrator; the
destructive five; and an approval carried through to APPROVED, which
locks the file and so wants a scratch shared drive. All are in §17a with
what stands in for them.

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
| **Downloads**: `files.get?alt=media` for blobs, with `Range` for partial reads and `acknowledgeAbuse` for flagged files; `files.export` for Workspace documents, capped at **10 MB**, no `Range`; `files.download` (a long-running operation, alive **a minimum of 12 h**) for Google Vids and for *revisions* of Docs and Sheets. Export formats: Docs to docx, odt, rtf, pdf, txt, html, zip, epub, md; Sheets to xlsx, ods, pdf, zip, csv, tsv; Slides to pptx, odp, pdf, txt; Drawings to pdf, jpg, png, svg. | `read_file` fetches only the byte window it shows; `download_file` streams to disk and verifies the md5; old revisions of Docs go through `files.download`. |
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
internal/mediatype/       what a media type means, in one table: display name, kind filter, export
                          name, the format a read takes, the format a download defaults to
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
internal/server/          SDK wiring; the gdrive:// resource templates; schema dump through an
                          in-memory client session
internal/tools/           one file per area: files.go, content.go, organise.go, access.go, drives.go,
                          history.go, comments.go, resources.go, tools.go
internal/version/
testdata/                 synthetic fixtures (§14) and golden outputs
docs/
scripts/gates/            the repository's own checks, as Go: coverage floor, schema diff,
                          stdio smoke, staleness, leaks, pins, error classes, pre-commit;
                          never shipped
scripts/livedrive/        drives the built binary against a real account, redacting ids,
                          links and addresses before anything is printed
scripts/evals/            drives an agent against the built binary and scores the end state
                          and the trace
scripts/internal/         the stdio client and the redactor those two share
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

### 7.7 Comments (built in phase 3)

`list_comments` (threads with their replies inline, resolved state,
tombstones on request), `add_comment` (unanchored; the description says
that a comment pinned to a passage of a Google Doc is a Docs API feature
this server does not offer), `reply_comment` (`action: reply | resolve |
reopen | edit`), and the gated `delete_comment`. One Drive backend; the
reason to have it here is every file that is not a Doc.

Four things the discovery document settled before any of it was written
(§18):

- **`fields` is required** on `comments.list`, `get`, `create` and
  `update`. Drive answers 400 without it, and this is the only corner of
  the API that does. `drivetest` enforces the same rule, and a test
  asserts the parameter is on the wire rather than trusting four passing
  calls.
- **No `supportsAllDrives`.** The comment and reply methods take no such
  parameter, so a comment on a shared-drive file is reached without one.
- **Replies come inline** with the comment, in chronological order, so a
  listing of threads costs one call rather than one per thread.
- **An author's email address is never populated** on a comment or a
  reply, so neither is asked for: it would be an always-empty field and
  an address to redact for nothing.

Resolving is a reply carrying an action, because Drive has no field on
the comment to set. That is why it lives in `reply_comment` rather than
in a state-setting tool, and why resolving a thread is visible to
everybody who can see the file. Resolving one that is already resolved
reports "unchanged" instead of adding a second reply.

Comment text is written by anybody who can reach the file. The renderer
quotes every line of it, so a comment reaches a model as data and never
as a line that could pass for this server's own.

### 7.8 Access requests (built in phase 3)

`list_access_requests` lists pending proposals on a file (who asked, who
would receive the access, the roles asked for, their message, when);
`resolve_access_request` accepts with a role or denies, with `notify`.
Only approvers can list them, and the API cannot create one.

Two corrections from the discovery document:

- **A proposal asks for a LIST of roles**, not one. A request naming
  several is `[ambiguous]` unless the caller says which to grant; a
  request naming exactly one is accepted as that one.
- **`resolve` answers with no body at all** — the method has no response
  type. So what an acceptance did cannot be reported from the call: the
  exposure after is read back, the same way `share_file` reads it.

Accepting grants a permission, so `resolve_access_request` is a
**sharing** tool: `GDRIVE_SHARING=off` removes it,
`capabilities.canShare` is checked first, and the result carries the
exposure before and after. §8's table marked it as an ordinary write
when it was written; hard rule 4 decides otherwise. `list_access_requests`
stays registered with sharing off, because reading who is waiting is not
widening anything.

### 7.9 Labels, approvals and activity (Phase 4, Workspace)

**Labels** come through two APIs and the split is the thing to keep
straight. The VALUES on a file are Drive's — `files.listLabels` reads
them and `files.modifyLabels` writes them, both on the ordinary `drive`
scope. The DEFINITIONS are the separate Drive Labels API, on its own
host, with its own scopes and its own enablement in the Cloud project.

`GDRIVE_LABELS=true` registers both tools and adds the label scopes at
login. It governs both even though applying needs neither, because
applying needs a label id and a field id and those exist only in the
definitions: `manage_labels` without `list_labels` would be a tool whose
arguments nobody can find out. Where the definitions are out of reach,
apply and remove still work and only `set_field` is refused — the API
has one setter per field type, and the type is in the definition.

`list_labels` fixes `publishedOnly` and `LABEL_VIEW_FULL` rather than
exposing them: a draft cannot be applied, and the basic view omits the
fields, so either would produce a listing that cannot be acted on.
`manage_labels` takes one label and one field per call and validates the
value against the definition, answering a wrong selection choice with the
choices that would have worked — a choice id is generated and
unguessable. The gate is `canModifyLabels`, not `canEdit`: Drive computes
a capability for exactly this and the two come apart.

`get_file` shows the labels on a file through `files.listLabels`, which
costs a second call. `includeLabels` would fold them into the first, but
only for ids the caller already knows, and the card's whole question is
which labels are on this file.

**Approvals** are `list_approvals` and `manage_approval` (start, approve,
decline, cancel, comment, reassign), on the ordinary scope. Two things
about them are not what the name suggests and are said in the tool
description and in every result: every verb MAILS somebody, with no
notify flag anywhere, unlike sharing; and an approval can LOCK the file,
at once with `lock_file` or on approval under the default
`RESET_APPROVAL` behaviour. Declining completes an approval on its own
where approving waits for everybody. Drive cannot remove a reviewer, so
the tool says so rather than offering an argument that always fails.
They are not behind `GDRIVE_SHARING`: an approval grants nobody access,
and what it can do is restrict rather than widen.

**Drive Activity** is `list_activity`, behind `GDRIVE_ACTIVITY=true`
because it needs `drive.activity.readonly`. Its honest limit is in the
output: the API identifies a person by a People API resource name and
gives no display name or address, so the tool says "you" or "somebody
else" and a line at the end says why. A folder answers two very
different questions — what happened TO it, which is almost always
nothing, and what happened inside it — so the head says which was asked.

## 8. Tool surface

snake_case verb_noun, no dots. Claude Code prefixes `mcp__<server>__`.
"Gated" means registered only with `GDRIVE_ENABLE_DESTRUCTIVE=true`;
gated tools also set `_meta["anthropic/requiresUserInteraction"]`.
`GDRIVE_READ_ONLY=true` registers only the readOnly rows and requests
`drive.readonly`. `GDRIVE_SHARING=off` removes the rows marked *sharing*
— which includes `resolve_access_request`, because accepting a request
grants a permission.

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
| `copy_file` | Copy, optionally converting (OCR); `recursive` walks a folder, refusing a tree over budget rather than copying half of it | — | 1, 3 |
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
| `list_access_requests` | Who has asked to be let in, and for what | readOnly | 3 |
| `resolve_access_request` | Accept or deny one; accepting grants a permission, so it is policy-checked with before and after | *sharing* | 3 |
| `list_labels`, `manage_labels` | Workspace labels: the definitions this account may use, and applying one to a file. Registered only with `GDRIVE_LABELS=true` | readOnly / — | 4 |
| `list_approvals` | The reviews on a file, and whether one waits on you | readOnly | 4 |
| `manage_approval` | Start, answer, withdraw, comment on or reassign a review. Every action mails somebody; an approval can lock the file | — | 4 |
| `list_activity` | What happened to a file or inside a folder. Registered only with `GDRIVE_ACTIVITY=true` | readOnly | 4 |
| `delete_file` | Gated: permanent, skips the trash | destructive | 2 |
| `empty_trash` | Gated: everything in the trash, or one shared drive's | destructive | 2 |
| `delete_drive` | Gated: an empty shared drive | destructive | 2 |
| `delete_revision` | Gated: one blob revision | destructive | 2 |
| `delete_comment` | Gated: a thread or one reply | destructive | 3 |

There is deliberately no bulk delete and no bulk share: one item per
call, so "remove these forty files" is forty approvals in the client. A
folder is the unit for bulk operations, and trashing one is reversible.

**Resources** (built in phase 3). `gdrive://{file}` is the text
`read_file` gives (markdown for a Doc, csv for a Sheet, the file's own
type otherwise) under one 400 000-character budget, and the media type
of the answer says which; `gdrive://{file}/meta` is the file card and
`gdrive://{folder}/children` is the first page of a listing, both as
`text/plain` — the renderer lays out columns, not markdown, and saying
markdown would be a claim about the bytes that is not true.

Templates with a shared prefix do not shadow each other in go-sdk v1.7.0,
and this is now asserted rather than asserted about: a template matches
through an anchored RFC 6570 pattern in which a simple-expansion variable
excludes `/`. A reserved expansion (`{+file}`) would shadow, and is
deliberately not used. The price is that a reference containing a slash —
a path, a URL — must arrive percent-encoded; ids need no encoding, and
ids are the contract.

A resource that is not there carries both halves: the protocol's
not-found code with the uri in its data, and the sentence saying what to
do next. The SDK's own `ResourceNotFoundError` gives only the first,
replacing the message with fixed words.

No static resource list (that would be a Drive listing) and no
subscriptions (no push without a public endpoint).

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
  in full mode. With `GDRIVE_ACTIVITY=true`: plus
  `drive.activity.readonly`, in both modes — the Activity API has no
  write. A missing-scope 403 becomes `[forbidden] missing scope …;
  re-run google-drive-mcp login`. Both extra APIs also have to be
  enabled in the Cloud project; the scope and the enablement fail the
  same way, and the tools name both steps in the refusal.
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
  chunk), `LABELS`, `ACTIVITY`. Operational flags: `--version`,
  `--dump-schemas`.
- **Startup**: warm the token off the startup path; on failure log and
  **keep serving** with `[auth]` errors on every tool. `doctor` checks
  credentials, the token exchange, granted scopes, `about.get` (account,
  storage, `canCreateDrives`), the shared drives the account can see,
  the sharing setting in effect, that `GDRIVE_LOCAL_DIR`
  exists and is writable, the Labels API when enabled, and, given a file
  reference, `files.get` and the resolved path.
- **Transport**: stdio (**decided**).

## 11. Reliability

**Drive's reads lag Drive's writes, on six endpoints and counting.** This
is a property of the API rather than of one call, and the list is here
rather than only in §18 because §18 is where a finding is recorded and
this is where somebody writing the next call site looks.

| Surface | What lags | What this server does |
|---|---|---|
| The changes feed | A write is not in the feed it was made against | The token design absorbs it; the driver asks twice |
| The property index | A file tagged seconds ago does not match a property search | Nothing server-side — an empty page is a valid answer. The driver reports UNVERIFIED rather than failing |
| `drives.list` after `drives.create` | A new shared drive is absent for minutes | `findDrive` falls back to `drives.get`, which is not lagged |
| A shared drive's trash count | An item trashed seconds ago counts as nothing | The note says the count lags |
| `files.emptyTrash` | An item trashed seconds ago survives it | The note says the method has no response and the view lags |
| `revisions.get` and `revisions.delete` after a write | A revision `revisions.list` just showed answers 404 — and in one run the dry run found it and the delete a second later did not | Both refusals say "or not yet" and suggest trying again |

Two rules come out of it, and both are worth more than the table:

1. **Never assert an outcome the response did not carry.** Every defect
   phase 5 found was this: a sentence built from the REQUEST. `lock_file`
   reported a lock Drive had not applied, `empty_trash` reported
   completion from a method with no response, `list_activity` reported a
   new API from an empty object. A result must say what came back.
2. **Where a listing lags and a `get` exists, an id should reach the
   `get`.** That is what fixed shared drives, and the same shape is
   available for `revisions.list`/`revisions.get` and
   `files.list`/`files.get` if either is ever seen to lag.

What this server does NOT do is sleep and retry inside a tool call. It
would hold an MCP call open, it cannot be bounded (nothing says how long
Drive takes), and asserting a settled state afterwards is rule 1 again
with extra steps. Saying plainly that the answer may be stale is the
honest option and the one a model can act on.

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
  kind. It is derived from the method: GET, PATCH, PUT and DELETE mean
  the same thing applied twice, so they may be repeated, and a POST may
  not unless whoever built it said why — so a write added in a later
  phase and given no thought fails closed rather than open. An earlier
  version of this rule read a flag whose zero value meant "safe to
  repeat", which had the same defect one level down. The one request that is neither
  a plain read nor a write is `files.generateIds`, which allocates: it
  is `kindRead` and repeating it is safe, because an unused id costs
  nothing and becomes a file only when a create carries it. That is
  written at the call site, because `kindRead` now grants a retry.
- **Sharing, in phase 2, does not retry at all** (decided here so that
  phase 2 does not have to rediscover it). `permissions.create` is not
  idempotent, and the failure that matters is not a duplicate grant —
  granting the same person the same role twice is the same grant. It is
  that a share applied after the caller believed the call failed is
  exposure nobody is watching, which is the one direction §9 exists to
  prevent. So a sharing write that fails, transiently or otherwise, is
  reported as `[ambiguous_outcome]` with `list_permissions` named as the
  way to find out, rather than repeated. `request.unsafeToRepeat` is
  where that is expressed. `permissions.create` is not idempotent, so after a network
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
- **Agent evals** `scripts/evals` (built in phase 3, sixteen tasks as of
  phase 4), tasks
  through `claude -p` with only this server's tools: find a file and say
  who can see it; build a folder structure and move a file into it; share
  with someone as commenter without emailing them; share with one person
  and do not make a public link; download a file; upload a markdown file
  as a Google Doc; get a starting point for the changes feed and explain
  the token; restore a trashed file; handle a duplicate name; read the
  tail of a large text file; rename and describe; comment on a file and
  resolve the thread; copy a folder and everything in it.
  Scored on the end state read back through the server and on the trace:
  no invented ids, ambiguity handled by asking or listing, `allow_anyone`
  and `transfer_ownership` never passed unasked. The trace half is the
  point — a task can be answered correctly by a model that guessed an id
  and was lucky, and the end state cannot tell. Everything happens inside
  one scratch folder, which is trashed at the end, and the output goes
  through the live driver's redactor.
- **Benchmarks** (`make bench`): reference parsing, path resolution cold
  and warm, a file card, a search page, a tree walk, tree rendering of a
  10 000-item fake, and a large stream through the fake with its
  allocation reported. The call counts and the memory assertion are
  tests, not benchmarks, so `make check` enforces them.
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

**Phase 1 — content and organisation (v0.1.0). Done 2026-09-05.**
`read_file`,
`download_file`, `create_file`, `upload_file`, `update_content`,
`create_folder`, `update_file`, `move_file`, `copy_file`,
`create_shortcut`, `trash_file`, `restore_file`; local-dir confinement;
`drivetest` grows uploads, downloads, trash and revisions; the live
driver covers every tool; `docs/configuration.md` and the README tool
table match the code (the staleness gate enforces it).

The live driver grew a `-write` mode for this: it makes one scratch
folder, exercises every tool inside it, and trashes it again, so a run
touches nothing else. Seven runs closed the phase; the last four found
the four refutations in §18, including two tools that had never worked
at all. With `-drive NAME_OR_ID` it also runs the write half of spike E: one
file into a shared drive and back out, and the folder-move refusal. That
part says loudly if the move back fails, because it is the only thing
trashing the scratch folder cannot clean up.

**Phase 2 — access, shared drives, history (v0.2.0). Done 2026-09-05.** `list_permissions`,
`share_file`, `unshare_file` with the policy; `list_drives`,
`manage_drive`; `list_revisions`, `manage_revision`, `list_changes`;
gated `delete_file`, `empty_trash`, `delete_drive`, `delete_revision`.
The sharing matrix and the inherited-permission cases are in the fake,
along with a changes feed and the shared-drive writes.

Two things changed how this phase was built, and both are worth keeping
for phase 3. The discovery document was read *before* the client rather
than after, which corrected four assumptions at the cost of one request
(§18). And the tool surface was reviewed from outside, which found three
live defects the tests here did not: a file id that could turn a bounded
delete into an unbounded one, a rate-limit reason that had silently
stopped matching, and a token refresh with no deadline behind a security
claim that said otherwise.

Three live runs closed it. The last one confirmed the five fixes the
first two produced, including the changes feed reporting a write and
handing back a fresh token — which the earlier runs could not
distinguish from a broken feed, because Drive's feed is eventually
consistent and the driver asked once.

What was NOT verified live, and is deferred rather than holding the
release (§17a):

- **Spike F** (ownership transfer). Blocked: it needs a second Google
  account, and there is not one to hand. It cannot be undone from this
  side either, so a colleague's account is not a substitute.
- **A share the organisation's policy blocks**, so the `[blocked]`
  mapping in §7.4 is built from a real response rather than an injected
  one. It needs an administrator to put an external address out of
  bounds on an organisational unit.
- **The destructive four against Drive.** They are gated off by default
  and the live driver does not enable them: `empty_trash` cannot be
  scoped to a scratch folder — it takes the whole account's trash — so
  it runs only against a scratch shared drive, or not at all.

**Phase 3 — collaboration, resources, evals, performance (v0.3.0). Done
2026-09-06.** Comments and access requests; `gdrive://` resources;
`copy_file recursive`; the agent evals; `make bench` and the numbers in
§11; the media-type registry and the request-kind check §17a had
deferred here.

Three things are worth carrying forward from how it went.

**The discovery document earned its round trip again.** Four more
corrections before a line was written, two of them structural: the
comment endpoints require `fields`, and `accessproposals.resolve` has no
response type at all, so an acceptance's result can only be read back.
Neither would have failed a test — the first fails every call, the
second produces a result that quietly reports the state from before.

**Benchmarks are for refuting the numbers you wrote down.** Two of §11's
targets were wrong, and both had been in this document since phase 0
without anybody doubting them. The fix was to state what the code does
and assert it, not to change the code: `get_file` climbs because a card
that says where a file is only sometimes is worse.

**A gate that reads one platform cannot see the others.** The coverage
floor ran only on Linux, so a test skipped on Windows cost coverage
nobody could measure. It runs on all three now, and one of the two
Windows skips turned out to be unnecessary.

Five live runs closed it, and the first found the defect no test could
have: Drive refuses `assigneeEmailAddress` in a comment field selection,
so every `add_comment` failed. The run before the fix reported "all calls
behaved as expected" — phase 2's lesson arriving again, and the reason
the driver now says in `docs/development.md` that its verdict is not the
thing to read. Reading the transcript also found a resolve-only reply
rendering as an empty pair of quotes.

The whole comment thread is verified on a blob and on a Google Doc:
create, reply, resolve, resolve again reporting unchanged rather than
posting a second reply everybody can see, reopen, edit, and the listing
with its replies. So are the three resources, the recursive copy with its
dry run and its three refusals, and the access-request listing.

Three of the thirteen evals have run against a real account, and the
first run of those found a bug in the eval harness rather than in the
server: the scratch folder's id was never substituted, so every prompt
carried a literal `{folder}`. Both tasks passed anyway — one by finding
the file by name, one by refusing for the wrong reason — which is the
end-state-versus-trace problem happening inside the thing built to catch
it.

What was NOT verified live, and is deferred rather than holding the
release (§17a): spike F and a policy-blocked share, both still blocked
on a second account and an administrator; the destructive five, which
stay gated off and out of the live driver; and the ten evals nobody has
run yet.

**Phase 4 — Workspace extras and the rest of the API (v0.4.0). Done
2026-09-06.** Labels
(`list_labels`, `manage_labels`) behind `GDRIVE_LABELS`; approvals
(`list_approvals`, `manage_approval`); Drive Activity (`list_activity`)
behind `GDRIVE_ACTIVITY`; `files.download` for Vids;
`use_content_as_indexable_text`, `viewed` marking, property search, and
`copy_comments` restored. `ocr_language` was already shipped in phase 1.
The three discovery documents diffed against what the client calls, with
every method used on purpose or left out on purpose in
`testdata/api-coverage.tsv` and a gate holding both directions.

Three things are worth carrying forward.

**Reading the documents first paid for itself before a line of feature
code.** Four corrections, two of them in behaviour that had already
shipped: `includeLabels=*` is a 400 and had never worked, and
`LabelField`'s date member is `dateString`, so every date-valued label
field decoded to nothing. Neither could have been caught by a test here
— the flag is off by default and the fake accepted whatever it was sent.
A read-only probe against a real account confirmed the first
independently, which is what makes it a fact rather than one bad
response.

**A refutation has a date on it.** §18 recorded, from phase 1, that
`files.copy` takes no `copyComments`. The discovery document lists it.
Whether Google added it or the phase-1 check read the wrong page cannot
be told from here — what matters is that a parameter list read once
stops being true without telling anybody, which is the argument for
re-reading every phase rather than trusting §18.

**A gate that was stricter than its own written rule.** The flat-schema
test refused every array, where §18 has always said "no nested objects
and no arrays of objects". Nothing had needed a list of scalars until an
approval wanted its reviewers. The rule did not change; the test caught
up with it, and now says which rule it is enforcing.

**Phase 5 — what only a live run could say (v1.0.0).** No new tools. The
destructive five and the approved-approval lock run against Drive for
the first time, in a shared drive the driver creates and destroys again;
`gates parity` holds `make check` and CI to the same list; and six
defects that no test here could have caught are fixed.

The through-line is one sentence, and it is worth more than any of the
individual fixes: **the server asserted outcomes from the request rather
than reading them from the response.** `list_activity` announced a new
Drive API on every ordinary entry. `lock_file` reported a lock that was
not applied, directly above a card showing it was not applied.
`empty_trash` claimed everything was gone for good, from a method that
has no response at all. Each was written by someone who knew what the
call was supposed to do, and each shipped.

The second lesson is about §18 itself. Phase 4 observed the right thing
live and wrote down the wrong SHAPE — "the member is missing" for what
is really `{}` — and phase 5's code was built to that record rather than
to a response, so the fix never ran. A probe that tests whether a member
is falsy cannot tell an absent object from an empty one. The entry in
this log has to be the response, or something derived from it by code,
and not a sentence recalling it.

**v1.0.0 ships without three things**, all blocked on something this
account cannot present rather than on work: ownership transfer and
resource keys need a second Google account, and a genuinely
policy-blocked share needs an address a Workspace administrator has put
out of bounds. `manage_labels`'s writes need an administrator to publish
a label. Each is in §17a with what would close it. Use in anger and an
eval round with a second client are what 1.1 wants.

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
| A test fails if **an id** appears in a log | Full ids never appear; a **six-character prefix** does, at debug level | A retry, its backoff and its outcome are separate log lines, and without a correlation key a failure cannot be traced to the call that caused it. Six characters of a 33-character id cannot be looked up, cannot be pasted into a URL, and identify nothing on their own. Everything else the standard names — names, titles, addresses, queries, content, and whole ids — is absent and stays absent: `TestLogsCarryNoTraceOfWhatWasTouched` runs the whole surface at debug level against unmistakable fixtures and fails on any of them. *(Phase 2: "the whole surface" had quietly stopped being true — the test was written for phase 1's tools and eight more had been added around it, including the only ones that take an email address as an argument. It covers them now, a domain was added to the forbidden fixtures, and `method=DELETE` joined the assertion that the writes actually reached the network. A claim like this decays every time the surface grows, which is the argument for asserting the methods rather than trusting the list of calls.)* *(Phase 3: a comment, a reply and an access request's message joined the forbidden fixtures. They are a new KIND of subject rather than a new call on an old one — somebody else's words about the file — and the test would have passed without them.)* *(No longer a deviation: the standard's wording is being changed to the intent it always had — a log must not identify or reconstruct its subject.)* |
| The staleness gate **deliberately fails** between the release commit and the tag | It passes, by accepting notes under an untagged version heading | Our release flow (§12) puts the release commit on a topic branch and requires CI green *before* the merge and therefore before the tag exists. A gate that fails there fails the release pull request. The gate still refuses an undocumented change: with the notes removed it fails, which is tested |
| Errors use the classes `invalid`, `not_found`, `auth`, `conflict`, `unavailable`, `unsupported` | Fourteen classes, including `ambiguous`, `blocked`, `rate_limited`, `ambiguous_outcome` and `pending` | Drive's failures are not the same set. `ambiguous` is the whole addressing design (§4.1), `blocked` is the organisation's sharing policy refusing something Google permits in general (§7.4), and `ambiguous_outcome` is a write whose result is unknown. Collapsing them into `invalid` would lose the distinction a model needs to decide what to do next. *(Phase 4 added `pending`: work Google has begun and not finished, which is a long-running download whose operation is still running. It is not `server`, which says something went wrong, and not `rate_limited`, which says you asked too often — nothing is wrong and nothing needs backing off from, and the same call in a minute picks up the finished render. A model told `server` would report a failure that did not happen.)* *(Phase 3: `gates classes` now holds the code to the list, in both directions — a class invented at a call site, and a class listed that nothing emits. It found neither here, which is what a working guard usually finds. A sibling server split its own `ambiguous` the same way after finding it carried both meanings at once, so that word is the one to keep identical across servers: a model must not have to learn what it means twice.)* |

## 17a. Deferred cleanups

Raised by the phase-0 review passes and deliberately not done in phase 0.

- **No MCP registry entry.** The bundle ships and nothing lists it, so
  the only way to find this server is to already know the repository
  exists. A sibling has one and has offered its implementation: the entry
  is written from the release's own `checksums.txt`, and the registry
  enforces its MCPB rules in code rather than in the published schema — a
  hash, a `github.com` release-asset URL carrying "mcp", no
  `registryBaseUrl`, and a HEAD on that URL before the entry is accepted.

  That last part decides where it goes: the step runs LAST in the release
  workflow, after the release exists, because an entry pointing at a
  download nobody can fetch is worse than no entry. It also means the
  step cannot be tested locally at all, which is the reason it is not
  done here yet rather than an argument against doing it.

- **The live driver's option coverage has never been measured.** The
  shared standard names a `live-cover` gate — the driver's source against
  the binary's published schema, failing on any tool option no step
  sends. Phase 5 grew this driver a great deal, including a whole
  destructive mode, and nothing here knows what fraction of the surface
  it actually exercises. The one repository that has measured it read 14
  of 28 on the day the gate was written. A number nobody has is not
  evidence of a good one.

- **"Never assert an outcome the response did not carry" has no gate.**
  §11 states it as this repository's rule and phase 5 fixed three
  violations by hand, every one of them found by a live run because
  nothing here could see them. The narrow, checkable version exists: for
  a tool input that asks Drive to MAKE SOMETHING SO — `lock_file`,
  `allow_anyone`, `keep_previous_revision`,
  `use_content_as_indexable_text` — fail if the field is read in the same
  function that builds the outcome note, in the family of `gates classes`
  and `TestEmptyTrashIsNamedInOnePlace`. It would have caught `lock_file`
  before Drive did. Not done here because choosing the field set is a
  design question and the gate is worth getting right rather than
  shipping on release eve. Raised by the phase-5 altitude review.

- **A created shared drive is thrown away and then looked for.**
  `manage_drive` create calls `forgetDrives()`, so the next `findDrive`
  pays for a cold paged `drives.list` that is *known* not to contain the
  drive just made — and then falls back to `drives.get`. The server is
  holding the created `*gdrive.Drive` and discards it. `rememberDrive`
  would fix it, but only when the cache is already warm: seeding a cold
  cache with one drive would make every OTHER drive unfindable by name
  until the TTL expired, which is a worse bug than the round trip. Raised
  by the phase-5 altitude review.

- **The live driver repeats two shapes it already has.** The
  "skipped, the file it needs was never created" banner is written out in
  thirteen places across `write.go` and `destroy.go`, where `needing` and
  `expecting` already own the format; and `deleteTheDrive`'s
  call-look-retry-shout is `recover`'s, with one marker string swapped. A
  `skip(section, why)` and a `twice(call, want, complaint)` would put
  both in one place. Left because it touches paths phase 5 had just
  changed heavily and verified live, which §17a has been wrong to do
  before. Raised by the phase-5 reuse review.

- **Five more oneof types are blind the same way `ActionDetail` was, and
  fail more quietly.** The fix after v0.4.0 taught `ActionDetail` to keep
  the member names it decoded, because a member no field names and an
  empty object are otherwise the same Go value. `ActivityCreate`
  (new/upload/copy), `ActivityComment` (post/assignment/suggestion),
  `ActivityActor`, `ActivityUser` and `ActivityTarget` have the identical
  shape and the identical blindness — and worse symptoms, because they do
  not report it: `actionWords` falls through an unrecognised create
  sub-kind to "created", and `actorWords` falls through an unrecognised
  actor to "somebody". A loud wrong answer was fixed; four silent ones
  are still there, and a silent one is what the phase-4 lesson says costs
  a phase to find.

  Not done here because it wants the general form rather than five copies
  — an unexported `unmarshalMembers(b []byte, v any, into *[]string)` in
  `internal/gdrive`, so each type's decoder is three lines — and because
  deciding what a fallthrough should SAY is a separate question per type:
  "created" for an unknown create sub-kind may well be the right answer,
  where "somebody" for an unknown actor probably is not. Raised by the
  post-v0.4.0 altitude review.

- Spikes A, C and the write half of E have run (§18). **F (ownership
  transfer) is still unrun**: `share_file` with `role: owner` exists as
  of phase 2 and behaves against `drivetest`, including the
  `pendingOwner` case and the forced notification, but no fake can prove
  Drive agrees. It runs with the live driver against a scratch folder,
  and needs a second account to transfer to.
- **Spike F (ownership transfer) is unrun, and blocked rather than
  postponed.** It needs a second Google account to transfer to, and the
  maintainer has none. The transfer cannot be undone from this side —
  the file lands in the other person's Drive and only they can remove
  it — so running it against a colleague to close a checklist is not an
  option either.

  What stands in for it: the path behaves against `drivetest`, including
  the forced notification and the `pendingOwner` case; the parameters
  come from the discovery document rather than from memory (§18); and
  nothing shipped asserts what a transfer does. `share_file`'s note
  states only the consequence the reference makes certain — this account
  becomes a writer — and every mention of a pending transfer is read off
  Drive's own `pendingOwner` field rather than predicted. So the code is
  as honest as an unverified path can be, and the first person to run it
  will be told what actually happened rather than what was expected.

  When an account is available: `livedrive -write -share ADDRESS` makes
  a file for the purpose, records its owner, transfers, reads it back and
  compares. It reports three outcomes, two of which are failures.
- ~~Ten of the thirteen evals have never been run.~~ **Done in phase 4.**
  All thirteen ran against a real account and all thirteen passed, and
  three more were added for phase 4's own surface, because the thirteen
  covered phases 0 to 3 and nothing else — an eval is the only thing that
  tests a tool DESCRIPTION rather than a code path, and the two warnings
  that matter most in this server are both in `manage_approval`'s.

  Writing them produced the finding below about how a model reads "ask
  somebody to review", and one of the three is unreachable on this
  account (§17a, labels). The original entry follows, because the reason
  it stood for two phases is still the reason to run them again: `make
  evals` needs the `claude` command, a signed-in account and several
  minutes, and it costs real tokens, so it is not in `make check` and
  phase 3 ran three tasks rather than thirteen. What that proved is the harness end to end
  — an agent reaching this server's tools and nothing else, a task set
  up, scored on the end state and on the trace, and the scratch folder
  trashed — and it found a defect in the harness on its first run. What
  it did not prove is what the other ten tasks say about the tool
  descriptions, which is the whole point of having them. They are the
  first thing to run when somebody has ten minutes and a live account.
- **A policy-blocked share is still unseen live, and looking for it
  found something else.** `livedrive -blocked ADDRESS` now attempts the
  share, reports Google's own reason beside the class this server gave
  it, and says when they disagree. Run against an address with no Google
  account behind it, it found that `invalidSharingRequest` at 400 is not
  a policy refusal at all (§18) — so the class that case had been given
  for three phases told a model to give up when the fix was `notify:
  true`.

  What is still unverified is the genuine article: a 403
  `invalidSharingRequest`, or a `domainPolicy`, from an organisation's
  own external-sharing restriction. It needs an address that a Workspace
  administrator has actually put out of bounds; the maintainer is one,
  and the check is written and waiting for the address.
- ~~The destructive five are unseen live.~~ **Done after v0.4.0.** All
  five have run against Drive, in a shared drive the driver makes and
  destroys again: `livedrive -destructive`. The reason this waited was
  sound — `empty_trash` cannot be scoped to a folder, so reaching it
  without a drive of its own means emptying a real account's trash — and
  the answer was a container that can be deleted whole, which is the
  same thing the approved state below was waiting for.

  The scoping is structural rather than remembered: `empty_trash` is
  named in exactly one method, which cannot run before the drive exists,
  and `TestEmptyTrashIsNamedInOnePlace` walks the driver's syntax tree
  and fails on a second mention. That test was written after the first
  draft named the tool in four places, one of which would have emptied
  the signed-in account.

  Four runs, and the first three each found something (§18): a freshly
  created drive unreachable by its own id, `lock_file` promising a lock
  Drive does not apply, `empty_trash` claiming an outcome a method with
  no response cannot know, and a `delete_revision` dry run contradicting
  the delete that followed it. Two of those runs stranded a scratch
  shared drive in a real Workspace before the cause was understood,
  which is why cleanup now deletes every id the run made rather than
  assuming each section tidied up after itself.
- ~~A `POST` that only reads would take the wrong limiter.~~ **Done in
  phase 3, and this entry described it wrongly until phase 4.** It named
  a `TestNoWriteIsLabelledAsARead` that walks the syntax tree checking a
  `kind` field. There is no such test and no such field: the fix that
  actually landed was better than the one recorded — `kind` was deleted
  and the limiter and the retry rule are both derived from the HTTP
  method, so there is nothing left for a call site to get wrong. A claim
  about a test that does not exist is worse than no claim, and this one
  survived a phase because nobody had reason to look.

  Phase 4 then met the case the derivation gets wrong, which is the
  other half of the same sentence: `activity:query` is a POST that only
  reads. Derived from the method it spends from the write budget and
  refuses to retry after a dropped connection. A request now carries a
  `reads` marker for that, whose zero value is still the safe one — a
  write added later and given no thought is still treated as a write.
- `internal/gapi/drivetest` implements the semantics of `name contains`
  from the reference. Spike A is what confirms the fake and Drive agree.
- ~~Two places know a file's text form.~~ **Done in phase 3**, with the
  registry below: the format a read takes and the format a download
  defaults to are two fields of one entry.
- ~~One MIME registry.~~ **Done in phase 3.** `internal/mediatype` is one
  table keyed by media type — display name, kind filter, export name, the
  format a read takes, the format a download defaults to — and the short
  format names left `internal/gapi` with it, along with `ExportFormats`,
  which went to `internal/model`: a short name is a word this server
  invented for a person to type, and that package speaks only Drive's own
  wire types. Adding a kind is one edit.

  The tool description is still written out rather than generated, because
  a Go struct tag cannot be composed from a constant, but a test now
  compares the format list in `download_file`'s schema against the
  registry. Writing that test found the schema two formats short: a Doc
  exports to `zip` and an Apps Script project to `json`, and neither was
  offered. That is the drift this entry predicted, found the first time
  something looked.

- **The live driver's transcript is redacted by habit, not by
  construction.** Phase 4 found the difference: the driver echoed each
  call's ARGUMENTS unredacted. That was arguably nobody's problem while
  the only address there was one the operator had typed as `-share`, and
  became one the moment starting an approval put the SIGNED-IN account's
  own address into the arguments of every write run. Fixed, with a test
  that an encoded argument list survives the redactor.

  What is not fixed is the shape of it. Every line happening to go
  through `red.Do` is not the same as every line having to, and the next
  print somebody adds while debugging will look exactly like the two
  beside it that are safe. A sibling repository built a gate for this
  over its own driver's syntax tree, and the idea is right.

  It was attempted here and abandoned deliberately. A gate that allows an
  expression by its spelling needs an allowlist that grows with every
  count and tool name printed — fifteen on the first run — and an
  allowlist that long is a gate nobody trusts. The version with teeth
  forbids `fmt.Print*` in the driver outside one redacting helper, so the
  rule is structural. That is a rewrite of some forty call sites in a
  file phase 4 had just changed heavily, on the eve of a live run, which
  is the wrong moment. It wants a phase of its own and no other changes
  in flight.

  **What has changed since: it is no longer a design, it is a thing that
  works and has caught something.** The shared standard now names
  `transcript` for every repository with a live driver, on the strength
  of a run where it found a section header printing straight to the
  terminal — one refactor away from carrying a document title with it.
  So the argument for deferring is now only the timing, and the estimate
  is no longer a guess. `TestEmptyTrashIsNamedInOnePlace` in this
  repository is the same technique at one call site and took an hour; the
  general form is that shape over `fmt.Print*` with one exemption.

  The standard also names `live-cover` — the driver's source against the
  binary's published schema, failing on any tool option no step sends.
  This repository has never measured that number, and phase 5 grew the
  driver a lot. The one place it was measured elsewhere it read 14 of 28.

- **`manage_labels` is unverified live, and `list_labels` is not.** The
  Drive Labels API answers (`doctor` calls `labels.list` and it
  succeeds), and the listing renders the empty case correctly — but no
  label is published to this account, so there is nothing to apply, and
  every write path is fake-only. It needs a Workspace administrator to
  publish one label with a text field and a selection field, which
  exercises both setter kinds and the choice validation. The driver's
  `-labels` mode does the rest.

- ~~The approvals live check is unfinished in one direction.~~ **Done
  after v0.4.0**, in the same scratch shared drive as the destructive
  five, and it found the two halves behave nothing alike.

  Approving is as described: the file comes back carrying a content
  restriction reading "Locked for File Approval", the card drops rename
  and comment from what you can do, and the next `update_content` is
  refused for violating it. That state is now verified and the fake
  applies it.

  `lock_file` at the START does not lock. Drive returned no content
  restriction at all, the card showed the file fully editable, and
  `update_content` succeeded — on two runs. The server had been saying
  "the file is LOCKED while the approval is open: nobody can change its
  content, including you", written from the ARGUMENT, printed directly
  above a card contradicting it. It reads the lock off the file now, so
  it is right whether or not Drive applies one; the fake no longer
  applies one either, since it was asserting the claim rather than the
  behaviour, which is how the claim survived a phase.

- **A stray editor file is in the public history.** A
  `.claude/settings.local.json.tmp.*` file reached a commit through
  `git add -A` while phase 1 was being merged. Its content is a
  permission allowlist: public documentation domains, shell command
  patterns, and the maintainer's name, which every commit already
  carries. `gates leaks history` passes over it. It is untracked and
  ignored now, but the blob stays, because removing it means rewriting
  a public history that two signed releases point into — the provenance
  attestation for v0.1.0 names the commit by hash. Twenty-five lines of
  permission patterns do not buy that. Recorded here so that the next
  person to find it does not have to work out whether it mattered.
- **A pre-generated id still costs a round trip per single create.**
  Phase 3 took the idea where it is safest: a recursive copy asks for
  the whole tree's ids in one call, because the walk has just counted
  exactly how many it will use, so almost none go unused and the belief
  about unused ids is barely leaned on. A general pool for the single
  creates is not done. It would save a round trip on nine creates in ten,
  and the call is independent of both the parent resolution and the
  duplicate check, so it could also simply run alongside them — but the
  saving is a network round trip, and the benchmarks measure against a
  fake that has none, so it cannot be checked here. It wants a live
  measurement, not another reading.
- **Schema descriptions from one source.** A Go struct tag cannot be
  composed from a constant, so the paragraph describing what a `file`
  argument accepts is written out per tool, and so are the kind and order
  lists. A templated tag that nothing expanded once shipped `${…}` to the
  model. Tests now assert that no schema carries a placeholder and that
  the kind and order lists match `service.Kinds()` and
  `service.OrderBys()`, which closes the hole; composing the descriptions
  after `mcp.AddTool` would close the duplication as well, and is worth
  doing once several more tools take a `file`.
- **A recursive copy writes its tree one item at a time.** The plan is
  breadth first, so every sibling under a folder that already exists is
  independent and only the parent-to-child edge is a real dependency; a
  two-hundred-item copy is therefore two hundred round trips in series,
  which at a Drive write's latency is around a minute. A fan-out per
  level would cut it to roughly the depth. It is not done for the same
  reason as the entry below — the failure list and the "finish and say
  exactly what did not make it" contract become shared mutable state —
  and it belongs with that one, because the fix is the same fix. Raised
  by the phase-3 review.
- **A sharing write builds the file's model twice.** `rereadAfterSharing`
  ends by building one to read the exposure off it, and the result then
  builds another from the same resolved file. On a shared-drive item each
  costs a `permissions.list`, so an accepted access request or a share
  there is one round trip more than it needs. The fix is for the reread
  to hand its model on, which is a signature change through the share,
  unshare and access-request paths — worth doing, and worth doing with a
  live run after it rather than at the end of a phase. Raised by the
  phase-3 review.
- **A pre-generated id's rule is consulted in two places.** `assignIDFor`
  decides for a single create and `idPool` decides for a tree, and both
  ask `gapi.AcceptsGeneratedID`. The duplication is not free but it is
  not removable for nothing either: a pool must count how many ids it
  will use before it asks for them, which is the same predicate by
  necessity. One id source on the service — take one, or prime a
  batch — would make it one call site. Raised by the phase-3 review.
- **`readPlan` states which kinds have a text form, and so does the
  registry.** `internal/mediatype` records a `ReadAs` per kind and
  `service.readPlan` switches on the media type to build the plan around
  it, so the set of readable kinds is written twice. They cannot disagree
  silently any more — a test reads every entry with a `ReadAs` through
  `read_file`, and found the fake refusing an Apps Script export Drive
  allows — but the switch is still where a new readable kind has to be
  added second. Moving the rest of a plan into the registry (the accepted
  format alternatives, the note, the display name of the format) would
  make it one place; it puts user-facing prose into a leaf table, which
  is the trade to weigh. Raised by the phase-3 review.
- **Bounded concurrency for listings**, still open, and the phase-3
  benchmarks did not settle it. A tree walk issues one `files.list` per
  folder in series and a search page up to twenty `files.get` in series,
  with no data dependency between siblings. Quota is unchanged; the cost
  is wall-clock, roughly 2-3 s per call at 100 ms round trip. Against the
  fake a twenty-folder walk is 8 ms, which measures this server's own
  work and says nothing about the thing worth fixing: the fake has no
  latency, so the benchmark cannot show the saving and cannot show a
  regression either. It needs a fake that can be told to be slow, or a
  measurement against Drive. The design constraint stands: the shared
  item budget becomes shared mutable state the moment the walk is
  concurrent.
- **The three sharing writes repeat their prelude.** `share_file`,
  `unshare_file` and `resolve_access_request` each resolve fresh, check
  `capabilities.canShare`, and read the exposure before — by hand, in
  that order, three times. The predicate itself is now one function
  (`model.CanShare`), which was the half worth doing immediately; the
  prelude is not, and "check canShare before any sharing write" is still
  enforced by whoever remembers. A `sharingTarget` helper returning the
  resolved file and the exposure before would make the rule structural
  rather than remembered. Raised by the phase-3 review, and the reason it
  waits is that it changes the flow of two tools that are already
  verified live.
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

**Phase 2 additions (2026-09-05).** The rows below the first block were
added by phase 2. Two things produced most of them: reading the Drive v3
discovery document (`www.googleapis.com/discovery/v1/apis/drive/v3/rest`,
revision 20260901) *before* writing the client rather than after, and a
review from outside this session. The pattern in both is the same and
worth stating once: **anything written from memory rather than from the
reference was wrong about a quarter of the time, and the wrongness was
invisible until a real call failed.**

**Phase 3 additions (2026-09-06).** The discovery document
(revision 20260901) was read before the client again, and the
benchmarks §11 had been promising since phase 0 were finally run. The
first corrected four things; the second refuted two of this document's
own numbers.

| Convention | Verdict | Effect |
|---|---|---|
| The `fields` parameter is optional everywhere, as it is on every other Drive method | **Refuted.** `comments.list`, `comments.get`, `comments.create` and `comments.update` all answer 400 without it: the reference says "Required: The `fields` parameter must be set" on each. `replies.*` does not. It is the only corner of this API with the rule | Every comment call sets it, `drivetest` refuses a call without one, and a test asserts the parameter is on the wire rather than trusting four passing calls. A fake that accepted what Drive refuses would have let this reach production |
| A shared-drive file needs `supportsAllDrives` on every call | **Refuted for comments.** The discovery document gives the comment and reply methods no such parameter at all, so a comment on a shared-drive file is reached without one, and sending it would be an unknown parameter on every call | The comment client sends none, and a test asserts it, because "add it everywhere" is the habit the rest of this client was built with |
| A comment's author can be identified | **Refuted.** "The author's email address and permission ID will not be populated" — for a comment and for a reply. Only the display name and whether it is you | Neither field is requested. Asking would add an always-empty field and an address to redact for nothing |
| A reply is a reply; resolving is a state on the comment | **Refuted.** `Comment.resolved` is output only, and the only way to set it is a reply whose `action` is `resolve` or `reopen` | Resolving lives in `reply_comment` rather than in a state-setting tool, and closing a thread is visible to everybody who can see the file. Resolving one already resolved reports "unchanged" instead of posting a second reply |
| An access proposal asks for a role | **Refuted.** `AccessProposal.rolesAndViews` is a LIST, and `ResolveAccessProposalRequest.role` is a list too, required for `ACCEPT` | A proposal naming exactly one role is accepted as that one; a proposal naming several is `[ambiguous]` with them listed, because picking between candidates is the thing this server never does |
| `accessproposals.resolve` reports what it did | **Refuted, and it shapes the tool.** The method has no response type: Drive answers with an empty body | The exposure after an acceptance is read back with a second call, the way `share_file` reads it. Nothing about the result is predicted from the request |
| Two resource templates sharing a prefix shadow each other | **Refuted for go-sdk v1.7.0 with uritemplate v3.0.2**, and checked rather than reasoned about: `Regexp()` anchors with `^` and `$`, and a simple expansion's character class excludes `/`. `gdrive://{file}` cannot match `gdrive://x/meta` | The three templates are registered as they are. A reserved expansion (`{+file}`) *would* shadow and is not used; the price is that a path or a URL must arrive percent-encoded, which a test records as behaviour rather than a footnote |
| `get_file` costs at most two calls (§11, since phase 0) | **Refuted by measurement.** Two holds for a file at the top of My Drive. Three folders down it is four: the file, and one read per folder above it, because the card's location line is a climb | The target is restated as the code behaves and asserted in a test, including that the second call costs nothing at all. The code did not change: a card that says where a file is only sometimes is worse than one that costs a read per level once |
| A search page's parent lookups are one, because they are cached (§11) | **Refuted by measurement.** A hundred hits in one folder three levels down cost three reads, not one and not a hundred: the cache is per folder, and the chain above the hits has three of them | The target names the chain. Still well inside the budget of twenty that the number was protecting |
| A wall-clock target can be asserted in `make check` | **Refuted.** 10 000 items render in 4.7 ms uninstrumented and over 60 ms under the race detector with coverage counters, which is what `make check` runs | The test asserts linearity — ten times the items within twenty times the work — plus a backstop far above anything instrumentation explains. The millisecond figure lives in the benchmark, where nothing is instrumented |
| A benchmark's fixture shape does not matter, only its size | **Refuted, by getting it wrong.** The first 10 000-item tree made one folder per folder, so it was a thousand levels deep — a shape Drive would never return — and measured the cost of the indent string, at 226 MB allocated per render. A realistic shape is 6.8 MB | The fixture branches three ways, which puts 10 000 items about eight levels down, inside the depth a walk will go to |
| A gate that runs on one platform of a three-platform matrix is enough | **Refuted** (reported by a sibling Go MCP server, checked here). The coverage floor ran under `if: runner.os == 'Linux'`, so a test skipped on Windows cost coverage nobody could measure | It runs on all three. One of the two Windows skips here turned out to be unnecessary — `os.UserConfigDir` reads `%AppData%` there, so clearing that is the same experiment — and now runs everywhere |
| Every `invalidSharingRequest` is the organisation's policy refusing (§7.4's mapping since phase 2) | **Refuted, live, and it was the wrong instruction rather than the wrong word.** Sharing with an address that has no Google account behind it answers **400** `invalidSharingRequest` — "you must check the Notify people box to invite this recipient". Google uses the reason for a policy refusal AND for a request the caller can simply fix, and the status is what tells them apart | `invalidSharingRequest` maps to `blocked` only at 403; at 400 it is `invalid`, which keeps Google's own message and with it the fix. `[blocked]` says give up and names an administrator; `[invalid]` says fix it and try again. The mapping had been built from documented reasons and had never been shown a real one — which is the whole argument for the check that found it |
| A driver that reads the CLASS of a refusal has verified the classification | **Refuted by its own first run.** The `-blocked` check compared the class against `blocked`, saw `blocked`, and printed "the mapping holds" for a refusal that was not a policy refusal at all | The verdict now prints Google's message and says to read it, and names both readings when the class is not `blocked` — the reason may be missing from the mapping, or the address may simply not be policy-refused. A check whose pass condition is one field of the thing it is checking can be fooled by that field |
| The reference listing a field means the field can be requested | **Refuted, live.** `Comment.assigneeEmailAddress` is in the discovery document and is a real field; Drive answers 400 "Invalid field selection assignee_email_address" when a `fields` expression names it, so asking fails the whole call. Every `add_comment` failed on the first live run | The field is out of the request, the model and the renderer, and a note sits where it was in the wire types, because the next person to read the reference will want to add it back. Neither the fake nor the discovery document could have caught this: the first answered the field happily, and the second is the source that says it exists |
| A live run that reports "all calls behaved as expected" has verified the tool surface | **Refuted, and it is the most useful thing phase 2 learned.** Two runs said exactly that while three results were wrong: a file card reporting `sharing: private to you` in the same result whose change line said the file was now public, a removal reporting "shared, but no grants are visible" instead of "private to you", and every My Drive file blaming an inherited grant on a shared drive it had never been near. The driver checks whether a call *succeeded*, not whether it *told the truth*, and those are different questions | The transcript is read, not just its verdict. Phase 3's evals are the mechanised version of this: a result that is wrong while succeeding is the class of defect no status code catches |
| The changes feed answering "0 changes" straight after a write is a bug | Neither confirmed nor refuted for two runs, which was the problem: Drive's feed is eventually consistent, so a feed that works and reports nothing is indistinguishable from a broken one when you ask once. The third run reported the change and a fresh token | The live driver polls the feed and says which happened rather than printing an empty answer. **The general rule: where a system is eventually consistent, a single read cannot be evidence of absence** |
| `permissions.create` and `permissions.update` take the same parameters (assumed; one options struct was written for both) | Refuted by the discovery document: create has `sendNotificationEmail`, `emailMessage` and `moveToNewOwnersRoot`; update has none of those and has `removeExpiration` instead | Two option types in `internal/gapi`, so a parameter cannot be offered on a call that ignores it |
| An empty `expirationTime` clears a permission's expiry (assumed) | Refuted: clearing it is the `removeExpiration` query parameter on update. Drive does not read `""` as "remove this" | `PermissionMeta.ExpirationTime` is a plain string that only sets; `UpdateShareOptions.RemoveExpiration` clears |
| `sendNotificationEmail` may be sent on any grant, so send it explicitly every time (assumed, and it is the safer-looking habit) | Refuted: the reference says it "defaults to `true` for users and groups, and **is not allowed for other requests**", and "must not be disabled for ownership transfers". Sending it for a `domain` or `anyone` grant is a 400 either way | The parameter is a `*bool`: omitted entirely for those principal types, explicit for a user or group, forced true for a transfer with the result saying so |
| `files.emptyTrash` needs `enforceSingleParent` alongside `driveId` (assumed) | Refuted: `enforceSingleParent` is deprecated on both `files.delete` and `files.emptyTrash`, as are `supportsTeamDrives`, `teamDriveId`, `includeTeamDriveItems` and `enforceExpansiveAccess` | None of them is sent; a test asserts `files.emptyTrash` carries no deprecated parameter |
| A shared drive is hidden by patching `hidden` on the resource (assumed; the field is writable) | Not refuted, but not taken: `drives.hide` and `drives.unhide` are their own endpoints. A patch field the service might quietly ignore is a poor way to change what a person sees | `HideDrive`/`UnhideDrive` post to the dedicated endpoints; a test asserts which endpoint was used |
| Drive returns the signed-in person's role on a shared drive, so `list_drives` can show it (§7.5 as written) | Refuted: the `Drive` resource carries capabilities and no role, and `drives.list` has no field for one. Naming a role from capabilities would be a guess dressed as a fact, and getting the real one costs a `permissions.list` per drive | `list_drives` says what the account **may do**, in the same plain words a file card uses. `list_permissions` on the drive gives the actual membership, this account's grant included |
| `url.PathEscape` on a file id makes a path safe (assumed) | **Refuted, and it was a live defect.** Drive puts non-id endpoints under `/files` as sibling segments, so an id of `trash` builds `DELETE /files/trash` — `files.emptyTrash`. A bounded destructive call becomes an unbounded one, and escaping cannot prevent it: every character in the word is legal in a path segment. It was unreachable only because a lookup two layers above happened to 404 first | One `fileSegment` guard that every `/files/{id}` path goes through, refusing the reserved segments, with a test asserting no request is even built. The general rule: **enumerate every value a reference type accepts that denotes more than one resource, or a resource containing others, and refuse them explicitly at each bounded destructive call** |
| Google refusing to delete a drive root through `capabilities.canDelete` is protection enough (implied by the capability checks elsewhere) | Refuted in principle rather than in practice: it holds today, but `root` is an alias for My Drive's root and a shared drive's id is its own root folder's id, so a bounded call turning unbounded rested on a field **the other side computes and sends** | `delete_file` refuses both cases itself, and a test strips the capabilities off the fixture to prove the refusal does not depend on them |
| One Google error reason has one spelling (assumed; the constants were camelCase and compared with `==`) | **Refuted, and it was a live defect.** Google writes a condition as `rateLimitExceeded` in the legacy `error.errors[]` envelope and as `RATE_LIMIT_EXCEEDED` in a `google.rpc.ErrorInfo` detail. This client prefers the detail, so every reason arriving the modern way missed its comparison: a throttled read was classified as a permission error, which is exactly what the class vocabulary exists to prevent | Reasons are compared through `sameReason`, which folds both spellings. The fake had been sending the same string in both places — a fake agreeing with the bug — and now sends each in its own spelling, so the whole suite exercises the modern form |
| A 403 quota reason means "wait and try again" (assumed for all of them) | Refuted for one: `dailyLimitExceeded` is a daily project quota. Backing off cannot free it, so retrying spends attempts and the advice is false | The rate reasons are a map to "can backing off help": three true, `dailyLimitExceeded` false. It is still classified as rate limiting, because that is what happened, but it is not retried and the message says what actually blocks it |
| The per-request deadline in `internal/gapi` covers a token refresh (asserted by `docs/security.md`) | **Refuted, and it was a live defect in a security claim.** A refresh runs inside the oauth2 transport against the context the token source was built with, not the one on the request, and with no client of our own it used `http.DefaultClient`, which has no timeout. A token endpoint that accepted a connection and never answered would hang the first tool call for the life of the process | `auth.TokenSource` and `login`'s code exchange both put a client with a deadline in the context. The test uses a raw listener that accepts and never answers, because `httptest.Server.Close` blocks on exactly the connection such a test must leave open — and it was checked to fail without the fix |
| `~> v2.18.0` pins `goreleaser-action`'s binary (recorded in this log as a fix earlier the same day) | Refuted by the action's own README at the tag this workflow pins: the input takes "a fixed version like `v0.117.0` or a max satisfying semver one like `~> 0.132`. In this case this will return `v0.132.1`". Narrowing a range is not pinning it, and it reads as though it were | `version: "v2.18.0"`, no operator. Worth recording twice: the wrong version of this fix survived a review looking straight at it |
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
| Exports have no size limit | Refuted: 10 MB for `files.export`; `files.download` (long-running) exists for Vids and for revisions of Docs and Sheets | `download_file` chooses the method by kind and revision |
| A download operation is valid for 24 hours (§2 and the row above, phase 0) | **Refuted in phase 4** against the long-running-operations guide: an operation "remains available for a minimum of 12 hours", and the guide says the duration is subject to change and differs between file types. The discovery document says nothing about it either way | §2 corrected to 12 h. Nothing in the code depended on the number — `download_file` polls and gives up after two minutes rather than holding an operation open — but a plan that states a number nobody checked is how a wrong number survives four phases |
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
| A pre-generated id works for every create (§11 and §7.2 as written, and what phase 1 shipped) | **Refuted live, 2026-09-05, twice, with two different messages.** First: `403 Generated IDs are not supported for Docs Editors formats`, on `create_file kind: doc`. Fixed by excluding the Docs Editors set — and the next run refuted *that*: `create_shortcut` answered `400 The provided file ID is not usable`. A folder takes one and so does anything with bytes, both confirmed in the same runs | The rule now names what is **known to work** — a folder, or a type that is not Drive's own — so the Google-native types nobody has tried (Sites, Maps, Vids, Jamboards, Apps Script) fall on the safe side by default. Withholding an id costs idempotency, which the duplicate-name guard catches; sending one where Drive refuses it costs the whole call, as it did twice. `drivetest` refuses both, each with Google's own message and status, and a table test covers all five cases |
| Enumerating the refusals is as safe as enumerating what works (my fix to the first refutation) | Refuted by the second one, eight minutes later. The first list was drawn from Google's "Docs Editors" vocabulary, which is a real category — and shortcuts are not in it, so a correct-looking list still had a hole. A list of what is forbidden fails open; a list of what is permitted fails closed | The same reasoning as the host allowlist earlier in this phase: where a check will be wrong, it should be wrong in the direction that refuses |
| A write may be retried on a 5xx because writes carry pre-generated ids (phase 0's `retryable`, which named the request kind) | Refuted by the finding above, and by a sibling project hitting the same shape from the other end: a rule stated for "writes" and tested through one kind of write silently stops covering a second kind added later. Here the second kind was a create that *cannot* carry an id | `retryable` no longer reads the kind. A test asserts that a create without an id is attempted once and one with an id twice, and it fails against the old rule |
| Naming the exception is enough (my first fix to the row above) | Refined after a second reader pointed at the default: the flag was `unsafeToRepeat`, whose zero value is false, so a POST added later and given no flag would retry — the same failure by omission, one level down. Marking the *safe* writes instead has the mirror defect, since someone still has to remember | Repeatability is derived from the HTTP method, where the answer is already defined: GET, PATCH, PUT and DELETE are repeatable, a POST only when it says why. Nobody has to remember, and the unconsidered case is the safe one |
| Every transient failure is equally ambiguous, so one rule covers them all (the first cut of the rule above) | Refuted in review: a 429 or one of Google's rate-limit reasons is a refusal to *begin* the work, so nothing was applied and repeating it is as safe as repeating a read. Only a 5xx is genuinely ambiguous. Treating them alike made every create that cannot carry an id — every Docs-format create — fail on the first rate limit instead of backing off, which is the one thing Google's own guide asks a client to do | `transientError` records which kind it is; a refusal is always repeatable, and only the ambiguous answers consult the request |
| Opening a resumable upload session is a create, and inherits a create's caution (the same cut) | Refuted in review: `uploadType=resumable` allocates a session URI and nothing else — a file appears only when chunks are committed, so a second attempt abandons the first session and creates nothing. The caution cost an entire large upload on a 503 before a byte was sent | The session-opening POST is marked idempotent, with the reason at the call site |
| Adding a name to a location produces a path (`Location.Child`, written for a move destination) | Refuted in review: three of the forms a location takes are not paths, and `Child` cleared `Orphaned`, so a folder whose parent this account cannot see was listed at the root of My Drive. The tree's header and its own first line — built by different routes — then disagreed with each other | `Child` turns "no visible parent" into `Above`, so the gap is shown where it is (`My Drive/…/Orphan`), and the tree's first line is built the same way as its header |
| Shared-drive creation can be retried freely | Refined: `requestId` is required and makes it idempotent | `manage_drive create` keeps the id for the retry |
| Shared drives are available to every account | Refuted: Workspace editions only; `about.canCreateDrives` | `get_account` and `doctor` report it; tests skip on consumer accounts |
| "Ask somebody to review this file" leads a model to the approval tools (the assumption behind `manage_approval`'s description) | **Refuted by an eval in phase 4.** Given exactly that sentence, the model shared the file as a commenter with a "could you review this?" message, and never looked at the approval tools at all — its tool search selected `share_file` and four others, and no approval tool among them. The answer is defensible and arguably the better one for the need, so the task was wrong to demand one of two correct calls, and now names the mechanism | The eval says "start a formal approval", which still does not name the tool. The finding stands on its own though: a surface where two tools answer one sentence is a surface where the more familiar one wins, and `manage_approval` is discoverable only to somebody already looking for approvals |
| A display name is capitalised, so a capital is what tells it from prose (the redactor, phases 1-3) | **Refuted live in phase 4.** A Workspace account with no display name set shows the address's local part instead — lowercase, dotted — and it survived EVERY position the redactor knows, because the shape refused it before the position was consulted. The transcript carried the maintainer's own name throughout | A dotted lowercase token is a name shape too, safe only because the positions are anchored. The fixtures were the other half of the problem: every invented name in them was capitalised, so the tests agreed with the bug |
| Labels are a Drive API feature | Refined: applied through the Drive API, defined through the separate Drive Labels API with its own scopes | Phase 4 with `GDRIVE_LABELS` |
| Reading the labels on a file needs the labels scopes (§10 as written) | **Refuted in phase 4** against the discovery document: `files.listLabels` and `files.modifyLabels` list only `drive`, `drive.file` and `drive.metadata`. The labels scopes belong to the Labels API, which defines labels; Drive alone reads and writes the values ON a file | `GDRIVE_LABELS` still governs both tools, because applying a label needs a label id and a field id and those live in the definitions. But the reason is now stated as it is, and `manage_labels` still applies and removes when the definitions are out of reach — only `set_field` is refused there, since the setter depends on the field's type |
| `includeLabels` asks Drive for a file's labels (what phase 1 shipped) | **Refuted in phase 4, twice over.** The discovery document says it is "a comma-separated list of IDs of labels to include"; a live probe answers `includeLabels=*` with 400 `badRequest`. `get_file` under `GDRIVE_LABELS=true` had therefore never worked since phase 1, and nothing noticed: the flag is off by default and no test could see it, because the fake accepted anything | `files.listLabels` is the method for "which labels are on this file"; `GetFileOptions.IncludeLabelIDs` takes ids when a caller has them. `drivetest` refuses a wildcard now, with Drive's own status, so the case a test could not see is a case a test now fails on |
| `LabelField`'s date member is `date` (what phase 1's wire type said) | **Refuted in phase 4** against the discovery document: it is `dateString`, an RFC 3339 full-date. The wrong tag decoded every date-valued field to nothing — silently, an absent member being indistinguishable from an unset one | Tag corrected, and a round-trip test asserts a date survives rather than describing the tag |
| Access requests can be created through the API | Refuted: listed and resolved only | §7.8 |
| Approvals are part of Drive v3 (my assumption: no) | Confirmed present in the v3 reference (`approvals.*`), edition and behaviour unverified | Phase 4, verified live first |
| `approvals.list` returns the approvals (the obvious reading) | **Refuted in phase 4** by the discovery document's own words: "By default, this method returns a minimal response that may not include the items array. To retrieve approval details, you must explicitly specify the fields you want." The same shape as the comment endpoints, which cost phase 3 a live run to find | `ApprovalFields` is sent on every call, and `drivetest` refuses a fields-less request. Reading the document first is what turned a live-run defect into a line of code |
| Starting an approval only asks people to look at a file | **Refined in phase 4** from the schemas: `StartApprovalRequest.lockFile` locks the content for the duration, and the default `fileContentChangeBehavior` of `RESET_APPROVAL` locks the file once it is approved. Every verb also mails somebody, with no notify flag anywhere | Said in `manage_approval`'s description and in every result. The live driver deliberately cancels rather than approves: an approved file is locked, and a scratch folder holding something the driver cannot clean up would break its own contract |
| Drive Activity is in Drive v3 | Refuted: a separate API (`driveactivity.googleapis.com`, v2) with its own scopes; its overview page returned 404 during this check | Phase 4, verified before design |
| Drive Activity's 404 in phase 0 meant something was wrong with it | **Refuted in phase 4**: the discovery document is there, the API is GA, and it has exactly one method (`activity.query`). A documentation page that 404s says nothing about an API | `list_activity`, behind `GDRIVE_ACTIVITY` |
| Google's search guide documents how to find a file by a custom property | **Refuted live in phase 4.** The guide gives `properties has { key='department' }` as its own example of matching a key whatever the value, and Drive answers it 400 `invalid` "Invalid Value" — twice in each of three spellings, and for `appProperties` too. Only `key` AND `value` works | `search_files property:` requires both halves and says why; `drivetest` refuses the key-only form as Drive does; the live driver checks the refusal, so a day when Drive starts accepting it shows up as this server being needlessly strict rather than never showing up |
| An empty property search means the query is wrong | Refined live: the file a run had just tagged was still absent after 30 s, and a direct query minutes later found it. The query is right and Drive's property index is eventually consistent, slower than the changes feed | The driver reports UNVERIFIED rather than failing: a slow index is not a defect, and a verdict that cries wolf is one nobody reads. It still must not pass quietly, because "not indexed yet" and "the query is broken" are the same empty page |
| Every activity says what happened | Refuted live in phase 4: some activities carry no action. **The shape was recorded wrongly and corrected after v0.4.0** — see the row below | `list_activity` counts and reports those separately from an action kind it has no words for — which would mean Google had added a thirteenth, and IS worth acting on. Reporting the first as the second sends a reader hunting a case that is not missing |
| An activity with no action arrives with `primaryActionDetail` **missing** (phase 4, from a live probe of 400) | **Refuted live after v0.4.0.** A fresh probe of 400 activities finds that shape **zero** times: Drive sends `primaryActionDetail: {}`, ten times in 400. Phase 4 saw the right entries and wrote down the wrong shape — a probe that asks whether the member is *falsy* cannot tell an absent object from an empty one, and `{}` is falsy in most languages a probe gets written in. The code was then built to the record rather than to the response, so the branch meant to catch the ordinary case never ran, and every one of those entries was reported as a thirteenth kind Google had added: the exact confusion the phase-4 split was written to end | `ActionDetail` keeps the member names it decoded, because an empty object and a member no field covers are otherwise the same Go value. `NewActivity` decides on the names; the fixtures are JSON the decoder reads rather than struct literals, since a literal cannot express the difference. On a real account the false alarms go from ten to none, and a kind Drive really grows is named rather than counted |
| A refutation is worth trusting once it has been checked live | **Refined after v0.4.0**, and this is the third time §18 has bent this way. Phase 4 learned that a parameter list read once has a date on it. This one has a SHAPE on it: the live observation was real and the record of it was not, and a note in this table is what the next phase builds against. What makes the difference is keeping the response — the bytes, or a decoder run over them — rather than a sentence about the response | The fixtures for both branches are now JSON, so the test is written in the same language as the evidence. Where a probe settles a question, the probe's own classification is quoted in the row |
| Approvals exist in the API but the edition is unverified (phase 4, from the discovery document) | **Confirmed live**: start, list, comment and cancel all work on this Workspace edition, and the file-content-change behaviour comes back `RESET_APPROVAL` as the schema says | The approved state and `lock_file` are still unrun, deliberately — §17a |
| A shared drive can be addressed by its id as soon as it exists | **Refuted live after v0.4.0.** `drives.list` is eventually consistent and `drives.get` is not, so a drive created moments ago answers by id and is absent from the listing for minutes. Everything taking a `drive` argument resolved through the listing alone, so the driver made a scratch drive, wrote files into it by that id, and was then told there was no drive of that NAME — with a list of unrelated drive names attached — when it tried to empty its trash and delete it. It stranded a shared drive in a real Workspace twice | `findDrive` falls back to `drives.get` when the listing has no match, which costs nothing on the paths that already worked: a name can only be answered by the listing, and an id that is in the listing is answered before the fallback. The refusal says both readings were tried. `drivetest.UnlistedDrives` is the fake's version of the lag |
| `files.emptyTrash` empties the trash | **Refined live after v0.4.0.** The method has NO response — the reference gives it none — so nothing can be read back about what went. A run trashed a file, emptied that shared drive's trash, and restored the same file on the very next call; the dry run before it counted the trash as holding 0 items while that file was in it | The result says Drive accepted the call, says the method reports nothing at all, and says the view it works from lags. It had been claiming "is empty. Everything that was in it is gone for good", which is the strongest claim in the whole surface and the one thing this call cannot know |
| `lockFile` on an approval locks the file | **Refuted live after v0.4.0**, on two runs: Drive applied no content restriction, the card showed the file fully editable, and `update_content` succeeded. APPROVING is what locks it — that half is confirmed, with the restriction reading "Locked for File Approval" and the next content change refused for violating it | The sentence is read off the file rather than written from the argument, so it is right in both worlds. The fake locked on `lockFile` too, which is how the claim survived a phase: it was built from the reference and agreed with the belief |
| A content restriction stops a file being removed | Refuted from the reference before the run that depended on it: `readOnly` stops a new revision, a comment, and the title — and says nothing about trashing or deletion, and `files.delete` takes no parameter a restriction could refuse. Confirmed live: an approved, locked file was permanently deleted | Checked BEFORE designing the destructive driver, because the answer decided whether an approved file could strand a scratch shared drive. `drives.delete`'s `allowItemDeletion` needs `useDomainAdminAccess`, which this server does not offer, so a drive must be emptied before it can go |
| `revisions.get` agrees with `revisions.list` | **Refuted live after v0.4.0**: a revision `list_revisions` had just shown answered 404 from `revisions.get` seconds later, and the delete that followed — same path, same call — succeeded. Drive's revision endpoint lags a write | The refusal says "or not yet" and tells the caller to try again, instead of explaining that the revision must have expired |
| Drive's eventual consistency is a property of the changes feed | **Refuted, cumulatively.** Six surfaces now: the changes feed (phase 2), the property index (phase 4), and after v0.4.0 `drives.list` after a create, a shared drive's trash count, the trash itself surviving `emptyTrash`, and `revisions.get` after a write. It is a property of Drive, not of one endpoint | The rule this repository now works to: a message must never assert an outcome the response did not carry. Every one of the findings after v0.4.0 — including the `list_activity` false alarm and `lock_file` — is the same mistake, which is that a sentence was built from the REQUEST |
| A refused approval is a permissions problem | **Refined live after v0.4.0**: answering an approval that is already finished is refused with the same bare `Permission denied` as answering one you are not a reviewer of. Drive does not distinguish them, so the message cannot either — it has to offer the whole set. It named two of three and left out the only cause the caller can have produced itself, so the live run, having just cancelled the approval it then answered, was told to check a reviewer list the account was already on | The finished case is named first and the message points at `list_approvals`, which is the one call that says which of the three it is |
| Drive Activity says who did something | **Refuted in phase 4** from the schemas: an actor is a `KnownUser` carrying a People API resource name (`people/123456`) and an `isCurrentUser` flag, and nothing else. No display name, no address. The permissions inside a `PermissionChange` carry no address either | `list_activity` says "you" or "somebody else" and prints a line saying why it cannot say more. Resolving the name would be a third API and a third scope for a decoration, and the id itself never reaches the output |
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
| `files.copy` can bring the comments with it (§7.3 as written) | **Refuted in phase 1** against the v3 reference — and the refutation was itself **refuted in phase 4** against the discovery document, which lists `copyComments` on `files.copy` with a default of `false`. Whether Google added it since or the phase-1 check read the reference page rather than the document cannot be told from here, and the difference does not matter: the lesson is that a parameter list read once is a fact with a date on it | `copy_comments` is back on `copy_file`, off by default, and the result says out loud when a copy carried somebody else's words somewhere new. This is the argument for re-reading the discovery document every phase rather than trusting §18 |
| An old revision of a Docs editors file is fetched with `files.download` (§18, from the revisions guide) | Refined in phase 1: `files.download` is a long-running operation that hands back an `Operation` to poll, while the `Revision` resource itself carries `exportLinks` for exactly this — a direct URL per format, on a Google host the allowlist already permits. The simpler documented route was taken | `download_file revision:` reads the revision, then fetches its export link. `files.download` stays for Vids in phase 4. To be confirmed by the live run |
| Drive's structural refusals arrive with their own status | Refuted by the fake once it answered with Google's real reason: `teamDrivesFolderMoveInNotSupported` comes back as **403**, and the error mapping tested the status before the reason, so "this cannot be done" was reported as "you may not". A model told `[forbidden]` goes looking for permissions to change; there are none | The reason is matched before the generic 403, and the folder-move refusal is `[unsupported]` with the way round it. Phase 0's own tests had never seen the real reason: the fake refused the move without one |
| "Flat schemas" means every argument is a scalar (convention, phase 0) | Refined in phase 1: `update_file` has to tell "leave this alone" from "set it to false", which is a nullable boolean (`type: ["null", "boolean"]`), and Drive's custom properties are a map. Both are still one level deep — a model fills them in without building a structure | The rule is now "no nested objects and no arrays of objects"; the schema test checks scalars, nullable scalars, and maps of scalars, and nothing else |
| A resumable upload's chunk is either stored whole or not at all (implied by the protocol) | Refuted by the protocol itself: the `308` carries a `Range` naming what the session actually holds, which may be less than the chunk just sent. A client that assumes the whole chunk landed skips those bytes and uploads a corrupt file | The upload keeps the current chunk in memory and resends from wherever the session says it got to; `drivetest` has a `ChunkLimit` that makes it store part of a chunk, so the path is tested rather than assumed |
| A download can be bounded by the per-attempt timeout, like every other call | Refuted: `GDRIVE_HTTP_TIMEOUT` is 60 s by default and a large file cannot arrive inside it, so the deadline that protects a metadata call would abort every real download | The deadline covers the response headers; the body is guarded by a stall timer that runs only while a read is outstanding, so a connection that stops sending is cut and one that is merely slow is not |
| A file too large to hold in memory is too large to read (the "up to 20 MB" in §7.1 as written) | Refuted in the phase-1 review: `read_file` fetches a byte range, so the file's size never reaches the process. The guard refused a 200 MB log and advised retrying with `max_chars` and `offset`, which the guard ignored — a refusal whose own advice leads back to itself. The tool description, written from the design's other half, had promised the opposite | The limit is gone; a range request is bounded by the window, not by the file |
| A create cannot invalidate a cached path (my reasoning when narrowing the cache flush) | Refuted in the phase-1 review, and it was the more dangerous half: a create *makes* a path ambiguous. A second `notes.txt` beside the first leaves the cached `(folder, name)` entry answering with the older file's id, which is exactly what §4.1 exists to prevent, and for the full 60 s of the path cache | A write evicts the entry for its own `(parent, name)` whatever it did; only a rename, move or trash also evicts by value. The cache key is built in one function so an eviction cannot spell it differently from a lookup |
| A resumable upload makes progress or fails (implied by the protocol) | Refuted in the phase-1 review: a `308` whose `Range` header is absent reads as "nothing stored", which is also what a proxy that strips the header produces. Neither the backwards guard nor the too-far guard fires, so the same chunk is sent for as long as the deadline allows | A no-progress counter, tested against a fake session that answers 308 and stores nothing |
| A file moved into a shared drive keeps its own sharing (never stated, and what the summary implied) | **Confirmed refuted live, spike E's write half, 2026-09-05**: a file with no grants of its own arrived in a shared drive reporting four editors, all inherited, and lost its `owner` line — the drive owns it now, not a person. Moving it back restored both. This is phase 0's shared-drive row proven rather than reasoned | The card was right about the exposure and clumsy about saying it: "shared with 4 people: 4 can edit … 4 inherited from the shared drive" invites the arithmetic 4 + 4, and named the drive twice. Where the grants come from is now part of the same clause |
| A My Drive folder cannot move into a shared drive (§18, from the reference) | **Confirmed live in the same run**, and with it the `[unsupported]` mapping made earlier in this phase: Drive answers 403 `teamDrivesFolderMoveInNotSupported`, and the tool says what to do instead | Spike E's write half is done. Its read half was already covered by `list_drives` in phase 0's live run |
| A Google-native document's `size` is worth showing | Refuted live: a new, empty Google Doc reports `size: 1 B`, and so does a long one — the field is the metadata Drive keeps, not the size of anything a person can get. The card said "size: 1 B" beside "export formats: docx, epub, …", inviting a reading the number cannot support | `model` fills in a size only for a file with bytes of its own. The export formats line already says what can actually be had |
| The coverage floor covers the packages that matter (phase 0's hand-written list) | Refuted when the list was replaced by `go list ./internal/...`: `internal/userconfig` — the profile file that records the account, the token location and the scopes — had never been under the floor at all, at 70%. A list maintained by hand omits a package silently, and the omission looks exactly like a package that does not exist | The list is derived, with three packages exempt by name and reason (wire types, a version string, the test fake). A package added in a later phase is under the floor from the day it exists. The gap it exposed was closed with tests for the paths a person has to trust: where a profile's files live, that a save replaces the file whole at `0600`, and that every path reports a machine with no config directory rather than returning an empty one |
| Pinning `goreleaser-action` by SHA pins the release (phase 0's release workflow) | Refuted, and by this repository's own earlier finding: the action was pinned by commit and then handed `version: "~> v2"`, so the binary that decides what the artifacts are floated across a whole major version. The `cosign`/`syft` sweep after the v0.0.1 signing failure pinned the tools those actions install and did not come back for this one | `version: "~> v2.18.0"`, beside the SHA, with a comment saying which half of the pin matters. A step that installs a tool has two versions, and this is the third time that has cost something. **Superseded 2026-09-05 (phase 2): the narrowing was not a pin either** |
| `~> v2.18.0` pins goreleaser to 2.18.0 (the fix above) | Refuted. The action's README at the tag this workflow pins says the input takes "a fixed version like `v0.117.0` or a max satisfying semver one like `~> 0.132`. In this case this will return `v0.132.1`". `~>` is a constraint whatever follows it, so `~> v2.18.0` floats across every 2.18.x. The first fix narrowed the range and read as though it had closed it | `version: "v2.18.0"`, no operator — and, because this is the third row about the same mistake, a gate: `gates pins` fails on any workflow tool version that is not exactly one version. A comment could not hold it shut, since the comment beside the wrong value said which half of the pin mattered and the value was still wrong |
| `gates leaks history` works, because it runs and reports (assumed from phase 0, when it was written and never run against a repository with annotated tags) | **Refuted the first time it was run in anger, 2026-09-05**, and in both directions at once. It scanned tag objects including the `tagger` identity git writes itself, so it failed on this repository's own two tags; and it skipped commit objects entirely, so "an id in a commit message" — the case its own comment names — had never been checked. The first defect guaranteed the second would never be noticed: a gate that always fails is a gate whose output nobody reads | A blob is scanned whole, a commit or tag from its message down. An identity is public in every repository by construction and unremovable without rewriting every commit; a message is what a person typed. Three tests, including the same address in a header and in a message with opposite verdicts |
| A host allowlist may strip the port before matching (phase 0's `googleHost`) | Refined in phase 1, prompted by a sibling project's opposite reading: stripping means `https://www.googleapis.com:8443` is allowed. Google's endpoints carry no port, and phase 1 added a path that fetches a URL taken out of a **response body** (a revision's export link), which makes this check load-bearing rather than a formality | A host with a port is refused. Where a check that decides whether an access token leaves the machine is going to be wrong, it should be wrong in the direction of refusing |
| Pinning an action to a full commit SHA pins what that step does | Refuted live: the first `v0.0.1` release failed because `cosign-installer` was pinned by SHA while the cosign it installs was not, and cosign 3 had moved from `--output-signature`/`--output-certificate` to a single `--bundle` — which its v3.0.1 release notes describe as moving from optional to **required** (re-checked against those notes 2026-09-06), so the old config gave it no output path at all rather than a degraded one. The action was immutable; its effect was not | `cosign-release` and `syft-version` are named alongside the action SHAs. A step that installs a tool has two versions, and pinning one of them is the more dangerous half of the job, because it looks done |
| A release workflow that has never run can be trusted because its parts are pinned | Refuted by the same failure. It had been validated with `goreleaser check` and a full local `goreleaser build`, and still failed at the signing step, which only runs with an OIDC token in CI | The first tag of a project is a test of the release path as much as of the code; `docs/development.md` says to verify the published artifacts from outside rather than trust the workflow's own green tick |
| Direct pushes to `main` by the maintainer are fine for a one-person project | Rejected: `main` is released code, and a rule with an exception for the person who releases is not a rule; Scorecard's Branch-Protection asks for pull requests gated by a passing check, and its two-reviewer tier cannot apply to a single maintainer | Pull requests required with CI green on three platforms as the gate; the review count does not apply; tags pushed directly, one at a time |
