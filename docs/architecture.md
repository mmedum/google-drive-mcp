# Architecture — google-drive-mcp

**Status:** phase 10 complete (2026-10-23), released as v3.1.2
(2026-10-24). v3.1.2 is the family sweep of 2026-10-24 (§16): every
reader trims through `config.Lookup`, the refresh token and config
directory included, and the bundle gate holds the identity, refuses a
repeated key, needs a semver release version and reads names whole-word;
reviewed by `/simplify`, `/code-review medium` and `/security-review`, and
a live run with `-write` of 253 calls passed, its transcript read.
v3.1.1 (2026-10-24) is 3.1.0's bundle work reviewed after its release
by `/simplify`, three `/code-review high` rounds and a security review
(§16): the bundle gate derives what it asks for from the server's
settings and refuses `args` or an override that would turn a switch on,
the OAuth client file is optional, the profile field states the name
rule, and a profile name is lowered A to Z alone. Two live runs with
`-write`, their transcripts read, made 254 and 253 calls, all as
expected. v3.1.0 (2026-10-24) gave the Claude Desktop bundle the shape
the sibling servers' bundles share, and skipped the live run this
repository's definition of done asks for; 3.1.1's runs cover it.
v3.0.0 (2026-10-23), built with Go 1.27.3: the server asks the person nothing,
and the client approves each call. A link for anyone, a domain, someone
outside the organization, a new owner and lifting a shared drive's
domain restriction are each their own tool behind
`GDRIVE_ENABLE_DESTRUCTIVE`, approving is its own tool, and a move or a
copy never reaches past the organization by omission (§4a). The Go
module path is now `/v3`. A live run with `-write -destructive` passed,
its transcript read: 366 calls, every one as expected, and no question
put to the person (§16). Verification from outside is owed after the
tag. Phase 9, released as v2.2.0 (2026-10-15), keeps the comments Google
Docs writes into a Markdown file's text, and is verified from outside:
all 13 checksums, the cosign signature against the exact tag identity,
the provenance attestation, which a tampered copy fails, the version in
the binary, the registry publish, and `go install
…/cmd/google-drive-mcp@latest` resolving v2.2.0.

**What 3.0.0 does not close.**

- **The `-share` half of the live run.** A real grant to a second
  account, its expiry, role change and removal, the `shared_with`
  search and `-blocked` were not driven: `share_file` ran only as
  refusals, and `share_outside_organization` as a dry run. The real
  ownership transfer also needs `-transfer`, to an account the
  maintainer controls; this run drove its refusals and a dry run.
- **The group probe** (§17): whether anything this server can read
  shows that a group admits outside members. Until it runs, a grant to
  such a group at the organization's domain is an ordinary `share_file`,
  and the result says who a group reaches.
- **The `members_only` probe** (§17): until it runs, turning
  `members_only` off is an ordinary `manage_drive` switch.
- **What the reviews left** (§17a), the security review's one low
  finding among them: a personal account on an address at another mail
  provider counts that provider's addresses as inside.

A default build registers **33** tools; twelve more exist
behind a flag — the nine `GDRIVE_ENABLE_DESTRUCTIVE` registers, plus
`list_labels` and `manage_labels` under `GDRIVE_LABELS` and
`list_activity` under `GDRIVE_ACTIVITY`. Those last three each need a
Google API enabled in the Cloud project AND a scope login would
otherwise not ask for, which is why they are off by default — 45
tools in all, and 13 in read-only mode. Neither phase 5 nor
phase 6 added a tool. Every method of all three APIs is recorded as used
on purpose or left out on purpose, and a gate holds that record to the
code AND to a snapshot of the APIs themselves — offline, on every
commit.

**What phase 6 is, and it is unreleased.** No new tools. The bundle, the
README a released project should have, and the three gates §17a had
described for three phases rather than built: `transcript`, `live-cover`
and the outcome gate. The lesson §17a names about itself is the one
phase 6 proves — **a guard that would be expensive to make automatic gets
described as though it were, because the description is free.** Building
the three cost a day and found two defects no test and no live run had:
`copy_file` claiming comments it could not know had arrived, and the
parity gate being defeatable by one `#`.

And a proposed rule is not a rule. §17a's version of the outcome gate
would have failed two sites that are already correct — it named the
function as the unit where the branch is the unit — and the entry read as
though only the typing remained. The live driver's option coverage is
122 of 188, measured for the first time; a reader that silently read 2 of
188 and printed it as a figure is what stands between a measurement and a
number.

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
property index were known (the property index was withdrawn on
2026-10-09: the search that measured it looked in the wrong folder,
§18). Phase 5 added four: `drives.list` after a
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
| **Quota is in units**: a read costs 5, a list 100, a download 200, an edit 50; 325 000 units per minute per user, 1 000 000 per minute per project. Over the limit Google answers 403 `userRateLimitExceeded` / `rateLimitExceeded` or 429; the guide says truncated exponential backoff. Sharing has its own `sharingRateLimitExceeded`. | A list is twenty reads. Listings are paged and budgeted; path resolution is cached; sharing has its own limiter; retries honor the reasons above. |
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
converting it to a Doc (which OCRs it, and creates a file: `extract_text`
creates it and deletes it again, §7.1); create an
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
    guidance and the observed behavior of Claude Code (§18): flat strict
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
   see the file before and after. `share_file` reaches named people and
   groups inside the organization; a link for anyone, a domain, someone
   outside it and a new owner are each their own tool, registered only
   with `GDRIVE_ENABLE_DESTRUCTIVE=true`, and a move or a copy never
   reaches past the organization by omission (§4a). Notification mail is
   off unless asked for. A deployer can turn the sharing tools off
   (§7.4).
4. **Nothing is destroyed without a way back, by default.** Trash and
   restore are the default surface. Permanent delete, empty trash, delete
   revision, delete shared drive and delete comment exist only with
   `GDRIVE_ENABLE_DESTRUCTIVE=true`, and each needs `confirm: true` on
   the call. Replacing a file's content can pin the previous revision
   first.
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
   both are present (§18). `extract_text` writes and returns text only,
   as `read_file` does: its result is the text of a file, and a JSON copy
   would double a 400 000-character window (§7.1).
10. **The model sees what the Drive UI shows.** Kind in plain words
    ("Google Sheet", "PDF", "folder", "shortcut to a Google Doc"), owner,
    a sharing summary, link state, what the signed-in person can do
    (edit, share, trash, delete, download), starred, trashed, size,
    modified by whom and when.

### 4a. The client approves each call; the server gates what is wide

Decided for the family 2026-10-22, and for this server 2026-10-23,
when the maintainer approved each change below. It replaces the
2.0.0 design, in which the server asked the person itself, through MCP
form elicitation, before eleven writes.

**The server asks the person nothing.** Per-call approval is the
client's. The specification puts consent to a tool call on the host,
and lets a server ask through elicitation without making the answer
consent. On today's clients the question was unreliable: an
`Elicitation` hook answers it with no dialog, Codex under full access
accepts an empty form by itself, and some clients declare it and never
draw it. No client's permission rules can govern it, and a question on
every call is answered without reading (§18, 2026-10-23).
`GDRIVE_REQUIRE_PROMPT` is gone; set to anything but `false`, the
server refuses to start, since ignoring it would quietly allow what its
owner set it to stop. That makes this 3.0.0.

What holds on every client stays, and is the safety this server counts
on: registration flags, `confirm: true` on the gated tools, the
witnesses and guards in the service, and `dry_run`. Every widely used
client has a mode that runs a registered tool with nobody approving the
call, so a tool that must not run unattended is not registered.

**Every risky variant is its own tool.** A client remembers an
approval per tool, never per argument value, so a wide form carried as
an argument rides along unseen once its tool is always allowed.

1. **Sharing.** `share_file` grants named people and groups inside the
   organization, and refuses the rest `[blocked]`, naming the tool
   that does it. Three tools take the rest, each registered only with
   `GDRIVE_ENABLE_DESTRUCTIVE=true` (and `GDRIVE_SHARING=all`), each
   needing `confirm: true`, each `destructiveHint: true`:
   `share_by_link` for anyone with the link or a `domain:` (and
   `discoverable`), `share_outside_organization` for a person or group
   outside, and `transfer_ownership`. `allow_anyone`, `allow_domain`,
   `transfer_ownership` and `discoverable` are gone from `share_file`.
   A wide grant is narrowed with the wide tool, or removed with
   `unshare_file`, which stays ordinary.

   Each wide tool also takes `file_name`, and `lift_drive_restriction`
   takes `drive_name`: the item's name, compared exactly with the one
   Drive has, refused `[stale]` when it differs, dry run included. The
   question these tools used to ask named the item. A client's prompt
   shows the call's arguments, and a name is what a person recognizes
   there, where an id says nothing. A shared drive's root also answers
   to the drive's own name.

   A group is shared with as a group, by its own address: who is in it
   is nowhere in the call, and this server holds no scope that reads
   it. Google decides each member's access when they open the item, so
   a grant to a group reaches everyone in the group, including people
   added later; a member outside the organization too, if the
   organization's sharing policy lets them in. `share_file`'s
   description and a group grant's result say so. Whether anything this
   server can read shows that a group admits outside members is an open
   question (§17).
2. **Outside** means an address whose domain is not the signed-in
   account's. A secondary domain of the same organization counts as
   outside, and so does every address when the account's own cannot be
   read; `share_file` says which. A personal Google account has no
   organization, so every address is outside it and `share_file`
   grants nobody there; `get_account` and the refusal say so. This is
   the conservative choice: "share with an outsider" is the
   exfiltration path, and the person can still turn the outside tool
   on.
3. **Nothing widens by omission.** A move, a copy, and a folder's
   limited access turned off reach whoever the destination reaches, and
   that is nowhere in the call. Each is refused `[blocked]`, dry run
   included, when what it would add includes anyone with the link, a
   domain, or a person or group outside the organization, naming each
   by address, domain or link, and when who it would reach cannot be
   read. A domain counts whichever it is, the account's own included:
   it is not a person or a group named one by one. A copy reaches only
   who its destination reaches, so a copy of an original whose sharing
   cannot be read is judged by everyone the copy would reach. When the
   account's own address cannot be read, a person cannot be told inside
   from outside, and the refusal says that rather than calling them
   outside. A gain only to people and groups inside the organization is
   an ordinary write, with before and after in the result. There is no
   "wide move" tool: sharing the item with them first, through the wide
   tool, leaves the move nothing to add. `GDRIVE_SHARING=off` still
   refuses any widening at all, `[forbidden]`, as before.
4. **Access requests.** Accepting a request from someone inside the
   organization is an ordinary grant. From outside, it is refused
   `[blocked]`, dry run included, naming `share_outside_organization`
   or Drive itself: the request and its message come from the person
   asking, which under injection is the attacker. Denying is ordinary.
5. **A shared drive's domain restriction.** Turning
   `domain_users_only` off lets what is in the drive be shared outside
   the organization, so `manage_drive` refuses it `[blocked]`, naming
   `lift_drive_restriction`, which does it behind the destructive flag
   with `confirm: true`. The other switches stay `manage_drive`'s.
   `members_only` off joins `lift_drive_restriction` only if a live
   probe shows it restores access already granted to non-members (§17);
   until then it is an ordinary write.
6. **Approving** is `approve_file`, `destructiveHint: true`: it is the
   person's sign-off, Drive has no call to withdraw it, and once every
   reviewer has approved, an approval started with `reset_approval`
   locks the file for good. `manage_approval` refuses `approve`, naming
   it, and keeps the other actions.

`create_file` and `upload_file` into a folder shared with anyone stay
ordinary, decided 2026-10-09 and kept: they put new content, written by
the caller, where the caller named. Nothing that fewer people could
reach before becomes reachable by more, which is what a move or a copy
of existing content does.

**The mark** (`_meta["anthropic/requiresUserInteraction"]`) is on four
tools, for every client: `delete_file`, which skips the trash Drive
offers; `empty_trash` and `delete_drive`, each an unrecoverable delete
of a whole container; and `share_by_link`, a public or domain-wide
share. Claude Code prompts on every call of a marked tool, whatever the
operator's rules, and refuses it where no prompt can be drawn. No other
client reads it, so the flag that registers the tool remains the
control. Nowhere else: `delete_revision` and `delete_comment` lost it,
since one of many in a row would mean a prompt per item, and people stop
reading prompts they see that often. `TestTheMarkSitsOnlyWhereItsPromptIsThePoint`
holds the list.

**Annotations are exact**, because they decide the client's prompt.
`readOnlyHint: true` only on a tool that changes nothing (VS Code
prompts on every tool without it); `destructiveHint` accurate (Codex
prompts by it, and claude.ai documents that it decides its prompt).
`reply_comment`, whose edit replaces a comment's text with no history,
and `manage_labels`, whose set replaces a field's value, are now
destructive.

**The model never widens its own reach.** No tool writes the server's
settings: they come from the `GDRIVE_*` environment, read once at start,
and the profile `login` writes. `TestNoToolWritesTheServersOwnSettings`
drives every registered tool and holds both unchanged.

**Held in tests.** The server test client declares form elicitation and
fails any call that asks; `TestNoToolAsksThePerson` drives every
registered tool on a protocol that asks with `elicitation/create` and on
one that asks with an input request. The live driver declares
elicitation too and counts any question as a failure. The README gives
the Claude Code `permissions.ask` rules for the writes that share,
publish, sign off or delete, and the staleness gate holds each name
there to the registered tools.

## 5. Module layout

```
cmd/google-drive-mcp/     main: login / logout / status / doctor subcommands, server default,
                          --version, --dump-schemas; `status` collects its state once and
                          renders it as text or, with --json, as one object for a script
internal/config/          GDRIVE_* env with flags bound to the same names; typed enums; validated at start
internal/credentials/     refresh token: OS keyring → 0600 file under os.UserConfigDir() with a logged
                          warning → GDRIVE_REFRESH_TOKEN env override
internal/userconfig/      non-secret profile file: client_secret path, account email, token location, scopes
internal/auth/            loopback OAuth (127.0.0.1:<random>, PKCE), scope sets (full / read-only / labels)
internal/gdrive/          Drive API wire types (File, Permission, Revision, Comment, Reply, Drive, Change,
                          About, AccessProposal, Label, Operation), no dependencies
internal/mediatype/       what a media type means, in one table: display name, kind filter, export
                          name, the format a read takes, the format a download defaults to, and the
                          extensions that give a file the type on every machine
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
internal/mdcomment/       the comments Google Docs keeps in a Markdown file's own text: their markers
                          and the gdoc_comments block, read from a stream a line at a time and
                          bounded; what a replacement would lose of them; no imports from this
                          repository
internal/office/          the text of Word, Excel and PowerPoint files and their OpenDocument
                          counterparts, read with archive/zip and encoding/xml through an
                          io.ReaderAt over byte ranges, every read bounded; no network, no imports
                          from this repository; officetest/ builds small such files
internal/render/          text renderers: file card, listing, tree, permissions, revisions, changes,
                          comments; budgets and continuation
internal/service/         orchestration: resolve refs and paths (short cache), search, listing and
                          walking, content in and out, organizing, the sharing policy, trash, drives,
                          history, comments, access requests, labels
internal/server/          SDK wiring; the gdrive:// resource templates; schema dump through an
                          in-memory client session
internal/tools/           one file per area: files.go, content.go, organize.go, access.go, drives.go,
                          history.go, comments.go, resources.go, tools.go
internal/version/
testdata/                 synthetic fixtures (§14) and golden outputs
docs/
scripts/gates/            the repository's own checks, as Go: coverage floor, schema diff,
                          stdio smoke, staleness, leaks, pins, error classes, pre-commit;
                          never shipped
scripts/livedrive/        drives the built binary against a real account, redacting ids,
                          links, addresses, people and the names of what is not its own
                          before anything is printed
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

Dependencies, all pinned: `modelcontextprotocol/go-sdk` v1.8.0 (with
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

Toolchain, newest as of 2026-09-05: Go 1.27.3 (since 2026-10-23, for
GO-2026-6809 in `crypto/x509`, which govulncheck found reachable under
1.27.2; `go 1.27.3` in go.mod, see §17), go-sdk v1.8.0 (since 2026-09, through Dependabot), golangci-lint v2.14.0 (since 2026-10-09: v2.13.2 cannot read the export data Go 1.27.2 writes), govulncheck v1.7.0, go-licenses
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
say so in the header. Organizing, sharing and trash tools act on the
shortcut itself, because that is the thing sitting in the folder the
person is looking at, and the result says "this is a shortcut to X".

**Resource keys.** A key seen in a URL, or in any response
(`resourceKey`, `shortcutDetails.targetResourceKey`), is remembered for
the process and sent as `X-Goog-Drive-Resource-Keys` on every later call
for that id.

Errors carry the fix, in a `[class] message` form: `auth`, `forbidden`, `not_found`, `ambiguous`, `exists`, `invalid`,
`unsupported`, `blocked`, `rate_limited`, `server`, `network`,
`ambiguous_outcome`, `pending`, `stale`, `unexpected`. The message quotes Google's own
error when Google sends its error envelope. A failure without one, such
as an HTML page from Google's front end, is never quoted: the message
gives the HTTP status and what it means. A 413 or 414 is `invalid`, a
request too long to take. A search refused that way says so, with the
query's length; a 400 page for a query longer than any Drive has been
seen to take (§18) is called most likely too long.

## 7. Reading, content, organizing, sharing

### 7.1 Read path

- `get_file` returns a **file card**: name, kind, id, link, location
  path, size, created and modified (by whom), owner, for a file shared
  with this account when and by whom (`sharedWithMeTime`,
  `sharingUser`), sharing summary
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
  a query, and the description says so), `under_folder` (below),
  `drive` (one shared drive),
  `scope` (`all`, `my_drive`, `shared_with_me`), `owner` (`me` or an
  email), `visibility` (`anyone`, `link`, `domain`, `limited`, below),
  `shared_with` (an address: `('x' in readers or 'x' in writers)`),
  `starred`, `trashed`, `modified_after`, `modified_before`,
  `created_after`, `order_by` (`modified`, `name`, `created`, `recency`,
  `viewed`, `size`, `shared`), `limit` (default 25, max 200), `page_token`, and
  `raw_query` for the rest of the syntax, ANDed in. `scope: my_drive`
  with `drive`, or with an `under_folder` in a shared drive, is refused:
  those search the shared drive, which is not My Drive. A next page's
  token wraps Drive's with a digest of the query, the order, the drive
  and the corpus, so a continuation with any other filter, order or
  scope is refused rather than sent beside a different query, and so is
  a token this server did not give out. The folders a query names, by
  `in_folder` or `under_folder`, carry their resource keys. Each hit shows kind,
  name, id, location, modified time and who, and for a file shared with
  this account when and by whom. `shared` orders by `sharedWithMeTime
  desc`; only a file shared with this account has that time, so with no
  `scope` it implies `shared_with_me` and the title says so, and with
  another scope it is refused. A search with no filter at all is allowed:
  every live file this account can see, titled "everything you can see,
  by <order>". `visibility` maps onto Drive's five values: `anyone` is
  `anyoneCanFind` or `anyoneWithLink`, since a person asking what is
  public means both; `link` is `anyoneWithLink` alone, what the sharing
  dialog calls "Anyone with the link"; `domain` is `domainCanFind` or
  `domainWithLink`; `limited` is `limited`. There is no "shared outside
  the organization" filter: Drive's query language has no term for it. Locations need the parents'
  names, which the listing does not carry: up to 20 distinct parents per
  page are looked up (cached), the rest show ids. `incomplete_search`
  from Google is passed on as a line the model can act on.

  `under_folder` searches a folder and every folder below it. Drive
  cannot recurse in a query, so the server walks the tree first, one
  level at a time: one listing per level asks for
  `mimeType = 'application/vnd.google-apps.folder' and ('a' in parents
  or 'b' in parents …)`, with the shared drive's `driveId` when the
  folder is in one. Only folders are asked for, so a shortcut is never
  followed. Then the search adds one `('a' in parents or …)` group over
  every folder found, so Drive's own order and page token still work.
  It covers at most 100 folders, the named one included, and refuses a
  larger tree before searching, as `copy_file` refuses a tree over its
  budget: about 56 bytes a folder once the URL encodes it keeps a
  search under an 8 KB request line, and Google documents no limit
  (§18). A folder the account can see but not list is named in the
  result, since folders below it are not found. It is not taken with
  `in_folder`. A level that comes back short with a token, which the
  reference allows, is paged to its end. Each level asks for the
  folders' resource keys, which the next level's listing and the search
  send. A next page's token also carries the folder and a digest of the
  folder set: a continuation under another folder, or without
  `under_folder`, is refused, and one whose set changed since the first
  page (walked again when the set kept for 5 minutes is gone) is
  refused too, rather than continuing a different query.
- `list_folder` lists one page of a folder's children, folders first,
  natural name order (`orderBy=folder,name_natural`), with `kind` and
  `page_token`. `recursive: true` walks breadth-first under `max_depth`
  (default 3, max 10) and `max_items` (default 200, max 2 000) and
  renders a tree with per-folder counts; when a budget stops it, the
  output names the folders it did not enter. The description states the
  cost. A folder whose `capabilities.canListChildren` is false is still
  listed, and the page or tree says this account cannot list it, so an
  empty answer is not read as an empty folder. A walk asks for that one
  capability on every item it lists.
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
  tool can in fact read (§18). PDFs and images are `[unsupported]` with
  three ways forward: `extract_text`, which reads the text with Google's
  OCR through a copy it deletes again; `download_file`; or `copy_file`
  with `convert_to: doc`, which keeps that copy as a Google Doc for
  `read_file`. A read-only server, which registers neither copying tool,
  names `download_file` alone. The header comment carries name, kind, size, head revision,
  the range shown and `continue_from`.

  An Office file — `.docx`, `.xlsx`, `.pptx`, `.odt`, `.ods`, `.odp` —
  is read here, out of its own bytes (`internal/office`). Google's own
  Drive MCP reads these server-side; Drive's API offers no text of a
  blob, and converting one means creating a file. The parser takes an
  `io.ReaderAt` over `alt=media` byte ranges with a small block cache,
  so `archive/zip` fetches the zip's directory and the parts that carry
  text, never the images beside them, and nothing goes to disk. Blocks
  are 64 KiB, at most 48 are kept, and reads that run on from the last
  fetch fetch further ahead each time, up to 2 MiB a request; one read
  fetches at most 64 MiB. A document reads in order, with headings
  marked `#`, list items `-` and table rows with `|` between the cells;
  tracked deletions, field codes, comments, footnotes and the second
  copy of a text box kept for older readers are left out. A workbook's
  first visible sheet reads as csv or tsv, each row ending at its last
  value: a number as stored, without its display format; a date or a
  time, decided by the cell's number format, as ISO 8601 in the
  workbook's 1900 or 1904 date system; a formula as its last value. A
  deck reads slide by slide in the order it shows them, under
  `--- slide N ---`, a hidden one marked; speaker notes are left out.
  The whole text is kept like an export, under the same 10 MB cap, so
  paging costs nothing after the first window. The cap and the parser's
  other limits are the zip-bomb bound: at most 10 000 entries and an 8
  MiB directory, checked from the end record and by counting the
  directory's headers before `archive/zip` reads them; no part that
  expands more than 100 times past 1 MiB, the ratio Apache POI refuses,
  by the sizes the part declares and by the bytes it actually pulls from
  the file; at most 256 MiB of XML parsed, one token of it at most 1
  MiB; nesting at most 256 deep; at most 65 536 relationships, styles,
  number formats, sheets or slides. A row is counted as it is read and
  stops the read once it outgrows the cap, a row repeated inside a table
  cell is put together once, and an empty cell repeated across a sheet
  costs nothing. A canceled call stops the parse between two tokens. A
  file past one of these is `[unsupported]`, or its text stops short
  with a note saying so. A file whose content is another kind than its
  type says, such as a workbook given a Word document's type, is
  refused rather than read as empty. `encoding/xml` expands no entity
  it was not given, so a part cannot define one that expands without
  end. The old binary `.doc`, `.xls` and `.ppt` are refused with
  `download_file` and `copy_file` with `convert_to` as the ways forward;
  a password-protected file, which Office keeps in the same binary
  container, is named as such.
- `extract_text` reads the text in a PDF or an image with Google's OCR.
  Drive offers OCR only as part of an import into a Google Doc, so the
  tool makes one: `files.copy` with `mimeType` a Google Doc and
  `ocrLanguage` when given, into the root of My Drive with
  `ignoreDefaultVisibility`, so the copy inherits nobody's access and
  skips an organization's rule that shares every new file. It exports
  the copy as plain text and deletes the copy for good, naming its id
  so `get_file` can show it is gone, or with `keep_copy` keeps it and
  names its id and link. A window read from the kept text says when it
  was read. It takes `offset` and
  `max_chars` like `read_file`, and keeps the whole text for
  `ExportTTL` apart from the text other reads keep, so paging makes no
  second copy even with a `read_file` between two windows; with
  `keep_copy` every call makes one. The continuation names
  `ocr_language` when it was given, since another language is another
  reading. A window that had to be read again, its text no longer
  kept, says that a second reading can differ and the window may not
  start where the last ended. It is a write, full mode only, and asks
  nothing (§4a). Its annotations are a write's: it adds a file and
  removes only that file, so `destructiveHint` is false, and two calls
  make two copies, so it is not idempotent. It returns text only, as
  `read_file` does (§4).

  The hazards, each handled before or after the copy. A Doc refuses an
  id from `files.generateIds` (§2), so the copy cannot be repeated
  safely and the client does not repeat it: a copy Google did not
  confirm is `[ambiguous_outcome]`, naming the copy and the
  `search_files` `raw_query` that finds it, an `appProperties` marker
  with a value unique to the call (private to this OAuth client, so only
  this server's searches see it). A copy that could not be deleted is
  named in the result, which still carries the text; the delete runs
  even when the call was canceled, under its own deadline, and only on
  the Google Doc Drive's answer to the copy named. The copy itself runs
  on when the call is canceled, under the same deadline, so Google's
  answer still names the copy, which is then deleted, even one asked
  to be kept: a cancel that cut the request short left a copy nobody
  was told of. A failed export
  deletes the copy and says so. Before anything is written it refuses a
  folder, a file `read_file` reads directly (a Google document, an
  Office file, text), anything but a PDF or an image, a type the
  account's import formats do not turn into a Doc (the import guide's
  JPEG, PNG, GIF, BMP and PDF when those cannot be read), a file over
  50 MB, and one whose `capabilities.canCopy` is false. Past 2 MB it
  reads and warns, since Google asks for 2 MB or less. Every belief
  about Google's OCR here is unverified until the live driver has run
  it (§18).
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
  Workspace file, or `content` (inline text) with `mime_type` (default:
  the type `internal/mediatype` gives the name's extension when it is a
  kind of text, so `notes.md` is `text/markdown`, otherwise `text/plain`;
  with `convert_to`, the name's type only when Google imports it as that
  kind, so a `rows.csv` made into a Doc goes as `text/plain`) and
  optional `convert_to` (`doc`, `sheet`, `slides`)
  through Google's import. Plus `description`, `starred`. Multipart
  upload with a pre-generated id **where Drive takes one**: the Docs
  Editors formats refuse it (§18), so those creates are the ones that
  are not idempotent, and the duplicate-name guard is what catches a
  double. Refuses content above 5 MB (use `upload_file`).
- `upload_file`: `local_path` (inside `GDRIVE_LOCAL_DIR` after symlinks
  are resolved), `name` (default the file name), `parent`, `mime_type`
  (default: by extension, then by sniffing; an extension
  `internal/mediatype` names, such as `.md`, `.csv` or `.docx`, gives the
  same type whatever the platform's tables say, §18),
  `convert_to`, `ocr_language`,
  `description`. Up to 5 MB multipart, above that resumable in 8 MiB
  chunks (a multiple of 256 KiB), each chunk under its own timeout. A cut
  connection is handled inside the call: query the session with
  `Content-Range: bytes */total`, read the `Range` from the `308`,
  continue. The blob's md5 is compared with the metadata Drive returns
  (conversions have none, and the result says so). Returns the file card.
- `update_content`: `file` plus `content` or `local_path`, `mime_type`
  (default: the type the file has, local file or not, because Drive makes
  the uploaded type the file's, §18), `keep_previous_revision` (pins the
  replaced revision with `revisions.update keepForever` after the upload;
  Drive keeps it 30 days anyway), `expect_head_revision` (a best-effort
  check, since Drive v3 has no preconditions), `allow_comment_changes`.
  Refused for Workspace documents (`[unsupported] this is a Google Doc;
  its content is edited through the Docs API, or upload a new file with
  convert_to`), folders and shortcuts. Returns old and new head revision
  ids and sizes.
- **A Markdown file is not only bytes.** Google Docs opens a
  `text/markdown` file in Drive and edits it in place, and writes the
  comments made there into the file's own text: a
  `<!-- gdoc_comment_begin: KEY -->` and a `<!-- gdoc_comment_end: KEY -->`
  around the passage, and an entry under the key in a `gdoc_comments`
  block that must end the file (§18). Drive's comments do not list them,
  so a replacement that drops one deletes it for good. Before replacing
  such a file, `update_content` reads it and the new content as streams
  (`internal/mdcomment`, a line at a time, at most 4 MiB a line and
  30 000 markers and entries) and refuses, as `[invalid]` naming each
  comment, content that drops an entry or either marker, changes an
  entry's status, or goes on after the block, and a `mime_type` that is
  not Markdown, which Drive would make the file's type. A
  `text/x-markdown` file, which Drive keeps under that older type and the
  Docs API reads as Markdown all the same (§18), is checked and described
  the same way. `allow_comment_changes` skips the check and the read. A
  person editing the file in Docs saves into the same text a few seconds
  after each change, and nothing here can hold a write until they have.
  So for a Markdown file `update_content` looks at the head revision once
  more just before the upload and refuses, as `[exists]`, if it moved;
  afterwards it reads the
  version history and names any save that landed between that look and
  the upload, which the upload replaced and Drive keeps 30 days unless it
  is pinned; `keep_previous_revision` pins the revision the upload
  actually replaced, which is that save when there was one. That narrows
  the window and names what falls in it; it does not close it. With
  `allow_comment_changes` nothing is read between the fresh resolve and
  the upload, so there is no last look; the history is still read.

### 7.3 Organizing

- `create_folder`: `name`, `parent`, `description`, `color`. Drive allows
  two folders of one name side by side; this tool does not, unless
  `allow_duplicate: true`: an existing sibling of that name is `[exists]`
  with its id (§17).
- `update_file`: `name`, `description`, `starred`, `color` (folders),
  `properties` (a map; an empty value deletes a key),
  `copy_requires_writer_permission`, `writers_can_share`,
  `restrict_download`, `limited_access` (folders). Patch semantics:
  only the fields passed change. Returns before and after per field.

  `restrict_download` is `none`, `viewers` (viewers and commenters) or
  `editors` (editors as well), sent as
  `downloadRestrictions.itemDownloadRestriction` with both switches. It
  needs `capabilities.canChangeItemDownloadRestriction`, which Drive
  gives a file's owner or a shared drive's organizer. The card shows
  `effectiveDownloadRestrictionWithContext`, which counts the shared
  drive's restriction and the organization's data loss prevention
  rules, and says so when that is more than the file's own setting.
  `copy_requires_writer_permission` is kept as Drive's legacy switch and
  described as such: since 2025-07-16 it reads whether viewers are
  restricted, and false lifts the restriction on editors too, which the
  result now says. The files guide warns the two "might conflict", so
  passing both in one call is refused.

  `limited_access` is `inheritedPermissionsDisabled`, which Drive offers
  on folders only: on, only the people added to the folder directly open
  it, and those who reach it from above see it without opening it. A
  file is refused before Drive is asked, and so is a change the
  capabilities (`canDisableInheritedPermissions`,
  `canEnableInheritedPermissions`) do not allow. Off lets everyone who
  reaches the folder above open it and everything inside it, so the
  server reads the folder's permissions and the folder above's, works
  out who that adds the way a move into that folder is worked out. It
  is refused `[blocked]`, naming them, when that adds anyone with the
  link, a domain or someone outside the organization, or either list
  cannot be read (§4a). `list_permissions` marks a `view: metadata` grant as one that
  "can see it but not open it", and the sharing summary counts those
  apart.
- `move_file`: `file`, or `files` for up to 50 items, `to` (a folder,
  `root`, or a shared drive). Checks
  the single-parent rule, refuses a My Drive folder bound for a shared
  drive with the API's reason and the alternative ("create the folder in
  the shared drive, then move the files"), supports `dry_run`. Returns
  old path and new path, and who could reach the item before and who can
  after (`sharing_before`, `sharing_after`).

  A move changes who can reach the item (§18): it keeps the grants made
  on it directly, loses what it inherited where it was, and inherits
  everything the destination has; a My Drive item moved into a shared
  drive loses its owner to the drive. Before the move the server reads
  the item's permissions and the destination's and works out the after
  from those rules. When what the after adds includes anyone with the
  link, a domain or a person or group outside the organization, or
  either list cannot be read, the move is refused `[blocked]`, dry run
  included, naming each by address, domain or link (§4a). Adding only
  people and groups inside the organization is an ordinary move, and
  the result says who it added. With `GDRIVE_SHARING=off` any move that
  adds anyone is refused, `[forbidden]`, and a list it cannot read is
  refused too: nothing shows it does not widen. With `files`, each such
  item is refused with the reason and the rest move. After the move the server reads the item's permissions again
  and reports those, and says so when they differ from what it worked
  out, the account's own access included, since the summary counts it.
  The signed-in account's own access is left out of who a move adds.
  An owner of the destination who does not own the item counts as an
  editor, since what Drive gives them is not documented. Children of
  a moved folder are not read one by one: they are reached the same
  way.

  With `files`, every item is read and checked before anything moves,
  and each gets its own outcome in `items`: `moved`, `unchanged` when it
  is already there, `refused` with the reason when a check stops it (a
  reference that does not resolve, an item listed twice, one Drive
  would refuse), or `failed` with Drive's answer. A dry run reports
  `would_move`. Items move one call each, in order, and a failure does not undo the
  moves before it or stop the ones after. A failed item is read back:
  one Drive moved anyway is reported as moved, and one it did not says
  where it is. A call that moves none because each item failed or was
  refused reports the action `failed`, never `unchanged`, which says
  there was nothing to do.
- `copy_file`: `file`, `name` (default "Copy of …", as Drive does), `to`
  (default the source's folder, as Drive does), `convert_to` (import
  conversion, OCR for PDFs and images, `ocr_language`),
  `keep_revision_forever`. Uses a pre-generated id. Folders are refused
  in Phase 1 with the reason; Phase 3 adds `recursive: true` with an item
  budget, refused before any write when the source or a folder inside it
  cannot be listed. `copy_comments`, off by default, asks Drive to bring the
  threads along; Drive's own formats carry them and uploaded bytes do
  not (§18). A copy takes on who can reach the folder it lands in, and
  none of the grants made on the original (§18). Outside a shared drive
  the copy is the signed-in account's, so the destination's owner, who
  is usually that same account, is not counted as an editor of it. So
  before it copies, the server reads who reaches the original and who
  reaches the destination, as a move does; the result says who reaches
  each, read back from Drive after the copy. A copy that anyone with the
  link, a domain or someone outside the organization could reach when
  they cannot reach the original is refused `[blocked]`, naming them,
  as is one whose reach cannot be read (§4a); with `GDRIVE_SHARING=off`
  any copy more people could reach is refused. A copy only this account
  would reach goes, even when who reaches the original cannot be read. A
  folder's copy is checked part by part: the folder itself, and each
  folder inside it with limited access, which reaches fewer people
  than the folder around it. Every other item reaches at least who its
  folder does, so they need no read of their own. The refusal names
  each part the copy would open past the organization, before anything
  is written.
- `create_shortcut`: `target`, `parent`, `name` (default the target's).
- `trash_file` and `restore_file`: the result names the item, says
  "folder with its contents" when it is one, reminds that the trash
  empties after 30 days, and shows the location after a restore.
  `dry_run` on both.

### 7.4 Sharing (`internal/service/access.go`)

**Who decides what may be shared.** Google does, per organization: the
Workspace admin's external-sharing setting (off, allowlisted domains, or
on, per organizational unit) is enforced by the Drive backend on every
API call, and shared drives add their own restrictions. This server does
not copy those rules. What it adds is what only the server can add:
brakes on the agent doing something the person is *allowed* to do but
did not ask for. The deployer sets `GDRIVE_SHARING`:

| Value | Meaning |
|---|---|
| `all` (default) | `share_file`, `unshare_file` and `resolve_access_request` are registered. `share_file` grants named people and groups inside the organization; the wide forms are their own tools behind `GDRIVE_ENABLE_DESTRUCTIVE` (§4a). |
| `off` | `share_file`, `unshare_file` and shared-drive membership changes are not registered. `list_permissions` stays. Loosening a shared drive's restrictions or a file's own switches is refused (§7.5), and so is a move that lets more people reach an item, or a copy that more people could reach than the original (§7.3). |

Before any sharing call the server reads `capabilities.canShare` on the
file and refuses with `[forbidden] you cannot change sharing on this
file` when it is false, as the sharing guide asks. A grant the
organization's policy blocks comes back from Google as 400
`invalidSharingRequest` ("ACL change not allowed"), 400
`shareOutNotPermittedForContent` (a file flagged as restricted content)
or 403 `domainPolicy`; the server maps that family to `[blocked] your
organization's sharing policy does not allow this` with Google's own
message attached. The exact shape of a policy refusal is observed live
in Phase 2 (§16) and recorded in §18.

`share_file` takes `file` (or a shared drive, for membership),
`principal` (`someone@example.com` or `group:team@example.com`, inside
the organization), `role` (`reader`, `commenter`, `writer`,
`file_organizer`, `organizer`), `notify` (default false; Google's
default is true), `message`, `expires` (RFC 3339 or a duration like
`30d`, at most a year, or `never`), and `dry_run`. An address outside
the organization, `anyone`, a `domain:` and the role `owner` are refused
`[blocked]`, naming the tool that does each (§4a).
`share_outside_organization` takes the same arguments for a person or a
group outside, and `confirm`. `share_by_link` takes `principal`
(`anyone` or `domain:example.com`), `role` (`reader`, `commenter`,
`writer`), `discoverable` (`allowFileDiscovery`; left out, it keeps what
the grant has, and a new grant is by link only), `confirm` and
`dry_run`. `transfer_ownership` takes `to`, `message`, `confirm` and
`dry_run`; Google always mails a transfer, which the result says, and
on a consumer account the result reads any pending acceptance off the
answer. Each of the three also takes `file_name`, the item's name as
Drive has it, or `[stale]` (§4a). The three wide tools exist only with
`GDRIVE_ENABLE_DESTRUCTIVE=true`. A principal that already has a grant is updated, and the
result says "changed from reader to writer". The text shows exposure
before and after; the JSON carries the permission id and the new sharing
summary. `unshare_file` takes `principal` or `permission_id`, and
`remove_link: true` for the `anyone` or `domain` link grant; an inherited
shared-drive permission is refused with the source named. An owner's
grant is never called inherited: Drive lists a My Drive owner's grant
both as made on the item and as coming from the folder above that the
owner also owns. A dry run of either tool says what the grant would
do, never that it did.

Publishing a revision to the web (`revisions.update published`) is an
exposure with no audience control and is deliberately not offered.

### 7.5 Shared drives

`list_drives` shows name, id, the signed-in person's role, hidden state
and restrictions, with `include_hidden`. `manage_drive` takes `action`
(`create`, `rename`, `hide`, `unhide`, `restrict`), `name`, the five
restriction flags (`domain_users_only`, `members_only`,
`copy_requires_writer_permission`, `admin_managed`,
`folder_sharing_requires_organizer`), `dry_run`. Every flag is a limit,
so turning one off loosens the drive: with `GDRIVE_SHARING=off` that is
refused, and turning one on is not. Turning `domain_users_only` off is
refused `[blocked]` in any configuration, naming `lift_drive_restriction`,
which takes `drive`, `drive_name` (the drive's name, or `[stale]`),
`restriction`, `confirm` and `dry_run` and exists
only with `GDRIVE_ENABLE_DESTRUCTIVE=true` (§4a). `members_only` off stays
here until the probe §17 records. `update_file`'s
`copy_requires_writer_permission: false`, `writers_can_share: true`, a
`restrict_download` that restricts fewer people than the file does, and
`limited_access: false` are refused there the same way. Members
are added and removed through `share_file` and `unshare_file` with the
drive as the target. `delete_drive` is gated and needs an empty drive.

### 7.6 History

- `list_revisions`: newest first, with id, time, who, size, whether it is
  kept forever, and the mime type; the output repeats Google's caveat
  that the list can be incomplete for busy Docs. These are Drive revision
  ids, not the Docs API's revision tokens; content comes through
  `download_file` with `revision`.
- `manage_revision`: `action: keep | unkeep` (`keepForever`).
  `delete_revision` is gated. Only the current revision unpins: Google
  documents a pin as one-way, and Drive refuses to unpin a revision that
  is no longer current (`illegalKeepForeverModification`, §18), so a pin
  on any other revision, `keep_previous_revision`'s included, is for
  good, and the results say so.
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
that pinning a comment in a Google Doc, Sheet or Slides deck is a feature
of the Docs, Sheets or Slides API, which this server does not use),
`reply_comment` (`action: reply | resolve | reopen | edit`), and the
gated `delete_comment`. One Drive backend; the reason to have it here is
every file that is not a Doc.

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

A Markdown file's comments made in Google Docs are in its text, not here
(§7.2), so `list_comments` on one says that this list does not show them
and that `read_file` does.

### 7.8 Access requests (built in phase 3)

`list_access_requests` lists pending proposals on a file (who asked, who
would receive the access, the roles asked for, their message, when);
`resolve_access_request` accepts with a role or denies, with `notify`.
Only approvers can list them, and the API cannot create one. An accept
for someone outside the organization is refused `[blocked]`, naming
`share_outside_organization` and Drive itself (§4a).

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

**Approvals** are `list_approvals`, `manage_approval` (start, decline,
cancel, comment, reassign) and `approve_file`, on the ordinary scope.
Approving is its own tool, `destructiveHint: true`, since it is the
person's sign-off and can lock the file for good (§4a). Two things
about them are not what the name suggests and are said in the tool
description and in every result: every verb MAILS somebody, with no
notify flag anywhere, unlike sharing; and an approval can LOCK the file,
at once with `lock_file` or on approval under the default
`RESET_APPROVAL` behavior. `on_content_change` on start chooses that
behavior: `reset_approval`, the default, clears answers given when the
content changes while the approval is open and locks the file once it
is approved; `no_action` does neither. The server sends it even for the
default and reports the behavior from Drive's answer, saying so when it
differs from what was asked. Declining completes an approval on its own
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
"Gated" means registered only with `GDRIVE_ENABLE_DESTRUCTIVE=true`, and
needing `confirm: true` on the call. `delete_file`, `empty_trash`,
`delete_drive` and `share_by_link` also set
`_meta["anthropic/requiresUserInteraction"]`, for every client, and no
other tool does (§4a).
`GDRIVE_READ_ONLY=true` registers only the readOnly rows, and
`download_file`, which writes only a new local file, and requests
`drive.readonly`. Every write states `destructiveHint`: the
specification reads an unset one as true, and `false` as "only additive
updates", so only the eight writes that add say it. `GDRIVE_SHARING=off` removes the rows marked *sharing*
— which includes `resolve_access_request`, because accepting a request
grants a permission.

| Tool | Purpose | Annotations | Phase |
|---|---|---|---|
| `get_account` | Who is signed in, storage used and limit, can create shared drives, sharing policy in effect | readOnly | 0 |
| `get_file` | The file card (§7.1) | readOnly | 0 |
| `search_files` | Typed search across My Drive, shared with me and shared drives, or under one folder at any depth | readOnly | 0 |
| `list_folder` | One page of children, or a budgeted tree | readOnly | 0 |
| `read_file` | Text of a file, budgeted with continuation | readOnly | 1 |
| `extract_text` | Text of a PDF or an image by Google's OCR, through a temporary Doc copy it deletes; text only | — | after 7 |
| `download_file` | Blob, export or old revision to `GDRIVE_LOCAL_DIR`, checksum-verified | local write | 1 |
| `create_file` | Empty Workspace file, or inline text with optional conversion | — | 1 |
| `upload_file` | A local file, multipart or resumable, optional conversion | — | 1 |
| `update_content` | Replace a blob's content; the old one stays a revision for about 30 days unless pinned | destructive | 1 |
| `create_folder` | New folder; refuses a duplicate name unless allowed | — | 1 |
| `update_file` | Rename, describe, star, color, properties, sharing switches, download restriction, a folder's limited access (refused off when that reaches past the organization) | destructive, idempotent | 1 |
| `move_file` | Move one item, or up to 50, to a folder or shared drive; single parent; who can reach each before and after, refusing an item it would open past the organization; dry run | destructive, idempotent | 1 |
| `copy_file` | Copy, optionally converting (OCR); `recursive` walks a folder, refusing a tree over budget rather than copying half of it | — | 1, 3 |
| `create_shortcut` | Shortcut to a file or folder | — | 1 |
| `trash_file`, `restore_file` | Reversible removal and its undo | destructive, idempotent | 1 |
| `list_permissions` | Who has access, how, inherited from where, link state | readOnly | 2 |
| `share_file` | Grant or change access for named people and groups inside the organization; policy-checked; before/after | *sharing*, destructive | 2, 10 |
| `unshare_file` | Revoke a grant or a link | *sharing*, destructive, idempotent | 2 |
| `list_drives` | Shared drives with your role and restrictions | readOnly | 2 |
| `manage_drive` | Create, rename, hide, unhide, restrict a shared drive; never turns `domain_users_only` off | destructive, idempotent | 2 |
| `list_revisions` | Version history with the incompleteness caveat | readOnly | 2 |
| `manage_revision` | Keep or unkeep a revision | destructive, idempotent | 2 |
| `list_changes` | The changes feed: start token, then changes since | readOnly | 2 |
| `list_comments` | Threads on any file | readOnly | 3 |
| `add_comment`, `reply_comment` | Unanchored comment; reply, resolve, reopen, edit (which replaces the text with no history) | — / destructive | 3, 10 |
| `list_access_requests` | Who has asked to be let in, and for what | readOnly | 3 |
| `resolve_access_request` | Accept one from inside the organization, or deny any; accepting grants a permission, so it is policy-checked with before and after | *sharing*, destructive | 3 |
| `list_labels`, `manage_labels` | Workspace labels: the definitions this account may use, and applying one to a file. Registered only with `GDRIVE_LABELS=true` | readOnly / destructive | 4, 10 |
| `list_approvals` | The reviews on a file, and whether one waits on you | readOnly | 4 |
| `manage_approval` | Start, decline, withdraw, comment on or reassign a review. Every action mails somebody; an approval can lock the file once approved, unless started with `on_content_change: no_action` | destructive | 4 |
| `approve_file` | Approve a review you were asked for: your sign-off, which Drive cannot withdraw and which can lock the file for good | destructive | 10 |
| `list_activity` | What happened to a file or inside a folder. Registered only with `GDRIVE_ACTIVITY=true` | readOnly | 4 |
| `delete_file` | Gated: permanent, skips the trash | destructive | 2 |
| `empty_trash` | Gated: everything in the trash, or one shared drive's | destructive | 2 |
| `delete_drive` | Gated: an empty shared drive | destructive | 2 |
| `delete_revision` | Gated: one blob revision | destructive | 2 |
| `delete_comment` | Gated: a thread or one reply | destructive | 3 |
| `share_by_link` | Gated, *sharing*: anyone with the link, or a whole domain | destructive | 10 |
| `share_outside_organization` | Gated, *sharing*: a person or a group outside the organization | destructive | 10 |
| `transfer_ownership` | Gated, *sharing*: a new owner, for good | destructive | 10 |
| `lift_drive_restriction` | Gated, *sharing*: turn a shared drive's `domain_users_only` off | destructive | 10 |

There is deliberately no bulk delete and no bulk share: one item per
call, so "remove these forty files" is forty approvals in the client. A
folder is the unit for bulk operations, and trashing one is reversible.
`move_file` is the exception, with up to 50 items in `files` (§18): a
move can be moved back, and the part of it that cannot be taken back,
who saw the item meanwhile, is refused item by item whenever it would
reach past the organization (§4a).

**Resources** (built in phase 3). `gdrive://{file}` is the text
`read_file` gives (markdown for a Doc, csv for a Sheet or a workbook,
plain text for an Office document or deck, the file's own type
otherwise) under one 400 000-character budget, and the media type
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
before and after summaries. `extract_text` is the exception: a write
whose result is a file's text, returned as a text block alone (§4).

## 9. Confidentiality, security, safety

**Nothing internal leaves the user's machine or enters the repository**
(**decided**).

- The repository never contains organization names, file or folder ids,
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
| Exposing a file to the world, a domain or someone outside the organization | The organization's own sharing policy, enforced by Google on every call; `share_file` limited to named people and groups inside the organization, and each wide form its own tool behind `GDRIVE_ENABLE_DESTRUCTIVE` with `confirm: true` (§4a); before-and-after exposure in every sharing result, move and copy; a move, a copy or limited access turned off that would reach past the organization refused, naming who; `dry_run`; `GDRIVE_SHARING=off`; no publish-to-web at all. |
| Unwanted email to people | `notify` is off unless asked; the result says when Google forced it on. |
| Mass deletion | Trash is the only default removal and it is reversible; permanent deletion and emptying the trash are gated; no bulk tool. The one ungated permanent delete is `extract_text` removing the copy it made in the same call, and only the file Drive's answer to that copy named. |
| Copying private files onto disk | Downloads only under `GDRIVE_LOCAL_DIR`, size-capped, named in the result. |
| Uploading files the person did not mean to share | Uploads only from `GDRIVE_LOCAL_DIR`; inline content is what the model wrote. |
| Instructions hidden in file content | Read tools are read-only and content is returned as data; the server never acts on what a file says; the client's per-call approval covers writes. |
| Runaway listings and quota exhaustion | Page and tree budgets, per-process limiters, backoff honoring Google's reasons. |
| A stalled request hanging the server | Every call, token refresh and chunk has a deadline; bodies and frames are bounded. |
| Secrets in the repository | gitleaks in pre-commit and CI; synthetic fixtures. |

## 10. Auth, config, process model

- **Google account and Cloud project.** Each deployer runs the server
  under a Cloud project they own, with a dedicated Desktop-app OAuth
  client so its tokens can be revoked independently. Setup in the order
  `doctor` checks it: project → enable the **Google Drive API** (and the
  Drive Labels API when labels are on) → consent screen (**Internal** for
  Workspace, **External + Testing** with the user added for consumer
  accounts, which means weekly `login`) → add the scopes, on an External
  app only → Desktop-app
  client → download `client_secret.json` → `login` → `doctor`.
- **No Developer Preview is needed.** Everything this server uses is GA.
  Google's own Drive MCP is in the preview program and is a comparison
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

**Drive's reads lag Drive's writes, on five endpoints and counting.** This
is a property of the API rather than of one call, and the list is here
rather than only in §18 because §18 is where a finding is recorded and
this is where somebody writing the next call site looks.

| Surface | What lags | What this server does |
|---|---|---|
| The changes feed | A write is not in the feed it was made against | The token design absorbs it; the driver asks twice |
| The property index | Nothing, once. A run on 2026-10-09 found a property it had just set, and a link grant it had just added and removed, on the first search after each change (§18). One run is not a bound. It was listed here from a phase 4 run whose search could not find the file at any speed | Nothing server-side — an empty page is a valid answer. The driver searches under its scratch folder, on every page, and reports UNVERIFIED rather than failing |
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
  backoff and full jitter, capped at 30 s, five attempts, honoring
  `Retry-After`. Metadata patches, moves and permission updates retry the
  same way: they are idempotent. Creates, uploads and copies retry
  because they carry a pre-generated id; a duplicate-id answer after an
  ambiguous failure is reported as `[ambiguous_outcome]`, because the
  earlier attempt may have made the file, and `get_file` settles it. **A create that cannot carry one is not retried at all**:
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
- **Sharing is not repeated after it may have landed.**
  `permissions.create` is a POST with no id, and the failure that
  matters is not a duplicate grant — granting the same person the same
  role twice is the same grant. It is that a share applied after the
  caller believed the call failed is exposure nobody is watching, which
  is the one direction §9 exists to prevent. So a 429, which refuses the
  work before it starts, is retried, and a 5xx or a connection lost
  after sending is reported as `[ambiguous_outcome]` with
  `list_permissions` named as the way to find out. `ambiguous` in
  `internal/gapi/client.go` is where that is expressed, for every write
  that cannot be repeated. A delete retried after a 5xx that then finds
  nothing is ambiguous too: the earlier attempt may have deleted it.
  Resumable uploads recover through the protocol itself.
  `extract_text`'s copy is such a create: a Google Doc, so no id, so a
  5xx or a cut connection is `[ambiguous_outcome]` with the marker that
  finds the copy, and never a second copy.
- **Limiters.** Reads and listings 10/s, burst 20. Writes 5/s, burst 10.
  Sharing 1/s, burst 3 (`sharingRateLimitExceeded` is its own quota).
  Transfers are bandwidth-bound, not count-bound.
- **Deadlines.** `GDRIVE_HTTP_TIMEOUT` per attempt and per 8 MiB chunk;
  the whole call under the MCP request context. The token refresh runs
  under the same deadline (a wrapped token source), so a stalled refresh
  cannot hang later calls.
- **Memory.** Streaming in both directions; `read_file` fetches only the
  window it shows of a text file, and of an Office file its directory
  and text parts through a 3 MiB block cache, holding at most the 10 MB
  of text; a listing holds one page; a tree walk holds its budget.
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
- **The Claude Desktop bundle** asks for what a person sets in another
  client's configuration: the OAuth client file, the profile, read-only,
  the destructive switch, the sharing tools, the local directory, the
  download cap, labels and activity, each under its variable's name
  without `GDRIVE_`, lowercased. Logging, the HTTP timeout, the config
  directory and the refresh token are left out, each with its reason in
  `gates mcpb`. The gate derives what is asked from `config.Vars`, so a
  new variable fails it until the bundle asks for it or a reason is
  written. Each switch defaults to off and every other optional key,
  the OAuth client file included, to `""`, which the server reads as
  unset; no default holds a `${` reference, which the host would expand.
  The gate starts the server through `config.Load` from the
  environment the reference host builds from those defaults and from
  none, and names any field that differs. Claude Desktop approves each
  tool, and a person can set a tool to always allow, so a switch left
  off is the one guard a bundle install keeps (§18). Settings pass by
  `mcp_config.env` alone: the base `args` is `[]` and no platform
  override carries `args` or `env`, which the host takes in place of
  the base's (§18). Every command starts with `${__dirname}/` and names
  a program the packer stages (§18), and `entry_point` is the file the
  default command runs. The manifest's `name` is the server's,
  `server.type` is `binary`, and `compatibility.platforms` is exactly
  the platforms the packer stages an entry point for. The profile field states
  `config.ProfileRule` and the sharing field the modes the server
  accepts. The server refuses no combination of the switches, and the
  gate holds that: it starts every set of them and fails on a refusal
  no field warns of. The other fields are each checked on their own. Read-only leaves the destructive switch
  nothing to register, and `sharing` off takes its four sharing tools,
  so those fields say so. The bundle stages
  `server/google-drive-mcp-darwin`, `server/google-drive-mcp.exe`,
  `server/google-drive-mcp-linux-amd64`, `server/google-drive-mcp-linux-arm64`
  and `server/launch-linux.sh`, with `LICENSE` and `README.md` at the root.
  The gate and the packer start from one function, which reads the
  manifest, the launcher, `go.mod` and `.goreleaser.yaml` once from one
  root, refuses a key repeated in one object and a version other than
  the placeholder; the packer validates the stamped manifest it ships
  against the vendored schema, with a Semantic Versioning version, and
  a failed close of the bundle fails the pack.
- **Setup guide** in the README, in the order `doctor` checks it (§10).
- **Versioning.** Semantic versions; Keep a Changelog; the schema diff in
  CI fails on a removed or renamed tool or resource, a field removed at
  any depth, a type change a caller would notice, an output newly
  optional, an input that loses a listed value, and a newly required
  input.
- **Documentation set.** README, this file, `docs/configuration.md`,
  `docs/security.md`, `docs/development.md`, `CONTRIBUTING.md`,
  `SECURITY.md`, `CHANGELOG.md`. Apache-2.0.

## 13. Testing

- **Unit, table-driven, no network.** `internal/gapi/drivetest` is an
  in-memory Drive behind `httptest`: files with parents and kinds, trash
  with cascade, permissions with the one-per-principal rule and inherited
  shared-drive grants, revisions, shared drives, a changes log, resumable
  upload sessions that honor `Content-Range` and answer `308` with the
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
- **Schema dump and diff** in CI against `testdata/schema-baseline.json`,
  the surface of the CHANGELOG's newest release, recorded in that
  release's commit by `make schema-baseline VERSION=vX.Y.Z`. The diff
  compares input and output fields at any depth with their types, one
  way, as JSON Schema 2020-12 reads a type: an input may take more types
  than it did and an output return fewer, so a `bool` becoming a `*bool`
  input, which may now be null, passes, while an output that may now be
  null fails. An integer is a number; no type, or the schema `true`, is
  any value, and the schema `false` none. An output a caller read as
  required that may now be missing fails, and so does an input that
  stops taking a listed value; an output that may carry a new listed
  value is printed for a person to read. It
  also fails when the baseline is not the newest release's, and, with
  nothing under `[Unreleased]`, when the build differs from it at all.
  It used to build the last tag and compare top-level inputs only: an
  output field could be dropped unseen, and a deliberate break had no
  way through before its tag existed. The baseline was recorded from
  the v2.0.1 tag, whose dump already carried output schemas.
- **Fuzzing.** `internal/office` parses bytes from any file in anyone's
  Drive, so it has two fuzz targets: `FuzzExtract` over whole files and
  `FuzzPartXML` over the XML of the part that carries the text, in a
  well-formed container. Each must never panic, return more text than
  its cap, or allocate more than 32 MiB under the fuzz limits; their
  seeds include the shapes that once cost hundreds of megabytes. Tests
  beside them hold each such shape to a memory and a time bound. The
  seeds run in `make check`; `go test -fuzz` runs them for longer.
- **Stdio smoke** without credentials: initialize with the newest
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
- **Agent evals** `scripts/evals` (built in phase 3, seventeen tasks
  since the Markdown-file fix), tasks
  through `claude -p` with only this server's tools: find a file and say
  who can see it; build a folder structure and move a file into it; share
  with someone as commenter without emailing them; share with one person
  and do not make a public link; download a file; upload a markdown file
  as a Google Doc; get a starting point for the changes feed and explain
  the token; restore a trashed file; handle a duplicate name; read the
  tail of a large text file; rename and describe; comment on a file and
  resolve the thread; copy a folder and everything in it; change a date in
  a Markdown file holding a comment made in Google Docs, keeping the
  comment.
  Scored on the end state read back through the server and on the trace:
  no invented ids, ambiguity handled by asking or listing, and no tool
  that shares past the organization called unasked. The trace half is the
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
| Google's sharing policy is the authority; the server adds only agent brakes: `canShare` checked first, notifications off by default, `GDRIVE_SHARING=off` available. *(2026-10-23: `anyone` is no longer double-gated by an argument. It, a domain, someone outside the organization and a new owner are each their own tool behind `GDRIVE_ENABLE_DESTRUCTIVE`.)* | §7.4, §4a |
| The server asks the person nothing; the client approves each call; `GDRIVE_REQUIRE_PROMPT` set refuses to start (2026-10-22 for the family, 2026-10-23 here) | §4a; 3.0.0 |
| Every risky variant is its own tool: `share_by_link`, `share_outside_organization`, `transfer_ownership`, `lift_drive_restriction`, `approve_file` (2026-10-23) | §4a, §8 |
| A wide share names what it opens: `file_name` on the three wide shares and `drive_name` on `lift_drive_restriction`, compared exactly with Drive's, `[stale]` when they differ (2026-10-23, the §3 rule that an argument carries what the question showed) | §4a, §7.4, §7.5 |
| A move, a copy or limited access turned off never reaches past the organization by omission; inside-only gains are ordinary (2026-10-23) | §4a, §7.3 |
| On a personal Google account every address is outside (2026-10-23) | §4a; `get_account` says so |
| The mark on `delete_file`, `empty_trash`, `delete_drive` and `share_by_link` only, for every client (2026-10-23) | §4a, §8 |
| Trash by default, destruction gated, no bulk removal tool | §8, §9 |
| One local directory for transfers, unset means none | §7.1, §7.2, §9 |
| Pre-generated ids and request ids for idempotent writes | §11 |
| Raw REST, own wire types, no generated client | §5 |
| Stdio only | No HTTP auth design |
| The repository is self-contained: no code imported from, and no reference to, any other project or environment | §5, §9 |
| Phases end in a tagged release and wait for an explicit "go" | §16 |

## 15. Reliability of the plan itself: what must be verified live

Everything in §2 comes from the reference. Five things have behavior
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
  Preview Program. Nothing depends on it.

**Phase 1 — content and organization (v0.1.0). Done 2026-09-05.**
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
- **A share the organization's policy blocks**, so the `[blocked]`
  mapping in §7.4 is built from a real response rather than an injected
  one. It needs an administrator to put an external address out of
  bounds on an organizational unit.
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
code.** Four corrections, two of them in behavior that had already
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

**Phase 6 — the gates that were described rather than built (v1.0.1).
Done 2026-09-06.**
No new tools. A Claude Desktop bundle on every release, a README a
released project should have, a code of conduct — and then the three
gates §17a had described for three phases: `transcript`, `live-cover`,
and one for §11's rule against asserting an outcome the response did not
carry.

The write half of the live driver ran at the end of it: 143 calls, all
as expected, 93 of 157 options, and it settled the question the outcome
gate had raised — `copy_comments` did not carry the threads, checked
twice minutes apart to rule out the listing lagging (§18).

The through-line is the one §17a itself names: **a guard that would be
expensive to make automatic gets described as though it were, because the
description is free.** Each of the three was a paragraph explaining what
would catch a defect. Building them cost a day and caught two defects
neither the tests nor eight live runs had found — `copy_file` claiming
comments it could not know had arrived, and the parity gate itself being
defeatable by one `#`.

Three things are worth carrying forward.

**A proposed rule is not a rule.** §17a's version of the outcome gate
would have failed two sites that are already correct, because it named
the function as the unit where the branch is the unit. The entry had been
sitting there for a phase reading as though the work were decided and
only the typing remained. It was not: the design was wrong, and only
writing it against the code said so.

**A measurement can fail by returning a number.** The live-coverage
reader's first working draft read 2 of 188 options and printed it as a
figure, because `any` is an identifier and not an interface node. Nothing
crashed. A gate that reports a number needs either a floor it refuses to
report below or a record that fails when the number moves, and this one
has the second: 122 of 188, with every gap holding a decision.

**Watch a gate fail before believing it, including one written an hour
ago.** The transcript gate was reviewed by writing `log.Printf` into the
driver and running it. It passed. Its own test enumerated the same six
call shapes the implementation did, which is how a test comes to agree
with the bug it should catch — the phase-4 lesson, one level up.

Four sibling repositories were building the same gates the same week, and
the cross-checking found more than any of them found alone: the bundle
reaching `checksums.txt` only through `extra_files`, three parity and
vocabulary holes, and the argument for a live driver recording what it
SENT rather than a gate reading what it says it sends.


**Phase 7 — the person confirms (v2.0.0). Done 2026-09-30.**
The server asks the person, through MCP form elicitation, before the
destructive five, before a share to `anyone`, a `domain:` or a new
owner, and before `manage_drive` turns a restriction off (§4a). First,
the defects a review of the surface had found:

- `copy_file` with `dry_run` on one file copied it. Only a tree read
  the flag.
- Seven reversible writes left `destructiveHint` unset, which the
  specification reads as true; `update_content` said false while
  replacing content; `download_file` said read-only while writing a
  local file. A test now fails on any write with the hint unset.
- A `domain:` grant needed no acknowledgment; it needs `allow_domain`.
- `manage_drive restrict` could loosen a drive with
  `GDRIVE_SHARING=off`. It may now only tighten one there.

The first live run failed one step, and the probe it led to found two
more, both in `manage_drive restrict` and both older than this phase
(§18): Drive refuses `downloadRestriction` in the body, which the wire
type had echoed back since 1.1.0, so every restrict failed; and Drive
merges the switches, so the `omitempty` booleans never sent a `false`:
turning a restriction off was a no-op that reported a change. The fake
had been built on the belief that Drive replaces the object, and agreed
with the bug. The patch now carries only the switches that change, as
pointers, and the result reads what changed from Drive's answer.

The live run's transcript printed the comment author's display name
inside a question. The redactor finds names by the positions the
renderers print them in, and its coverage test read only functions
returning a string, so the questions were invisible to it. It reads
`Question` too now, and the position is added.

Run live 2026-09-30 with `-write -destructive`: 250 calls, all as
expected, 15 questions, one declined and the file still there after.

The review round (quality, correctness, security) found no way past the
question. It changed: a thread delete binds its replies; a narrowing
share asks nothing; `manage_revision` is destructive again, since
`unkeep` lets Drive purge a revision; turning the copy switch off says,
and reports, that readers may download again; a share of a folder or a
shared drive says it reaches everything inside; an ownership transfer
shows its message; more invisible characters are stripped; and
`update_file` may not loosen a file's two sharing switches with
`GDRIVE_SHARING=off`. What it left open is in §17a. After it, by the
maintainer's decision, a share outside the organization and accepting
an access request ask too; `update_file`'s switches stay unasked, being
reversible and reaching nobody new. Run live on the final build: 249 calls,
all as expected, 15 questions. A share outside the organization is held
by tests rather than the live run, which shares with a second person only
under `-share`, and that flag also makes the ownership transfer. Run live again
after it: 249 calls, all as expected, 14 questions — one fewer because a
cleanup delete found its file already taken by the trash empty, which
the driver accepts either way.

### Closing a phase

1. `make check` green on all three platforms; the live driver run, and
   in Phase 3 the evals; the review passes done, with findings resolved
   or recorded in §17a with the reason; and `go run ./scripts/gates leaks
   history` green, which `make check` does not run because it walks every
   object the repository has ever held. It had been failing since
   mid-September 2026 before anyone ran it.
2. The status line updated, the phase marked done in §16 with the date,
   what was verified added to §18, the release entry written in
   `CHANGELOG.md`.
3. Released per `docs/development.md`: a "Release N.N.N" commit on a
   topic branch, a pull request, CI green, merge, then the tag pushed on
   its own.
4. Stop and wait for "go".

**Phase 8 — the gap analysis (v2.1.0). Done 2026-10-09.** The
2026-10-08 comparison with the API and with Google's own servers named
what this server could not do; this phase built it. `read_file` reads
Office and OpenDocument files, parsed locally under hard caps, and
`extract_text` reads a PDF or an image through Google's OCR on a
temporary copy it deletes. `update_file` restricts downloads and gives a
folder limited access. `search_files` searches a whole folder tree
(`under_folder`), by who can open a file (`visibility`) and by who it is
shared with (`shared_with`), and shows who shared a file with you and
when. `move_file` and `copy_file` show who can reach an item before and
after, ask before a destination widens that, and refuse it under
`GDRIVE_SHARING=off`; `move_file` moves up to 50 items at once.
`manage_approval` chooses what a content change does to an approval.
Two reviews (correctness and quality) and three live runs, the last on
the code released as v2.1.0, found and settled what the unit suite
could not.
Their verdicts are in §18, with what the next run still owes.

**Phase 9 — Markdown files Google Docs edits in place (v2.2.0). Done
2026-10-14.** Google Docs began opening a `.md` stored in Drive and
editing it in place on 2026-10-05, writing the comments made there into
the file's own text, where Drive's comments do not list them. This
server treated such a file as a blob. `update_content` now refuses to
lose those comments (`internal/mdcomment`, a bounded streaming scan),
keeps the file's own type, looks at the head once more before
uploading and names any save it replaced; the type registry names the
extensions it reads or converts, so a `.md` is Markdown on every
machine; `get_file`, `read_file` and `list_comments` say where the
comments are. `manage_revision` and `keep_previous_revision` say a pin
on an old revision is for good, which Drive enforces. The history leak
scan, failing unnoticed since mid-September, reads attribution trailers
as the header and is a step in closing a phase. Three live runs, the
evals (16 of 17, the label task unreachable), and two rounds of
`/simplify` and `/code-review high`; the verdicts are in §18.

**Phase 10 — the client decides (v3.0.0). Done 2026-10-23.** The
family standard moved per-call approval to the client on
2026-10-22, and the maintainer approved this server's port on
2026-10-23 (§4a, §14). The question to the person is gone, with
`internal/tools/ask.go`, `internal/service/ask.go`, the question
renderers and their tests; `GDRIVE_REQUIRE_PROMPT` set refuses to start.
Six writes the question had been the only guard on now have another:
a share outside the organization is its own tool behind the destructive
flag; accepting an outside access request, a widening move or copy, and
turning a folder's limited access off are refused when they reach past
the organization; turning a shared drive's `domain_users_only` off is
its own tool. The link, domain and ownership shares became tools too,
`approve_file` split from `manage_approval`, the mark moved to four
tools for every client, and `reply_comment` and `manage_labels` became
destructive. The schema diff now lets through a break the
`[Unreleased]` CHANGELOG declares under `**Breaking:**`, naming the
tool.

`/simplify`, `/code-review high` and a security review ran; what they
left is in §17a. The live run with `-write -destructive`, its
transcript read, made 366 calls, all as expected, and put no question
to the person. It drove the new refusals in `share_file`, `move_file`,
`copy_file`, `update_file`, `manage_drive` and `manage_approval`; the
`confirm` and `[stale]` refusals of `share_by_link`,
`transfer_ownership` and `lift_drive_restriction`; a dry run of each
wide tool; a real link share; the shared drive's `domain_users_only`
lifted and read back; and `approve_file`
locking a file in a scratch shared drive while a `no_action` approval
left one open. The runs before it found two things the tests could
not: Drive drops a file's own link grant on a move into a folder open
by link and back out, and Drive's reply ids carry a dot, which the
driver's parser missed (§18). Still owed: the `-share` half, the real
ownership transfer with `-transfer`, and the two probes in §17. Built
with Go 1.27.3, which fixes GO-2026-6809 in `crypto/x509`; govulncheck
found it reachable under 1.27.2.

**3.1.0, reviewed after release.** 3.1.0 shipped without `/simplify`,
which ran over `v3.0.0..v3.1.0` on 2026-10-24; what it found ships in
the next release. The bundle gate kept its own list of the settings it
asks for, added two variables by hand, and read a switch's default from
a field kept only for it; `staleness` read the settings from
`config.go` with a regular expression. All of them now come from
`config.Vars`, and the bundle asks for every variable it records less
those `bundleLeftOut` gives a reason for. Fixed, each with a test that
failed first: `--enable-destructive=true` in the base `args` passed and
turned the deletes on, and so did an override's `args` or `env`; a
command from the root passed, and so did a command or entry point naming
`LICENSE`; the defaults check did not say which field differed, and
guessed at a list default; the profile field promised less than Build
refuses, and Build read the Kelvin sign as `k`; the gate and the packer
each chained the checks, and the packer skipped one. Added: the gate
starts every set of the switches and fails on a refusal no field warns
of, and holds the sharing field to the modes Build accepts. By the
maintainer's decision, the OAuth client file is optional: left empty,
the server uses the client login recorded. Skipped: holding the tools a
field names to the tools its setting registers (§17a), and the copies
of the settings list in the README, this file and the CHANGELOG.
`/code-review high` of `v3.0.0..HEAD` then found, fixed with a test
each that failed first: a variable read outside Define was a list
typed by hand, so a new one would escape both gates (a test now finds
every `GDRIVE_` name the code spells as a literal, or as literals and
`EnvPrefix` joined, and holds it to `EnvVars`); a variable of spaces
alone skipped its default and stopped the server;
`config` imported the keyring for two names, which it now owns; the
sharing refusal typed the modes the field derives; the defaults
comparison would panic on an unexported field; `docs/configuration.md`
still gave the old profile wording; and this file claimed the gate
tries every combination of the fields, where it tries the switches.
Kept: the declared list of refused sets, empty today, since the
family's servers share the mechanism and its tests force a real
refusal. `/security-review`: no finding at confidence 8 or above. A
second `/code-review high`, of those fixes, found the spaces fix
stopping short of the two variables read outside Define, a scan floor
that could no longer fail, a scan blind to longer `+` chains, and a
diff helper typed `any` for its test; each is fixed with a test that
failed first. Two live runs with `-write`, their transcripts read, made
254 and 253 calls, all as expected; the second printed one comment
reply id the redactor left in place (§17a).

**The family sweep of 2026-10-24.** The family's servers were held to
one table of the bundle gate's and the configuration's rules, and this
repository was missing these, each now with a test that failed first.
The server: every variable is read through one `config.Lookup`, which
drops the spaces around a value, so the profile directory and the
refresh token are trimmed as every setting already was, and `logout` no
longer warns of a refresh token of spaces alone. The name scan reads a
`+` chain in order, so a name built after another operand is seen, and
walks `scripts/` too, where the smoke test, the evals and the live
driver set the server's variables. The bundle gate: `fieldDiff` compares
an unexported field with `Value.Equal` only where no interface is inside
its type, rather than panicking; the manifest's `name` is the server's,
`compatibility.platforms` is exactly the platforms an entry point is
staged for, and `entry_point` is the file the default command runs; the
gate and the packer read every file from one root, a key repeated in one
object is refused, and the packer validates the stamped manifest it
ships. The configuration page is held by whole word, both ways, and
states `config.ProfileRule`. Already held: a failed close of the bundle
fails the pack. Not carried: a check of the manifest's `license` against
`LICENSE`, which no row of the table asks for. `/simplify` (one agent,
four angles), `/code-review medium` and `/security-review` then ran
over `main...family-rules`, with the family's second round of fixes.
Fixed, each with a test that failed first: `fieldDiff` reported an
unexported field it cannot name only when no named field differed, and
compared a pointer by its address; the repeated-key refusal named a key
holding a dot ambiguously, and passed a manifest whose keys it could
not walk; `mcpb-manifest` wrote its refusal where the stamped manifest
goes, and its help said `MANIFEST` where it takes a root; a refusal
named the manifest when a problem can be in a file beside it; the
stamped version was only stripped of a `v`, twice, and is now held to
Semantic Versioning, which the schema does not hold, so the stamped
check can fail; the configuration page check counted `xGDRIVE_LABELS`
and `GDRIVE_LABELSx` as `GDRIVE_LABELS` and read `GDRIVE_ENABLE_*` as a
variable; the name scan read `EnvPrefix + "ENABLE_" + kind` as a
variable `GDRIVE_ENABLE_`; a default holding a `${` reference, which
the host expands, was not refused by name; the install hint check
missed a `module` line with a trailing comment; and `config` and
`userconfig` each spelled the default profile, which is now
`config.DefaultProfile` alone. The integration test reads the profile
through `config.Load` rather than its own `os.Getenv`, a one-use alias
of the server's name is gone, and the scan test no longer promises a
list of the live driver's variables that does not exist. Kept: the
override check's guard for a manifest that claims no platform, and the
skip of a claimed platform with no staged file, which the review called
dead: each stops a second report of what the identity check reports,
and a test now fails without each. `userconfig.BaseDir` reads the
process environment rather than an injected one, which only a test
would use; the server reads the process environment either way.
Repeated keys are compared exactly, not under case folding: the
manifest decodes into a map, which keeps `command` and `Command` apart
as the host does. Not carried: spelling the names typed outside
`config` through its constants, since the name scan already holds each
one to `EnvVars`. `/security-review`: no finding at confidence 8 or
above.

## 17. Open decisions

Two, waiting on a live probe:

- **`members_only` off** (approved 2026-10-23, conditionally). If
  turning a shared drive's `members_only` off restores access that
  non-members were already granted on files inside it, it joins
  `domain_users_only` in `lift_drive_restriction`, behind the
  destructive flag, and `manage_drive` refuses it; if not, it stays an
  ordinary `manage_drive` switch. The probe needs a shared drive this
  run makes, `members_only` on, a file inside it granted to a second
  account that is not a member, and a read as that account before and
  after turning it off. Until the probe, nothing depends on its answer:
  `members_only` off is an ordinary write, as it was.
- **A group that admits outside members** (approved 2026-10-23). A
  grant to a group at the organization's domain is ordinary, and its
  result says the grant reaches everyone in the group, including people
  added later, and a member outside the organization too if the
  organization's sharing policy lets them in. Google's Drive help says
  external members of a shared-with group are let in or kept out per
  user by that policy, that later members get access by URL, and that
  Drive marks a file shared with an organization's group "set to allow
  external members" as external even with no external member yet
  (<https://support.google.com/a/users/answer/167101>,
  <https://knowledge.workspace.google.com/admin/drive/manage-external-sharing-for-your-organization>,
  read 2026-10-23). If
  anything readable with this server's scopes shows that a group allows
  external members (the permission resource, `about`, or another field
  Drive returns), `share_file` refuses such a group `[blocked]`, naming
  `share_outside_organization`, and a move or a copy counts it as
  reaching past the organization. The probe needs a Workspace account,
  one group at its domain set to allow external members and one that
  is not, a file shared with each, and every field Drive returns for
  both grants and for the account, compared. Until the probe, nothing
  depends on its answer.

The five choices below were decided on 2026-09-05 and are kept
here so they are not reopened.

1. **Sharing.** Google's organization policy is the authority (§7.4).
   `GDRIVE_SHARING` is `all` or `off`; an `internal` mode that copied the
   admin's rules was dropped because the admin console and the Drive
   backend already enforce them per organizational unit, the sharing
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
5. **Go directive.** `go 1.27.3`, the current point release.
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
| Widening access is never by omission | A new file made or uploaded into a folder shared with anyone stays an ordinary write, while a move or a copy of existing content there is refused (§4a) | Decided 2026-10-09 and kept 2026-10-23. `create_file` and `upload_file` put new content, written by the caller, where the caller named: nothing that fewer people could reach before becomes reachable by more, as a post in a space reaches its members. A move or a copy takes existing content, which someone else may have written for fewer people, to where the destination's reach is nowhere in the call |
| `members_only` off is a wide form, its own tool | Still an ordinary `manage_drive` switch | Waiting on the live probe §17 records: whether turning it off restores access already granted to non-members decides whether it is a widening at all |
| Errors use the classes `invalid`, `not_found`, `auth`, `conflict`, `unavailable`, `unsupported` | Fifteen classes, including `ambiguous`, `blocked`, `rate_limited`, `ambiguous_outcome`, `pending` and `stale` | Drive's failures are not the same set. `ambiguous` is the whole addressing design (§4.1), `blocked` is the organization's sharing policy refusing something Google permits in general (§7.4), and `ambiguous_outcome` is a write whose result is unknown. Collapsing them into `invalid` would lose the distinction a model needs to decide what to do next. *(Phase 4 added `pending`: work Google has begun and not finished, which is a long-running download whose operation is still running. It is not `server`, which says something went wrong, and not `rate_limited`, which says you asked too often — nothing is wrong and nothing needs backing off from, and the same call in a minute picks up the finished render. A model told `server` would report a failure that did not happen.)* *(Phase 3: `gates classes` now holds the code to the list, in both directions — a class invented at a call site, and a class listed that nothing emits. It found neither here, which is what a working guard usually finds. A sibling server split its own `ambiguous` the same way after finding it carried both meanings at once, so that word is the one to keep identical across servers: a model must not have to learn what it means twice.)* *(Phase 10: `blocked` also covers this server's own refusal of a wide form in an ordinary tool — a share, a move or a copy past the organization, an outside access request, `domain_users_only` off — each naming the tool that does it behind the destructive flag. Both meanings say "not this way, and no argument here changes that": the organization's policy, or this server's rule.)* *(Phase 10 added `stale`: a wide share's `file_name` or `lift_drive_restriction`'s `drive_name` is not the name Drive has. It is the word the sibling servers use for a witness that does not match; `exists`, which `expect_head_revision` has used since phase 4, says something is already there.)* |

## 17a. Deferred cleanups

Raised by the 3.1.0 review (2026-10-24), and left for the maintainer:

- **A bundle field names tools nothing holds it to.** The
  `enable_destructive`, `sharing`, `labels` and `activity` fields list
  the tools their setting registers, and no check compares the list
  with registration. It would take a registered-tool diff per key, a
  mechanism no sibling has.
- **The live transcript's redactor leaves two things in place.** The
  organization's domain, where the instructions name who `share_file`
  reaches, and, in one of two runs on 2026-10-24, a comment reply id
  in the note `reply_comment` writes on reopening a thread. The
  transcript is redacted so it can be shared, so until both are fixed
  it is read before it is shared, as the run's own last line asks.

Raised by the phase 10 reviews (2026-10-23), and left for the
maintainer:

- **A personal account on an address at another mail provider** (the
  security review's one low finding, 7 of 10). Google also makes
  personal accounts on existing addresses, and `consumerDomains` knows
  only Gmail's. For such an account every address at the same provider
  counts as inside, so accepting an access request, a widening move or
  copy, and turning limited access off go through for them, where 2.2.0
  asked when the client could. §4a and the README say a personal
  account counts every address as outside. Deciding "personal" from
  `about.get`, by `canCreateDrives: false`, would close it and
  over-refuse a Workspace tenant without shared-drive creation, the safe
  direction; an operator setting naming the organization's domain is
  the other way. Either wants a test on all three paths.
- **Narrowing a wide grant**, or giving it an expiry, takes the wide
  tool and so the destructive flag; a default server can only remove
  one with `unshare_file`.
- **The schema diff exempts a tool wholesale**: a tool named on a
  `**Breaking:**` line lets every break of that tool through.
- **`transfer_ownership` carries no mark**, though it cannot be undone.
  It is flag-gated and needs `confirm`, as decided.
- **The simplify pass's design suggestions**: one per-tool descriptor in
  `share()`, and one rule for reaching past the organization shared by
  `shareToolTakes`, `wideGrants` and access requests; the gated tool
  lists derived from registration rather than typed out in the server
  tests, the evals and the staleness gate; one helper for "X does it,
  when GDRIVE_ENABLE_DESTRUCTIVE=true".

Raised by the phase 7 reviews (2026-09-30), and how phase 10 left them:

- **Grants inside the organization are ordinary.** A share to a
  colleague is routine; under injection, a colleague's address is
  still one somebody named. Phase 10 keeps it, now as §4a's rule rather
  than as a question not asked.
- **`update_file`'s download and reshare switches are ordinary.**
  Letting viewers copy a file, editors reshare it, or lifting a
  download restriction is refused with `GDRIVE_SHARING=off`, as the
  drive-level switches are. Turning a folder's limited access off is
  refused when it opens the folder past the organization (§4a).
- ~~Shown, not bound; a question names a file without its folder;
  `GDRIVE_REQUIRE_PROMPT` with a client that cannot ask; the protocol a
  question is sent on.~~ Gone with the question (phase 10).

Raised by the review of the change that asks once per delete
(2026-10-09):

- ~~A client can get neither the mark nor a question.~~ Gone in phase
  10: the mark no longer depends on the client, and there is no
  question.

Raised by the phase-0 review passes and deliberately not done in phase 0.

Raised by the phase-0 review passes and deliberately not done in phase 0.

- ~~No MCP registry entry.~~ **Done, and the thing that blocked it was
  real.** The entry is `packaging/registry/server.json`, `gates registry`
  holds it to the rules, and the release workflow publishes it LAST,
  authenticating with GitHub OIDC — the `io.github.` namespace is what
  that proves.

  This entry said the step "cannot be tested locally at all", and that
  was true only while no release carried a bundle. Both halves are tested
  now. The generated entry was produced from v1.0.1's own signed
  `checksums.txt` and checked; the HEAD the registry makes was made by
  hand against the published URL, which answers **302** to a signed asset
  URL rather than 200. The registry's validator accepts a 3xx carrying a
  Location, explicitly — a check written to expect 200 would reject every
  GitHub release there is, and this one would have been written that way
  from the description alone.

  What was verified rather than adopted: a sibling's account of the rules
  was accurate in every particular, and reading the validator confirmed
  it AND turned up the redirect case nobody had mentioned. §18 has the
  entry. The rules are in code and not in the published schema, which is
  why a document that lints clean can still be rejected at the end of a
  release.

- ~~The live driver's option coverage has never been measured.~~
  **Measured, and gated: 122 of 188.** `gates live-cover` holds every one
  of the binary's published tool options to a decision, in
  `testdata/live-cover.tsv` and in the shape `api-coverage.tsv` already
  uses: an option the driver does not send is `undrivable` from this
  account, with what blocks it, or `undriven`, with what closing it would
  take. Both directions fail — an option nobody decided about, and a row
  for an option the driver has since started sending.

  Ten are undrivable and the reasons repeat: a second person (a reviewer
  swap, an access request, mail to somebody's inbox), an administrator (a
  published label to page through), or a state nothing here may create (a
  file Google has flagged as malware; a scan to run OCR over). The other
  fifty-six are gaps with a recipe, and most are one argument on a call
  that already happens — `search_files.starred` on a run that stars a
  file, `list_folder.include_trashed` on a run that trashes one,
  `page_size: 1` anywhere a token is wanted. They are written down rather
  than closed here, because a step added to the live driver is unverified
  until somebody runs it, and adding sixty of them blind is how a driver
  starts lying.

  **A static gate over the driver's source is the smaller half, and it
  took a sibling repository to say so.** It reads the words, so a step
  that exists and never runs — in a branch that was false, in a list
  nothing passes on — reads exactly like a step that runs. They proved it
  by deleting one call and watching their own gate report full coverage.
  So the driver carries a recorder as well: every call is recorded where
  the calls go out, and the end of every run prints what the run sent,
  what it never called, and any option the SOURCE says it sends that the
  run did not. The first run of it named five, all of them steps behind
  `-file`, and passing `-file` took the list to three. That is the
  mechanism tracking reality rather than asserting it.

  Two things the reader found are worth keeping. `any` is an identifier
  and not an interface node — the alias is resolved by the type checker,
  never by the parser — and a first draft looking for `*ast.InterfaceType`
  read 2 of 188 options and would have reported that as a measurement.
  And a helper that names the tool for its caller (`emptyTrash`, which
  exists so that `empty_trash` is named in exactly one place and always
  scoped) made its `confirm` and `dry_run` look undriven, which would
  have put two lies in the record: they are sent twice on every
  destructive run. The reader follows that indirection now.

- ~~"Never assert an outcome the response did not carry" has no gate.~~
  **Done, and the rule this entry proposed was wrong.** §17a said: fail
  if the field is read in the same function that builds the outcome note.
  Reading the code says otherwise, and thinking about it did not. Two
  CORRECT sites read the field exactly there — the lock sentence branches
  on `in.LockFile` and then calls `lockWords(after.File)`, which reads
  the file back, and the pinning note branches on `in.KeepPreviousRevision`
  and then asks Drive to pin and words the result from what Drive
  answered. The function is the wrong unit. The BRANCH is the right one,
  and what makes a branch honest is that it consults something before it
  speaks.

  So `gates outcomes` fails a branch that tests a boolean the caller
  sent, and then writes prose describing the result, without asking Drive
  anything in between. Two shapes are excluded by the rule itself rather
  than by a list: a dry run, which makes no call by construction and says
  "would" — the honest form of exactly this sentence — and a refusal,
  which says what THIS SERVER did and is true whatever Drive would have
  answered.

  The other half of the design question the entry raised — which fields —
  is answered by deriving rather than choosing. Every boolean field on a
  service input struct is in scope, because a boolean input is the shape
  that asks for a state where a string carries a value, and deriving it
  means a new one is covered on the commit that adds it.

  **It found one, and it is the same defect as `lock_file` with somebody
  else's words in place of a restriction.** `copy_file` said "The comment
  threads were copied with it, so everybody who can see the copy can read
  what was said on the original", from `copy_comments: true` alone.
  `files.copy` answers with a File and mentions comments nowhere, so
  nothing here could know. The live driver has copied with that parameter
  since phase 4 and never once looked at the copy, and §18 recorded the
  overpromise as though it were a feature: "the result says out loud when
  a copy carried somebody else's words somewhere new."

  It now says what Drive was ASKED to do, says outright that Drive does
  not report whether it did, and names `list_comments` on the copy as the
  call that settles it. Reading it back was the other option and was
  rejected for a reason phase 5 paid for: `comments.list` lags a copy, so
  an empty answer would report threads as dropped when they were merely
  late — the `empty_trash` mistake in the opposite direction. The driver
  now lists the copy's comments, so the next live run answers the
  question nobody has asked.

  The test that covered this asserted the wrong sentence, which is the
  phase-4 pattern exactly: a fixture written from the belief keeps the
  belief alive.

  What the gate cannot do is judge the WORDS — the honest form of the
  shape parses identically to the dishonest one. A branch that is right
  anyway carries a row in `testdata/outcome-claims.tsv` with the reason,
  and a row that stops matching fails, so an excuse cannot outlive the
  code it excused. There are two rows.

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
  not report it: `actionWords` falls through an unrecognized create
  sub-kind to "created", and `actorWords` falls through an unrecognized
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

  When an account is available: `livedrive -write -destructive -transfer
  ADDRESS` makes a file for the purpose, records its owner, transfers,
  reads it back and compares. The transfer cannot be undone and Google
  emails the address, so it is an account the maintainer controls. It reports three outcomes, two of which are failures.
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
  `invalidSharingRequest`, or a `domainPolicy`, from an organization's
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

- ~~The live driver's transcript is redacted by habit, not by
  construction.~~ **Done.** `scripts/internal/transcript` is now the only
  way the live driver and the eval harness write anything, and `gates
  transcript` refuses every other route to a terminal: `fmt.Print*`, any
  mention of `os.Stdout` or `os.Stderr`, and the builtins `print` and
  `println`.

  The entry stood for three phases and the estimate in it was right: the
  version with teeth is a rewrite of every print in the program, about
  eighty call sites, and it wanted a moment with nothing else in flight.
  What the estimate got wrong is which programs. The eval harness has the
  same shape — same account, same kinds of thing printed, same `red`
  field used by habit — and a rule covering one of two identical programs
  is a rule waiting to be drifted around. Both go through the one type
  now.

  Three things about the gate are worth keeping, because each was a
  choice that could have gone the other way.

  It forbids a DESTINATION rather than a function. `fmt.Fprintf` into a
  `bytes.Buffer` is how an eval task builds its fixture and reaches
  nobody; a gate that banned the function would have needed an exception
  for it on day one, and an allowlist is how a gate stops being believed.
  A terminal is `os.Stdout` and `os.Stderr`, and `fmt.Print*` is a write
  to `os.Stdout` with the name left out, so naming those three names
  every way out.

  It refuses a HOLLOW exemption. Every other check here is about WHERE
  printing happens and says nothing about what is printed: moving each
  `fmt.Println` into a passthrough helper would satisfy all of them and
  leak exactly as much. So the exempt package must pass what it writes
  through the redactor, and what comes out of it is asserted in a test
  rather than argued for here.

  It redacts the whole formatted LINE, not the arguments somebody
  remembered to wrap. That is what makes the property structural instead
  of careful: an address arriving through a format string nobody thought
  about is redacted by the same code that redacts one somebody did. It
  also removed about fifteen hand-written `red.Do` calls at print sites,
  which were the habit this entry was about.

  The line that had never been redacted at all was the run's own failure.
  It is printed after the session, from `main`, and it carries whatever
  the error carried — a file name, an address, a path. Six ways to a
  terminal are watched being refused in tests, and the failure line is
  one of the three addresses `TestEveryLinePrintedIsRedacted` sends
  through.

- ~~Whether `copy_comments` works for a Google-native document is
  unknown.~~ **Answered, and then made a rule.** The Doc's threads came
  across; the uploaded CSV's did not. That was two points and an obvious
  reading, and this entry said what would settle it: a third kind that is
  Drive's own format and not a Doc. A Google Sheet carried its threads
  too, so the reading holds — Drive's own formats yes, uploaded bytes no
  (§18).

  The driver copies all three kinds every run and says which carried,
  because a fact that has already changed once is one to re-check rather
  than remember.
- ~~The outcome gate subtracts rather than selects.~~ **Done.** It asks
  the positive question now — does this literal reach what the caller
  reads — and the answer is three shapes, because `internal/render`
  writes every result there is: appended to a `notes` slice, assigned to
  a note field, or set as `Note:` in an outcome literal. `Errorf` prose
  and `slog` prose are excluded by construction rather than by name, the
  25-character heuristic is gone, and `isRefusalOrLog` went from two
  callers to one narrow use.

  A test caught the first draft missing the third shape, which is the
  commonest one in the package. The entry predicted this would be "real
  work against a gate that is currently correct on every branch"; it was
  an hour, and the gate is correct on every branch for a reason now
  rather than by three independent adjustments agreeing.

- ~~`gates mcpb` stages four binaries and nothing holds that list to
  `.goreleaser.yaml`.~~ **Done.** `checkBuildMatrix` reads the `goos`
  matrix and fails when the bundle stages nothing from a platform
  goreleaser builds. Watched failing by adding one.

  The asymmetry the entry named is why it was worth doing: a platform
  REMOVED from the matrix fails loudly at pack time when the glob finds
  nothing, and a platform ADDED is simply absent from the bundle, in
  silence, for as long as nobody looks.

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
  behavior, which is how the claim survived a phase.

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
| Two resource templates sharing a prefix shadow each other | **Refuted for go-sdk v1.7.0 with uritemplate v3.0.2**, and checked rather than reasoned about: `Regexp()` anchors with `^` and `$`, and a simple expansion's character class excludes `/`. `gdrive://{file}` cannot match `gdrive://x/meta` | The three templates are registered as they are. A reserved expansion (`{+file}`) *would* shadow and is not used; the price is that a path or a URL must arrive percent-encoded, which a test records as behavior rather than a footnote |
| `get_file` costs at most two calls (§11, since phase 0) | **Refuted by measurement.** Two holds for a file at the top of My Drive. Three folders down it is four: the file, and one read per folder above it, because the card's location line is a climb | The target is restated as the code behaves and asserted in a test, including that the second call costs nothing at all. The code did not change: a card that says where a file is only sometimes is worse than one that costs a read per level once |
| A search page's parent lookups are one, because they are cached (§11) | **Refuted by measurement.** A hundred hits in one folder three levels down cost three reads, not one and not a hundred: the cache is per folder, and the chain above the hits has three of them | The target names the chain. Still well inside the budget of twenty that the number was protecting |
| A wall-clock target can be asserted in `make check` | **Refuted.** 10 000 items render in 4.7 ms uninstrumented and over 60 ms under the race detector with coverage counters, which is what `make check` runs | The test asserts linearity — ten times the items within twenty times the work — plus a backstop far above anything instrumentation explains. The millisecond figure lives in the benchmark, where nothing is instrumented |
| A benchmark's fixture shape does not matter, only its size | **Refuted, by getting it wrong.** The first 10 000-item tree made one folder per folder, so it was a thousand levels deep — a shape Drive would never return — and measured the cost of the indent string, at 226 MB allocated per render. A realistic shape is 6.8 MB | The fixture branches three ways, which puts 10 000 items about eight levels down, inside the depth a walk will go to |
| A gate that runs on one platform of a three-platform matrix is enough | **Refuted** (reported by a sibling Go MCP server, checked here). The coverage floor ran under `if: runner.os == 'Linux'`, so a test skipped on Windows cost coverage nobody could measure | It runs on all three. One of the two Windows skips here turned out to be unnecessary — `os.UserConfigDir` reads `%AppData%` there, so clearing that is the same experiment — and now runs everywhere |
| Every `invalidSharingRequest` is the organization's policy refusing (§7.4's mapping since phase 2) | **Refuted, live, and it was the wrong instruction rather than the wrong word.** Sharing with an address that has no Google account behind it answers **400** `invalidSharingRequest` — "you must check the Notify people box to invite this recipient". Google uses the reason for a policy refusal AND for a request the caller can simply fix, and the status is what tells them apart | `invalidSharingRequest` maps to `blocked` only at 403; at 400 it is `invalid`, which keeps Google's own message and with it the fix. `[blocked]` says give up and names an administrator; `[invalid]` says fix it and try again. The mapping had been built from documented reasons and had never been shown a real one — which is the whole argument for the check that found it |
| A driver that reads the CLASS of a refusal has verified the classification | **Refuted by its own first run.** The `-blocked` check compared the class against `blocked`, saw `blocked`, and printed "the mapping holds" for a refusal that was not a policy refusal at all | The verdict now prints Google's message and says to read it, and names both readings when the class is not `blocked` — the reason may be missing from the mapping, or the address may simply not be policy-refused. A check whose pass condition is one field of the thing it is checking can be fooled by that field |
| The reference listing a field means the field can be requested | **Refuted, live.** `Comment.assigneeEmailAddress` is in the discovery document and is a real field; Drive answers 400 "Invalid field selection assignee_email_address" when a `fields` expression names it, so asking fails the whole call. Every `add_comment` failed on the first live run | The field is out of the request, the model and the renderer, and a note sits where it was in the wire types, because the next person to read the reference will want to add it back. Neither the fake nor the discovery document could have caught this: the first answered the field happily, and the second is the source that says it exists |
| A live run that reports "all calls behaved as expected" has verified the tool surface | **Refuted, and it is the most useful thing phase 2 learned.** Two runs said exactly that while three results were wrong: a file card reporting `sharing: private to you` in the same result whose change line said the file was now public, a removal reporting "shared, but no grants are visible" instead of "private to you", and every My Drive file blaming an inherited grant on a shared drive it had never been near. The driver checks whether a call *succeeded*, not whether it *told the truth*, and those are different questions | The transcript is read, not just its verdict. Phase 3's evals are the mechanized version of this: a result that is wrong while succeeding is the class of defect no status code catches |
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
| The Drive API has no preview-only features this server would want (my assumption) | Confirmed for everything in §8: files, permissions, drives, revisions, comments, changes, access proposals, labels and approvals are GA methods in the v3 reference | No preview enrollment in the setup guide; an enrolled project is needed only for spike B, which is skipped without one |
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
| A display name is capitalized, so a capital is what tells it from prose (the redactor, phases 1-3) | **Refuted live in phase 4.** A Workspace account with no display name set shows the address's local part instead — lowercase, dotted — and it survived EVERY position the redactor knows, because the shape refused it before the position was consulted. The transcript carried the maintainer's own name throughout | A dotted lowercase token is a name shape too, safe only because the positions are anchored. The fixtures were the other half of the problem: every invented name in them was capitalized, so the tests agreed with the bug |
| The redactor's positions find every person and id a live transcript carries (phases 1-6) | **Refuted live 2026-10-09.** A coworker's display name, as lowercase initials, sat beside an address the redactor hid; a file called `link.txt` after "shared by" became a person; the account was two people because approvals give it another display name; Drive's comment and reply ids, eleven characters, were too short for the id pattern; and the names of the organization's drives, customers and contracts, from the read steps that look at the whole Drive, were printed as they are | A name beside an address or before "(you)" is found by that anchor, in any case and shape, and every "(you)" is one placeholder. "by Name" is a person only after a time. Comment and reply ids are hidden where they are printed and then everywhere else. The live driver hides the names of files, folders and drives outside its scratch folder in rows, paths, card heads and drive lists; the summary says a name anywhere else is not hidden |
| Labels are a Drive API feature | Refined: applied through the Drive API, defined through the separate Drive Labels API with its own scopes | Phase 4 with `GDRIVE_LABELS` |
| Reading the labels on a file needs the labels scopes (§10 as written) | **Refuted in phase 4** against the discovery document: `files.listLabels` and `files.modifyLabels` list only `drive`, `drive.file` and `drive.metadata`. The labels scopes belong to the Labels API, which defines labels; Drive alone reads and writes the values ON a file | `GDRIVE_LABELS` still governs both tools, because applying a label needs a label id and a field id and those live in the definitions. But the reason is now stated as it is, and `manage_labels` still applies and removes when the definitions are out of reach — only `set_field` is refused there, since the setter depends on the field's type |
| `includeLabels` asks Drive for a file's labels (what phase 1 shipped) | **Refuted in phase 4, twice over.** The discovery document says it is "a comma-separated list of IDs of labels to include"; a live probe answers `includeLabels=*` with 400 `badRequest`. `get_file` under `GDRIVE_LABELS=true` had therefore never worked since phase 1, and nothing noticed: the flag is off by default and no test could see it, because the fake accepted anything | `files.listLabels` is the method for "which labels are on this file"; `GetFileOptions.IncludeLabelIDs` takes ids when a caller has them. `drivetest` refuses a wildcard now, with Drive's own status, so the case a test could not see is a case a test now fails on |
| `LabelField`'s date member is `date` (what phase 1's wire type said) | **Refuted in phase 4** against the discovery document: it is `dateString`, an RFC 3339 full-date. The wrong tag decoded every date-valued field to nothing — silently, an absent member being indistinguishable from an unset one | Tag corrected, and a round-trip test asserts a date survives rather than describing the tag |
| Access requests can be created through the API | Refuted: listed and resolved only | §7.8 |
| Approvals are part of Drive v3 (my assumption: no) | Confirmed present in the v3 reference (`approvals.*`), edition and behavior unverified | Phase 4, verified live first |
| `approvals.list` returns the approvals (the obvious reading) | **Refuted in phase 4** by the discovery document's own words: "By default, this method returns a minimal response that may not include the items array. To retrieve approval details, you must explicitly specify the fields you want." The same shape as the comment endpoints, which cost phase 3 a live run to find | `ApprovalFields` is sent on every call, and `drivetest` refuses a fields-less request. Reading the document first is what turned a live-run defect into a line of code |
| Starting an approval only asks people to look at a file | **Refined in phase 4** from the schemas: `StartApprovalRequest.lockFile` locks the content for the duration, and the default `fileContentChangeBehavior` of `RESET_APPROVAL` locks the file once it is approved. Every verb also mails somebody, with no notify flag anywhere | Said in `manage_approval`'s description and in every result. The live driver deliberately cancels rather than approves: an approved file is locked, and a scratch folder holding something the driver cannot clean up would break its own contract |
| Drive Activity is in Drive v3 | Refuted: a separate API (`driveactivity.googleapis.com`, v2) with its own scopes; its overview page returned 404 during this check | Phase 4, verified before design |
| Drive Activity's 404 in phase 0 meant something was wrong with it | **Refuted in phase 4**: the discovery document is there, the API is GA, and it has exactly one method (`activity.query`). A documentation page that 404s says nothing about an API | `list_activity`, behind `GDRIVE_ACTIVITY` |
| Google's search guide documents how to find a file by a custom property | **Refuted live in phase 4.** The guide gives `properties has { key='department' }` as its own example of matching a key whatever the value, and Drive answers it 400 `invalid` "Invalid Value" — twice in each of three spellings, and for `appProperties` too. Only `key` AND `value` works | `search_files property:` requires both halves and says why; `drivetest` refuses the key-only form as Drive does; the live driver checks the refusal, so a day when Drive starts accepting it shows up as this server being needlessly strict rather than never showing up |
| An empty property search means the query is wrong | **Withdrawn 2026-10-09.** Phase 4 found the file a run had just tagged still absent after 30 s, a direct query minutes later found it, and this row called Drive's property index slower than the changes feed. That run had moved the file into a folder inside the scratch folder and searched only the scratch folder's direct children, so the search could not find it at any speed; the direct query found it because it searched everywhere. The first 2026-10-09 run did the same, and its visibility searches too. **Measured by the second 2026-10-09 run**, searching under the scratch folder on every page: the first search after each change found the file, with no wait. That was a property set on it, a link grant added (searched as `link` and `anyone`) and the grant removed (searched as `limited`). One run is not a bound | The driver searches under the scratch folder, reads every page, and checks for the file's id. It still reports UNVERIFIED rather than failing when 30 s go by without it, since nothing here knows how slow the index can be |
| Every activity says what happened | Refuted live in phase 4: some activities carry no action. **The shape was recorded wrongly and corrected after v0.4.0** — see the row below | `list_activity` counts and reports those separately from an action kind it has no words for — which would mean Google had added a thirteenth, and IS worth acting on. Reporting the first as the second sends a reader hunting a case that is not missing |
| An activity with no action arrives with `primaryActionDetail` **missing** (phase 4, from a live probe of 400) | **Refuted live after v0.4.0.** A fresh probe of 400 activities finds that shape **zero** times: Drive sends `primaryActionDetail: {}`, ten times in 400. Phase 4 saw the right entries and wrote down the wrong shape — a probe that asks whether the member is *falsy* cannot tell an absent object from an empty one, and `{}` is falsy in most languages a probe gets written in. The code was then built to the record rather than to the response, so the branch meant to catch the ordinary case never ran, and every one of those entries was reported as a thirteenth kind Google had added: the exact confusion the phase-4 split was written to end | `ActionDetail` keeps the member names it decoded, because an empty object and a member no field covers are otherwise the same Go value. `NewActivity` decides on the names; the fixtures are JSON the decoder reads rather than struct literals, since a literal cannot express the difference. On a real account the false alarms go from ten to none, and a kind Drive really grows is named rather than counted |
| A refutation is worth trusting once it has been checked live | **Refined after v0.4.0**, and this is the third time §18 has bent this way. Phase 4 learned that a parameter list read once has a date on it. This one has a SHAPE on it: the live observation was real and the record of it was not, and a note in this table is what the next phase builds against. What makes the difference is keeping the response — the bytes, or a decoder run over them — rather than a sentence about the response | The fixtures for both branches are now JSON, so the test is written in the same language as the evidence. Where a probe settles a question, the probe's own classification is quoted in the row |
| Approvals exist in the API but the edition is unverified (phase 4, from the discovery document) | **Confirmed live**: start, list, comment and cancel all work on this Workspace edition, and the file-content-change behavior comes back `RESET_APPROVAL` as the schema says | The approved state and `lock_file` are still unrun, deliberately — §17a |
| A shared drive can be addressed by its id as soon as it exists | **Refuted live after v0.4.0.** `drives.list` is eventually consistent and `drives.get` is not, so a drive created moments ago answers by id and is absent from the listing for minutes. Everything taking a `drive` argument resolved through the listing alone, so the driver made a scratch drive, wrote files into it by that id, and was then told there was no drive of that NAME — with a list of unrelated drive names attached — when it tried to empty its trash and delete it. It stranded a shared drive in a real Workspace twice | `findDrive` falls back to `drives.get` when the listing has no match, which costs nothing on the paths that already worked: a name can only be answered by the listing, and an id that is in the listing is answered before the fallback. The refusal says both readings were tried. `drivetest.UnlistedDrives` is the fake's version of the lag |
| `files.emptyTrash` empties the trash | **Refined live after v0.4.0.** The method has NO response — the reference gives it none — so nothing can be read back about what went. A run trashed a file, emptied that shared drive's trash, and restored the same file on the very next call; the dry run before it counted the trash as holding 0 items while that file was in it | The result says Drive accepted the call, says the method reports nothing at all, and says the view it works from lags. It had been claiming "is empty. Everything that was in it is gone for good", which is the strongest claim in the whole surface and the one thing this call cannot know |
| `lockFile` on an approval locks the file | **Refuted live after v0.4.0**, on two runs: Drive applied no content restriction, the card showed the file fully editable, and `update_content` succeeded. APPROVING is what locks it — that half is confirmed, with the restriction reading "Locked for File Approval" and the next content change refused for violating it | The sentence is read off the file rather than written from the argument, so it is right in both worlds. The fake locked on `lockFile` too, which is how the claim survived a phase: it was built from the reference and agreed with the belief |
| A content restriction stops a file being removed | Refuted from the reference before the run that depended on it: `readOnly` stops a new revision, a comment, and the title — and says nothing about trashing or deletion, and `files.delete` takes no parameter a restriction could refuse. Confirmed live: an approved, locked file was permanently deleted | Checked BEFORE designing the destructive driver, because the answer decided whether an approved file could strand a scratch shared drive. `drives.delete`'s `allowItemDeletion` needs `useDomainAdminAccess`, which this server does not offer, so a drive must be emptied before it can go |
| `revisions.get` agrees with `revisions.list` | **Refuted live after v0.4.0**: a revision `list_revisions` had just shown answered 404 from `revisions.get` seconds later, and the delete that followed — same path, same call — succeeded. Drive's revision endpoint lags a write | The refusal says "or not yet" and tells the caller to try again, instead of explaining that the revision must have expired |
| Drive's eventual consistency is a property of the changes feed | **Refuted, cumulatively.** Five measured surfaces now: the changes feed (phase 2), and after v0.4.0 `drives.list` after a create, a shared drive's trash count, the trash itself surviving `emptyTrash`, and `revisions.get` after a write. It is a property of Drive, not of one endpoint. The property index was counted here until 2026-10-09 and is withdrawn: the search that measured it could not find the file (the property row above) | The rule this repository now works to: a message must never assert an outcome the response did not carry. Every one of the findings after v0.4.0 — including the `list_activity` false alarm and `lock_file` — is the same mistake, which is that a sentence was built from the REQUEST |
| A refused approval is a permissions problem | **Refined live after v0.4.0**: answering an approval that is already finished is refused with the same bare `Permission denied` as answering one you are not a reviewer of. Drive does not distinguish them, so the message cannot either — it has to offer the whole set. It named two of three and left out the only cause the caller can have produced itself, so the live run, having just canceled the approval it then answered, was told to check a reviewer list the account was already on | The finished case is named first and the message points at `list_approvals`, which is the one call that says which of the three it is |
| Drive Activity says who did something | **Refuted in phase 4** from the schemas: an actor is a `KnownUser` carrying a People API resource name (`people/123456`) and an `isCurrentUser` flag, and nothing else. No display name, no address. The permissions inside a `PermissionChange` carry no address either | `list_activity` says "you" or "somebody else" and prints a line saying why it cannot say more. Resolving the name would be a third API and a third scope for a decoration, and the id itself never reaches the output |
| go-sdk latest is v1.7.0 | Confirmed (proxy, 2026-07-27); v1.8.0-pre.2 tagged 2026-09-04 hardens bounds and adds `SupportedProtocolVersions`; protocol `2026-07-28` supported | Pin v1.7.0; smoke tests two protocol versions |
| Go latest is 1.27.x | Confirmed: 1.27.1 is current; 1.27 brings generic methods, `encoding/json/v2` behind `encoding/json`, a `uuid` package (useful for `requestId`) | §17 item 5 |
| Read tools return text only (convention) | Confirmed by observation of Claude Code 2.1 (2026-09-03): when a result carries both a text block and `structuredContent`, the model is shown only the structured form, so a read returning both looked like metadata; the spec makes the text block the backwards-compatible form | Kept; write tools return both, and their JSON carries everything the text does |
| `[class] message` errors, flat schemas, snake_case names, server-side gating, keep serving without credentials, keyring → file → env, loopback PKCE, env-first config with bound flags (convention) | Confirmed against primary sources: the MCP spec requires only `isError` and treats annotations as untrusted, so gates must be server-side; `gh` falls back from the keyring to a file; Google's native-app guide requires loopback with PKCE (OOB blocked since 2023); Claude Code, Claude Desktop and Cursor pass only `command`, `args` and `env`; a server that exits at startup shows the person "failed to connect" and the model never learns why | Kept unchanged |
| Duplicate names are the model's problem (my first thought) | Changed in review of the OSS failures: a lost result creates the same folder twice and nothing refuses it | The guard in §7.3, pending §17 item 2 |
| Bulk operations belong in the tool surface | Rejected: one item per call keeps every removal and every share a visible approval; a folder is the unit of bulk work | §8 |
| The server should carry its own "internal only" sharing mode (my first design) | Refuted in review: the Workspace admin's external-sharing setting (off, allowlisted domains, on; per organizational unit) is enforced by Google on the API, the sharing guide's instruction to apps is to check `capabilities.canShare` before offering the action, and the MCP specification asks servers for "appropriate access controls" while putting consent at the host | `GDRIVE_SHARING` is `all` or `off`; `share_file` checks `canShare` first; policy refusals map to `[blocked]` |
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
| The MCP registry's MCPB rules are in its published schema, so a document that validates will be accepted | **Refuted against the registry's own validator** (`internal/validators/registries/mcpb.go`, read 2026-09-06). None of them is in the schema. The identifier must be HTTPS, must be `github.com` or `gitlab.com`, must be shaped like a release asset, must contain "mcp" somewhere case-insensitively, must not sit beside a `registryBaseUrl`, and must answer a **HEAD** — 200, or a 3xx carrying a `Location`. `fileSha256` is required and never verified by the registry; its own documentation says so, and clients check it before installing. A sibling's account of these was accurate in every particular; reading the code confirmed it and added the redirect case, which nobody had mentioned and which decides the outcome: GitHub answers a release-asset HEAD with 302, so a check written to expect 200 would reject every GitHub release there is | `gates registry` enforces all of them on the committed entry, each watched failing. The HEAD was made by hand against the published v1.0.1 URL rather than reasoned about, which is what turned "cannot be tested locally at all" into two things that are tested |
| A gate over the registry entry should check the rules the schema does not carry (this repository, an hour earlier) | **Half a rule, and the missing half cost the first publish.** Reading the validator was right and finding the rules absent from the schema was right; aiming ONLY at those was not. The first entry refused was `body.description: expected length <= 100` against a 205-character description the gate had just passed, and the refusal arrived AFTER the OIDC login succeeded — so everything that could have failed for a boring reason had already worked. The schema carries `description` and `title` at 1..100, `name` at 3..200 with pattern `^[a-zA-Z0-9.-]+/[a-zA-Z0-9._-]+$`, `version` up to 255, and `fileSha256` as `^[a-f0-9]{64}$` (schema `2025-12-11`, read 2026-09-07) | Both sets are enforced, each watched failing. The limits are transcribed with their source and date rather than fetched, because a gate that reaches the network fails when somebody else's CDN is slow. The lesson generalizes past this file: two documents describe one contract, and checking the one somebody told you about is not checking the contract |
| `copy_file` with `copy_comments: true` brings the threads with it (the note this server printed, phases 4-6) | **A rule, on three data points.** A Google Doc's threads came across and a Google Sheet's did too; an uploaded CSV's did not, on the same runs with the same argument. The CSV was checked twice minutes apart with `include_deleted: true` — `0 comment threads` / `no comments: nobody has commented on this file, and none has been deleted either` — so it is not `comments.list` lagging, which was the other explanation and the one the code had been written to allow for. Two points made this an inference; the Sheet is Drive's own format that is not a Doc, so it either agreed with the reading or refuted it, and it agreed. **Confirmed live 2026-10-09** (second run): a Doc's and a Sheet's threads came across with `copy_comments`, and a copy of each made without it carried none, so the flag is what brought them. A CSV's copy carried none, a second sighting | Drive's own formats carry comments and uploaded bytes do not. The note and the SCHEMA say Drive does not always do it and name the kinds actually seen; the schema matters more, because it is read before the call. The driver copies all three kinds every run and says which carried, so a fact that has already changed once is re-checked rather than remembered. For each kind that carried, it also makes a copy without `copy_comments`: only that copy coming out empty shows the flag made the difference |
| `files.copy` can bring the comments with it (§7.3 as written) | **Refuted in phase 1** against the v3 reference — and the refutation was itself **refuted in phase 4** against the discovery document, which lists `copyComments` on `files.copy` with a default of `false`. Whether Google added it since or the phase-1 check read the reference page rather than the document cannot be told from here, and the difference does not matter: the lesson is that a parameter list read once is a fact with a date on it | `copy_comments` is back on `copy_file`, off by default. The result said out loud "when a copy carried somebody else's words somewhere new" — which was this overpromise written down as a feature: files.copy answers with a File and mentions comments nowhere, so nothing could know it had. `gates outcomes` found it. The result now says what Drive was ASKED to do and names list_comments on the copy as the call that settles it This is the argument for re-reading the discovery document every phase rather than trusting §18 |
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
| The bundle manifest's `$schema` and `manifest_version` are right because they are present | Refuted, 2026-09-17, against the published schemas. The gate required both keys to EXIST and read neither, so `$schema` could have said anything: it named `main`, a branch upstream can amend under a document that claims to conform to it — the path pins the FORMAT, the ref pins the BYTES. Fetched `mcpb-manifest-v0.2`, `v0.3` and `v0.4`, which are served, and `v0.5`, which is not; the copy at tag `v2.1.2` is byte-identical to `main` today, which is the argument for the tag rather than against it. 0.4 is not adopted: its only difference from 0.3 is a `uv` value in the `server.type` enum, and this bundle's type is `binary`. | `checkManifest` holds the declaration as well as the staged tree: upstream's path at a full release tag or a commit SHA — an allow-list, because refusing branch NAMES passes a partial tag like `v2.1` — the version agreeing with the URL, a floor under it, and a support URL. The floor is the one the others cannot make: 0.2 beside a 0.2 schema is stale and self-consistent |

**Phase 7 additions (2026-09-30).** Asking the person, and what the
live run of it found.

| Convention | Verdict | Effect |
|---|---|---|
| `drives.update` replaces the whole `restrictions` object, so a patch re-sends the switches it keeps (§7.5 as written, and the fake) | **Refuted by a live probe, 2026-09-30,** on a shared drive the probe made and deleted. A body with only `copyRequiresWriterPermission: true` changed that switch and kept the others; a body that left a switch out kept it; only an explicit `false` turned one off. A body carrying `downloadRestriction` — `{}`, or the values Drive already held — was refused, 400 `badRequest`, "Bad Request". Turning `copyRequiresWriterPermission` on set `downloadRestriction.restrictedForReaders` too, and turning it off cleared it | The patch is `DriveRestrictionsPatch`: pointer switches, only the ones that change, no `downloadRestriction`. The result reads the change from the response and names a switch Drive did not apply. The fake merges and refuses the same way. `manage_drive restrict` had failed on every call since 1.1.0 and had never turned a switch off |
| An unset `destructiveHint` means not destructive | **Refuted.** The specification's `ToolAnnotations` in `schema/2026-07-28/schema.ts`, read 2026-09-30: "Default: true", and `false` means "the tool performs only additive updates" | Every write states the hint, and a test fails on one that does not. Trash, move, restore, rename, unshare and the drive settings say `false`: each can be set back, and a client that confirms on the hint would otherwise ask as it does for a permanent delete. That is a reading, not the letter, which calls any non-additive update destructive. `update_content` says `true`: the old content survives only as a revision Drive may purge *(Superseded 2026-10-31: next row.)* |
| A write that can be set back may say `destructiveHint: false` | **Refuted 2026-10-31**, by the letter of the row above: the MCP schema (2025-11-25), `ToolAnnotations`, "If false, the tool performs only additive updates". Trashing, moving, restoring, renaming, unsharing, a drive's settings, a grant's role, an approval's reviewers and a pending access request all change what is there | Every write says true but the eight that add: `create_file`, `create_folder`, `create_shortcut`, `upload_file`, `copy_file`, `add_comment`, `download_file`, and `extract_text`, which deletes only the temporary copy it made. The nine that flip stay ungated, with no confirm. `TestOnlyToolsThatAddSayTheyAreNotDestructive` holds it |
| Google writes a time it does not have as the Unix epoch | Seen live on 2026-10-31 from Google Chat's API: an administrator's read of a space answered `1970-01-01T00:00:00Z` for a time it lacked. Not seen yet from Drive, but nothing in Drive dates from 1970, so reading the epoch as no time loses nothing | `parseTime` reads the epoch as no time through `KnownTime`, which the times shown as written go through too: a grant's expiry, and a file's time in an `[ambiguous]` list. Whether a file was shared with you still reads the field's presence, not its time |
| A scope must be on the consent screen to be granted | **Refined 2026-10-31.** Google's configure-oauth-consent guide (updated 2026-09-03): "you only need to list scopes for apps used by people outside your Google Workspace organization", and for internal apps "scopes aren't listed on the consent screen". Confirmed live the same day on an Internal app, whose token held scopes its Data Access page does not list | The README, the configuration guide and the labels and activity refusal ask for the scope list on External apps only |
| `readOnlyHint` is about Drive | **Refuted**, same source: "the tool does not modify its environment" | `download_file` writes a local file, so it is not read-only; read-only mode still registers it, since it changes nothing in Drive |
| Drive keeps a replaced file's old content | **Refined.** The revisions overview (<https://developers.google.com/workspace/drive/api/guides/change-overview>), read 2026-09-30: a blob revision not kept forever "is purgeable", "typically preserved for 30 days", and purged sooner once a file has 100 such revisions | `update_content`'s description and `keep_previous_revision` say so |
| The Go SDK's typed handler can return a question | **Confirmed** in MCP Go SDK v1.8.0, `mcp/server.go`, read 2026-09-30: when a handler's result carries `InputRequests`, the output is not marshaled or validated against the output schema | The asking tools keep their typed handlers and output schema *(Superseded 2026-10-23: the server asks nobody, §4a.)* |
| A form elicitation must ask for at least one field | **Refuted.** `ElicitRequestFormParams` in the specification's `schema.ts` for 2025-06-18, 2025-11-25 and 2026-07-28, read 2026-09-28: `properties` is an open map with no minimum, and `required` is optional | The question's form is `{"type":"object","properties":{}}`; accepting it is the answer (§4a) *(Superseded 2026-10-23: the server asks nobody, §4a.)* |
| A client that declares elicitation has a person to answer it | **Refuted.** `claude -p` 2.1.284 against a throwaway probe server, 2026-09-28, answered `cancel` within milliseconds on both 2025-11-25 and 2026-07-28 | An unattended client that declares elicitation cannot make the asking writes; the refusal says "not confirmed by the person" and never that the person declined *(Superseded 2026-10-23: the server asks nobody, §4a.)* |
| An `accept` comes from a person | **Refuted.** Claude Code's MCP documentation (<https://code.claude.com/docs/en/mcp>), read 2026-09-28: an `Elicitation` hook can answer without a dialog. Codex (`codex-rs/codex-mcp/src/elicitation.rs`, read 2026-09-29) accepts a form with no properties itself under approval policy `never` with full access, and a VS Code question the person skips resolves as `accept` | Recorded as a limit of the empty form. A required choice would stop both, and was found slower and less clear than Accept in the maintainer's check in Claude Code, 2026-09-29 *(This is part of why the server no longer asks, 2026-10-23, §4a.)* |
| A client draws a question as plain text | **Refuted.** VS Code's `mcpElicitationService.ts`, read 2026-09-29, builds a form question as an untrusted `MarkdownString` | Every value from Drive or the call stands in a code span with its backticks and quote marks folded, and links broken *(Superseded 2026-10-23: the server asks nobody, §4a.)* |
| `\b` in a Go regular expression is a word boundary in any script | **Refuted.** `go doc regexp/syntax`, Go 1.27.1: `\b` is "at ASCII word boundary" | The link shapes in a question are unanchored, so a link after an underscore or in a non-Latin domain is broken too; a test holds both *(Superseded 2026-10-23: the server asks nobody, §4a.)* |
| A destructive tool should carry both `requiresUserInteraction` and the server's own question | **Refuted** 2026-10-09, after the owner was asked twice for one delete in another server built the same way. No source recommends two hard gates for one call: the spec puts confirmation on the client, GitHub's `delete_repository` and Supabase confirm with `destructiveHint` plus a form elicitation and set no mark, and Claude Code's documentation scopes the mark to "tools whose permission prompt is itself the point" | The mark is sent per client, present only when the request declares no form elicitation, on the five tools that ask before every write (`askedEveryCall`). A Claude Code `Elicitation` hook that accepts now confirms these deletes alone, where the mark used to stop the call before it reached the server. A typed confirmation, which would stop Codex accepting an empty form unseen, was offered and not chosen *(Corrected 2026-10-23: there is no question, and the mark no longer depends on the client. It is on `delete_file`, `empty_trash`, `delete_drive` and `share_by_link`, for every client, and came off `delete_revision` and `delete_comment` (§4a).)* |

**Gap-analysis additions (2026-10-09).** Three `-write` live runs the
same day settled the rows marked "live 2026-10-09". The second made the
checks the first could not: the visibility and property searches under
the scratch folder on every page, `extract_text`'s temporary copies read
by id, ranged reads of a workbook and a deck, `restrict_download:
viewers`, a copy without `copy_comments` beside each one that carried
its threads, and the status of the query Drive refused. The third ran on
the code released as v2.1.0. It saw the same again and settled three
more rows: the indexable-text flag, the checksum of a download by the
current revision's id, and the flag the coverage report names for each
option a run did not send (the last three rows). What is still owed,
for the next run:

- The modes none of the three passed: `-share`, `-drive`, `-destructive`,
  `-unlistable`, `-labels` and `-activity`. `list_drives` and
  `manage_drive` were not called. The `readers` search waits for
  `-share`.
- One check by hand: what a Form's card says about its responders,
  against `files.get` with `includePermissionsForView=published` (the
  Form row). In all three runs a Form's card read "private to you"
  while the link searches returned it.
- Moving a folder that has limited access: the move prediction ignores
  the folder's own setting, a belief its row records as unverified, and
  no live step makes that move yet.
- Cleanup by hand: each run leaves its scratch folder in the trash,
  which held nearly all of the account's Drive usage from earlier runs,
  and approvals mail the account.

| Convention | Verdict | Effect |
|---|---|---|
| CI's checkout has no tags, so a schema diff against the last tag compares against nothing there | **Refuted for this repository.** The `smoke` job checks out with `fetch-depth: 0`, which `actions/checkout`'s `action.yml` at the pinned v7.0.1 SHA describes as "all history for all branches and tags". The tag was there, and the diff built it | The diff still moved to a committed baseline, for what the tag could not do: it compared top-level inputs only, so a lost output field or a field deep in an object passed; it rebuilt old source on every run; and a deliberate break could not pass before its own tag existed |
| `capabilities.canListChildren` false means a listing of the folder is refused (unstated) | **Unverified.** The `files` reference, read 2026-10-09, says only "Whether the current user can list the children of this folder." Whether `'id' in parents` then answers with nothing, with the items shared with the account directly, or with an error is not documented | `list_folder` lists anyway and says the folder cannot be listed, so either answer reads correctly; a recursive copy refuses. The live driver's `-unlistable REF` step settles which answer Drive gives |
| `sharedWithMeTime` orders the files shared with this account (convention) | **Confirmed** against the `files.list` reference, read 2026-10-09: `orderBy` lists `sharedWithMeTime`, "When the file was shared with the user, if applicable". The `files` reference says the same of the field and calls `sharingUser` "The user who shared the file with the requesting user, if applicable". Where a file without the time sorts is not documented. **Confirmed live 2026-10-09**: a search with `order_by: shared` listed five shares newest first | `order_by: shared` sends `sharedWithMeTime desc` only beside `sharedWithMe = true`, so every hit has the time. Both fields are in the list mask, so a row says who shared a file and when, and a shared file whose folder is out of reach reads as "Shared with me" in a search, not as having no folder |
| `visibility` takes the five values the search-terms guide lists (convention) | **Confirmed** against <https://developers.google.com/workspace/drive/api/guides/ref-search-terms>, read 2026-10-09: operators `=` and `!=`, values `anyoneCanFind`, `anyoneWithLink`, `domainCanFind`, `domainWithLink` and `limited`. The search guide says `limited` is "private, or shared with specific users or groups" | The fake refuses any other value or operator. Which value a file with both an anyone and a domain grant reports is not documented; the fake assumes the wider. The live driver searches `link`, `anyone` and `limited` around its link grant |
| `readers` holds the people who can edit (unstated) | **Unverified.** The search-terms guide says `readers` are "Users or groups who have permission to read the file" and `writers` "have permission to modify the file", and nothing more | `shared_with` asks `readers` or `writers`, which is right either way. The fake takes the narrow reading: `readers` is reader and commenter, `writers` is writer, organizer and owner. With `-share`, the live driver asks `readers` alone for a file the address can edit and says which way Drive answered |
| A move leaves an item's sharing alone (what `move_file` assumed until 2026-10-09) | **Refuted** against the sharing guide, <https://developers.google.com/workspace/drive/api/guides/manage-sharing>, read 2026-10-09: "Moving an item to a new parent folder re-evaluates and applies the new parent's permissions to the item and its children." The Workspace admin help, <https://knowledge.workspace.google.com/admin/drive/moving-content-from-drive-shared-folders>, read the same day: "Any permissions that the moved content inherited from the shared folder are removed. It inherits new permissions from the destination folder, in addition to other, explicitly set, permissions." | `move_file` works out who can reach the item after the move from its direct grants and the destination's list, reports before and after, asks the person when that widens (§4a), and reads the after back from Drive |
| Moves into, out of and between shared drives follow the same rule (the API guides say nothing) | **Confirmed in part** against the Workspace help, <https://support.google.com/a/users/answer/12380484>, read 2026-10-09: an item moved in "keeps its sharing permissions", "file permissions inherited from the folder the file was in aren't copied", and its creator is "no longer the owner"; moved out, access is "reassessed" and "the file's original sharing settings take effect". It also says access "may change if sharing settings for the shared drive are more restrictive", without saying how | The prediction drops the owner on a move into a shared drive and otherwise applies the same rule. A drive's restriction dropping a direct grant would make the prediction overstate, which asks a question too many; the read-back reports what Drive did. Shared drive to shared drive is not driven live: the driver takes one `-drive` |
| A grant made on an item directly stays when it moves (the Workspace help above: "in addition to other, explicitly set, permissions") | **Refuted live 2026-10-23 for a grant the destination also gives**, in two runs: a file shared by link on its own, moved into a folder open by link and back out, came out private. The server's prediction for the move back, worked out from the list read just before it, said private too, so Drive was already listing the file's link grant as inherited only | Nothing changes in the server: each move is predicted from the grants read just before it, and the result shows the narrowing. The live driver shares the file by link again after each move back out, before a step that needs it shared |
| What the owner of a My Drive destination folder gets on an item moved into it (undocumented) | **Unverified.** No reference or help page read 2026-10-09 says | Counted as an editor, which errs toward showing more exposure. Settling it needs a folder another account owns, which a one-account run cannot make; the read-back reports what Drive did on any real move |
| `permissionDetails` marks inherited grants on a My Drive item (unstated for My Drive) | **Confirmed live 2026-10-09.** The `permissions` reference, read 2026-10-09, says `inherited` "is always populated" and `inheritedFrom` "is only populated for items in shared drives". Live, a grant from a folder above carried an inherited detail with no source, and four moves' predictions matched what Drive answered. A My Drive owner's grant carries both kinds: `list_permissions` showed it inherited, and a move's prediction kept it as made on the item, which needs a direct detail | The live driver moves a file into a link-shared folder and back out, and fails the step when the result says Drive answered other than predicted. An owner's grant is not called inherited, since it was made on the item |
| A copy takes on its new folder's grants and none of the original's (unstated) | **Confirmed live 2026-10-09, both ways**: a private file copied into a folder open by link came out open by link, and a file open by link on its own came out private in a private folder. The prediction counted the account as one more editor of the copy, which was fixed the same day: outside a shared drive a copy is the account's own. The `files.copy` reference (updated 2026-09-04) says only, of `ignoreDefaultVisibility`, "Permissions are still inherited from parent folders"; the sharing guide (updated 2026-09-14), read 2026-10-09, speaks of moves and not copies | `copy_file` works out the copy's reach from the destination's grants alone, and reads the copy's back from Drive after it is made, saying when the two differ. The live driver copies a private file into a folder open by link, and a file shared by link on its own into a private one, and fails a step when either copy is reached otherwise |
| `copy_file` asks like a move, and `create_file` and `upload_file` do not (decided 2026-10-09) | **Decided.** A review found that with `GDRIVE_SHARING=off` a move into a link-shared folder was refused while a copy into it succeeded, and trashing the original then finished the same exposure | A copy that more people could reach than the original is put to the person and, with `GDRIVE_SHARING=off`, refused, part by part for a folder (§4a, §7.3). `create_file` and `upload_file` put new content the caller wrote into the folder the caller named, so they are unchanged *(2026-10-23: nothing is put to the person. A copy that would reach past the organization is refused, as a move is; `create_file` and `upload_file` stay ordinary, recorded in §17b.)* |
| A move's new sharing is in place when `files.update` answers (unstated) | **Confirmed live 2026-10-09** for four moves: the permission list read right after each answer was the one predicted | The read-back is one permission list right after the move. A late answer shows as "that is not who this server worked out would reach it", which points at `list_permissions`; the live driver's two moves fail on it |
| A moved folder with limited access keeps out the destination's grants (unstated for a move) | **Unverified.** The sharing guide, read 2026-10-09, says of an inherited grant: "Changes must be made on the originating parent, or the folder must use the limited access setting." It says nothing about a move | The card reads `inheritedPermissionsDisabled`, and the move prediction does not use it: it counts the destination's grants on such a folder and may ask a question too many. A destination's own `view: metadata` grants are left out, since they do not reach what is inside it |
| A bulk move needs one approval per item, as removal and sharing do (the "Bulk operations belong in the tool surface" row did not cover moves) | **Rejected for moves, decided 2026-10-09.** That row rejected bulk work because "one item per call keeps every removal and every share a visible approval". A move removes nothing and grants nothing by name: it is undone by moving the item back. What it can do that is not undone is let more people reach the item, and since 2026-10-09 that is put to the person before any item moves, once for the batch, naming every item that would reach further. Forty moves into a folder nobody else can reach is forty approvals of nothing | `move_file` takes `files`, at most 50, to one destination. Each item gets its own outcome, and a failure leaves the moves before it in place. Removal and sharing stay one item per call, and there is no bulk rename *(2026-10-23: the question for the batch is gone; each item that would reach past the organization is refused with its reason, and the rest move.)* |
| A query may carry any number of `in parents` terms (unstated) | **Refuted live 2026-10-09.** The `files.list` reference (updated 2026-07-07) and the search guide (updated 2026-09-09), read 2026-10-09, state no limit on the length of `q`, its number of terms or its complexity. Live, Drive took 200 terms, 9,996 bytes of query, and refused 400, 19,996 bytes, with an HTML error page from Google's front end rather than a Drive error. A second run the same day found the same bounds and recorded the status: **HTTP 400**, not 414 | `under_folder` stops at 100 folders, about 5.6 KB of encoded terms, half of what Drive took, and refuses a larger tree. A search Google refuses with a page and a query longer than 9,996 bytes is called most likely too long. The live driver doubles a group of `in parents` terms from 50 until Drive refuses one, and prints where and with which status |
| A page token continues only the query it came from (unstated) | **Unverified.** The `files.list` reference says the token "is typically valid for several hours" and to start over when it is rejected; it does not say the token is bound to `q` | Every search wraps Drive's token with a digest of the request: the query, which holds every filter and the folder set, the order, the drive and the corpus. With `under_folder` it also carries the folder and a digest of the folder set. A continuation with any of those different, under another folder, without `under_folder`, or over a set that changed since the first page is refused here, so Drive never sees a token beside a different query. Until 2026-10-09 only `under_folder` searches were bound, and any other filter could change between pages |
| A page of `files.list` holds what `pageSize` asked unless it is the last (assumed by the `under_folder` walk until 2026-10-09) | **Refuted** against the `files.list` reference, read 2026-10-09: "Partial or empty result pages are possible even before the end of the files list has been reached" | The walk pages each level to its end. The fake can serve short pages (`FilePageCap`), and a test holds the walk to them |
| A search in one shared drive should name it (convention) | **Confirmed** against the `files.list` reference, read 2026-10-09: "Prefer `user` or `drive` to `allDrives` for efficiency" | The walk under a shared-drive folder, and the search after it, send that drive's `driveId` |
| `copyRequiresWriterPermission` is the file's download switch (what `update_file` and the card assumed until 2026-10-09) | **Refuted** against the release notes for 2025-07-16 ("the functionality is different for both reading from and writing to the field") and the files guide, <https://developers.google.com/workspace/drive/api/guides/file-locking>, read 2026-10-09: the field now reads whether viewers and commenters are restricted, counting the shared drive's setting; false "updates both the `restrictedForWriters` and `restrictedForReaders` fields to `false`"; use `DownloadRestriction` instead, and not both, since "the two field values might conflict" | The card shows `downloadRestrictions.effectiveDownloadRestrictionWithContext` and falls back to the legacy field only when that was not read. `restrict_download` writes `itemDownloadRestriction`. `copy_requires_writer_permission` stays in the contract, called legacy, and false reports that editors are let go too. The two together are refused |
| `files.update` takes `downloadRestrictions` with only `itemDownloadRestriction` (the guide's word; `drives.update` refused the drive's equivalent live, 2026-09-30) | **Confirmed live 2026-10-09 for all three levels**: the first run's card read editors and none back, and the second run set viewers directly and read it back too. The legacy `copyRequiresWriterPermission` then read true, and setting it false cleared the restriction. From nobody restricted, `copyRequiresWriterPermission: true` restricted viewers and commenters only, and `false` cleared both. The files guide says "set the `downloadRestrictions` field using the `files.update` method" and that only the item restriction can be set, with no example body. The drive-level refusal is why this is not taken on trust | `restrict_download` sends `{"itemDownloadRestriction": {"restrictedForReaders": …, "restrictedForWriters": …}}`, both switches always. The live driver sets each level on a Doc and fails a step when the card does not read it back. What `copyRequiresWriterPermission: true` does to `restrictedForWriters` is not documented either; the fake keeps it, and the live driver says which way Drive went |
| Limited access is a folder's, set by its owner or organizer (convention) | **Confirmed** against <https://developers.google.com/workspace/drive/api/guides/limited-expansive-access>, read 2026-10-09: "limited access isn't available for files"; "only the `owner` role in My Drive and the `organizer` role in shared drives can enable or disable limited access", and a writer in My Drive when `writersCanShare` is true; `canDisableInheritedPermissions` and `canEnableInheritedPermissions` say whether the account may | `limited_access` on a file is refused before Drive is asked, and so is a change the capabilities do not allow. What Drive answers for a file is not documented; the fake answers 400 `invalid` |
| A grant a limited-access folder keeps out is listed as `view: metadata` (convention) | **Confirmed for people** against the same guide: such permissions carry "`inheritedPermissionsDisabled=true` and `view=metadata`", "The role is always set to `reader`", and every `permissionDetails` entry is inherited. The `Permission.view` reference says the metadata view "is only supported on folders". **Confirmed live 2026-10-09 for a link**: a link grant from the folder above listed on a limited-access folder as a metadata view, every detail inherited, while the owner's grant stayed whole. **Unverified** for a domain grant from above | `list_permissions` says such a grant "can see it but not open it" and the summary counts it apart; a destination's metadata grants are left out of a move's prediction. The fake lists every grant from above that way, owners and organizers excepted, and passes none of them below the folder. The live driver gives a folder inside a link-shared one limited access and says how Drive lists the link |
| An approved file is locked either way (what `manage_approval`'s descriptions said until 2026-10-09) | **Refuted** against the approvals guide, <https://developers.google.com/workspace/drive/api/guides/approvals> (updated 2026-09-03), read 2026-10-09, and the release notes for 2026-07-15, which made `fileContentChangeBehavior` generally available on `start`. `RESET_APPROVAL`, the default, clears answers on a content change and locks the file once approved; under `NO_APPROVAL_ACTION` "the file isn't locked on final approval" and answers stay. The discovery document (revision 20261005) marks the field output only on `Approval` and optional on `StartApprovalRequest` | `on_content_change` takes `reset_approval` (default) or `no_action`, is sent on every start, and the result reports what Drive answered. The descriptions no longer promise a lock. The fake honors both, and resets an approved answer to `NO_RESPONSE` on a content change under the default, since the enum has no `NO_DECISION`. The live driver's destructive run starts one with `no_action` in its scratch shared drive, approves it, and fails the step when the file comes back locked |
| `archive/zip` reads no more of a part than its declared sizes (convention) | **Confirmed, and not enough**, in the Go 1.27.2 source, `archive/zip/reader.go`, read 2026-10-09: `File.Open` reads the compressed bytes through a section of `CompressedSize64`, and `checksumReader.Read` returns `ErrFormat` once it has produced more than `UncompressedSize64`. The compressed size bounds the read from above only: a deflate stream that ends early inflates from fewer bytes, so a header that declares a hundredth of the real size hides a thousandfold ratio. A review found a 254 KB file that parsed 250 MiB of XML that way | `internal/office` checks the declared ratio before opening a part, and the real one while it inflates, by counting the bytes the part pulls from the file |
| `archive/zip` can be handed any file and bounds itself (assumed) | **Refuted** in the same source: `Reader.init` reads the whole central directory into memory before returning. It reads headers until one is not a header, and compares only the low 16 bits of their count with the end record's, so an end record cannot bound it. A review's file claiming one entry before 1.4 million headers reached 349 MiB | `internal/office` reads the end record, zip64 included, refuses more than 10 000 entries or an 8 MiB directory, and counts the headers the way `archive/zip` will read them, from both places it may start, before `zip.NewReader` runs. It reads at most 8 MiB to do so. Tests hold that such files are refused after fetching a fraction of them |
| A part that inflates more than 100 times over is a zip bomb (convention) | **Confirmed as a convention** against Apache POI's `ZipSecureFile.setMinInflateRatio`, <https://poi.apache.org/apidocs/dev/org/apache/poi/openxml4j/util/ZipSecureFile.html>, read 2026-10-09: "It defaults to 1% (= 0.01d), i.e. when the compression is better than 1% for any given read package part, the parsing will fail indicating a Zip-Bomb". POI's `ZipArchiveThresholdInputStream`, read the same day, checks after every read, with the compressed bytes the stream has consumed | Adopted, with a 1 MiB grace below which a part is not checked: a small part costs little however well it compresses, and a real style sheet can. Checked as POI does, while the part is read |
| `encoding/xml` hands over a token without holding it whole (assumed) | **Refuted** in the Go 1.27.2 source, `encoding/xml/xml.go`, read 2026-10-09: the decoder writes a token's bytes into one buffer until the token ends, and `Token`'s documentation says the returned data refers to that buffer. A run of text 250 MiB long is held at once, and costs more again when copied to a string | `internal/office` stops the decoder once it has read 1 MiB past the end of the last token it handed over, which `Decoder.InputOffset` reports as it reads, and cuts the text short there |
| An XML parser needs guarding against entity expansion (convention) | **Not needed for `encoding/xml`.** `go doc encoding/xml Decoder`, Go 1.27.2: `Entity` "can be used to map non-standard entity names to string replacements", and in strict mode, the default, an unknown entity is a syntax error | No entity map is given, so a part cannot define the entity that expands into a billion copies of itself; a test holds that one is refused. Nesting is bounded at 256 by the walker, since the decoder does not bound it |
| Number formats 14 to 22 and 45 to 47 are dates and times (convention) | **Confirmed** against ECMA-376 Part 1's list of built-in number formats (§18.8.30, `numFmt`), read through mirrors of the standard 2026-10-09, and Apache POI's `DateUtil.isInternalDateFormat`, which names the same set | A cell in one of those, or in a custom format whose first section is only date and time letters once quoted text and bracketed codes are gone, shows as ISO 8601. Every other number shows as stored |
| Serial 60 in the 1900 date system is 29 February 1900 (convention) | **Confirmed** against ECMA-376 Part 1 §18.17.4.1 through mirrors, 2026-10-09: the 1900 base runs from serial 1 to 2 958 465 and gives the day that never was serial 60, for compatibility with Lotus 1-2-3; the 1904 base runs from 0 to 2 957 003. Microsoft's article on the two systems gives the 1 462 days between them | Serial 60, day zero and anything out of range show as the number they are rather than a wrong date; `date1904` of `1`, `true` or `on` switches the base |
| Word stores a built-in style under its English name in every language (assumed) | **Unverified.** A localized Word can translate the style id; the parser trusts the style's name, `heading N` or `Title`, and its outline level, never the id | A heading in a file saved by a localized Word that does not keep the English name reads as body text. The live driver's files are built here, so a live run cannot settle it either |
| Google's csv export of a Sheet pads every row to the widest (unstated) | **Unverified** | An Office workbook's rows end at their last value instead, which loses nothing and cannot turn formatting that runs to row 1 048 576 into a million blank lines |
| Drive answers a byte range of a blob with that range (convention, §2) | **Confirmed** by the reference's partial-download section and in use since phase 1 for text windows. **Confirmed live 2026-10-09** for the many small ranges an Office read makes: a workbook and a deck of about 99 KiB each, padded so their parts sit past the first 64 KiB block, each read whole and in two windows that joined | A response that is not a 206 for a range past the start is refused as `[unexpected]` rather than parsed. The live driver reads three uploaded Office files back |
| `files.copy` takes `ocrLanguage` and `ignoreDefaultVisibility` (convention) | **Confirmed** against the `files.copy` reference, read 2026-10-09: `ocrLanguage`, "A language hint for OCR processing during image import (ISO 639-1 code)"; `ignoreDefaultVisibility`, "Whether to ignore the domain's default visibility settings for the created file … Permissions are still inherited from parent folders." The upload guide, read the same day, calls the hint a BCP 47 code | `extract_text` passes the hint as given and describes it as ISO 639-1, the reference's word; a two-letter code is both. The copy goes to the root of My Drive, which passes on nobody's access |
| Google reads text out of JPEG, PNG, GIF, BMP and PDF (convention) | **Confirmed** against the upload guide's import table, read 2026-10-09, which lists those five as becoming a Google Doc. The Help Center, <https://support.google.com/drive/answer/176692>, read the same day, names PDFs and ".jpeg, .png and .gif" | The account's `about.importFormats` decides when it can be read; the guide's five stand in when it cannot |
| OCR has a size limit (convention) | **Confirmed as advice only.** The Help Center: "The file should be 2 MB or smaller", and text "at least 10 pixels high". Its storage page, <https://support.google.com/drive/answer/37603>, says a text document converted to a Doc "can be up to 50 MB"; for a PDF or an image that limit is **unverified** | Past 2 MB `extract_text` reads and warns; past 50 MB it refuses before copying, so a conversion that could only time out is not attempted |
| Google's OCR reads every page of a PDF (unstated) | **Unverified.** Third-party guides, none of them Google's and none dated, say only the first ten pages are read; Google's current pages say nothing about pages | Nothing in the code depends on it. A result shorter than the file is not explained by the server, because it cannot know |
| A copy converted to a Doc can be exported as soon as `files.copy` answers (unstated) | **Confirmed live 2026-10-09**, three times: two temporary copies and a kept one each exported as soon as `files.copy` answered | `extract_text` exports at once. A failure there deletes the copy and reports Google's answer |
| A Doc's plain-text export starts with a byte-order mark (unstated) | **Unverified** | `extract_text` drops one when it is there |
| `appProperties has { key=… and value=… }` finds a file by its app property (convention) | **Confirmed** against the search-terms guide, read 2026-10-09, which lists `appProperties` with the `has` operator. That the key alone is refused is confirmed live for both (phase 4, the row on the search guide above). That the key and value together find a file by its app property is confirmed live for `properties` and **unverified** for `appProperties`; the reference says app properties are "private to the requesting app" | The marker query names both. The fake accepts the query on `appProperties` and requires both halves, as it does for `properties` |
| A write returns text and JSON with everything the text says (§4, rule 9) | **Not for `extract_text`, decided 2026-10-09.** Its result is a file's text, up to 400 000 characters a window; a JSON copy doubles it, and Claude Code shows the model only the structured half when there is one (the row on Claude Code 2.1 above). `read_file` returns text only for the same reason | `extract_text` returns a text block alone, whose header names the kept or leftover copy's id and link. A protocol test holds that it returns no structured content |
| A permanent delete needs `GDRIVE_ENABLE_DESTRUCTIVE` (hard rule 5) | **Kept, with one exception decided 2026-10-09:** the temporary copy `extract_text` makes and deletes in the same call. It was never the person's, nobody has seen it, and putting it in the trash would leave a copy of their file there for 30 days | The delete reaches only the Google Doc Drive's answer to the copy named, never the source, and runs under its own deadline even when the call was canceled |
| A file card shows everyone who can reach a Google Form (unstated) | **Unverified, and probably not.** The Forms guide, <https://developers.google.com/workspace/forms/api/guides/publish-form>, updated 2026-09-03: "A responder is a permission with view='published' and role='reader'", anyone with the link as a responder is "a permission with type 'anyone', view 'published', and role 'reader'", and listing them needs "the query parameter `includePermissionsForView=published`". The `files.get` reference, updated 2026-03-20, says that parameter "Specifies which additional view's permissions to include in the response. Only `published` is supported." This server never sends it. A live run on 2026-10-09 found a Form whose card said "private to you" among the files a search for `visibility: link` and `anyone` returned. A second run the same day saw it again: the Form it created read "private to you" on its card, and both searches returned it. A third saw the same. Still unverified: no run looked at the published view | Nothing changed yet. A check by hand settles it: `get_file` on a Form published to anyone with the link, against `files.get` with `includePermissionsForView=published`. If the card under-reports, a Form's reads should ask for the published view and count its responders apart from its editors |
| Pinning a comment belongs to the Docs API, for every Google document (what `list_comments` said of every kind until 2026-10-09) | **Refuted for Sheets and Slides** against the discovery documents, fetched 2026-10-09: Sheets v4, Docs v1 and Slides v1 each publish an `InsertCommentRequest`, tied to a `GridCoordinate` in a sheet, a `Range` in a document, and a page or a page element in a presentation | `list_comments` names the kind, the place a comment pins to there and the API that pins it. Another Google kind gets the sentence without an API. `add_comment`'s description names all three |
| `use_content_as_indexable_text` makes an upload of a type Drive does not read searchable by its words, and does nothing for a type it reads (what the tool's description says) | **Confirmed live 2026-10-09 for a type Drive does not read**, on the third run. The same 51 bytes went up twice as `application/octet-stream`, once with the flag. A search for a word only those bytes hold found the flagged copy on the first try, and the other copy on neither of two searches. The `files.create` and `files.update` parameters in the discovery document fetched 2026-10-09 (revision 20261005) say only "Whether to use the uploaded content as indexable text." **Unverified** for a type Drive reads: the first two runs sent the flag on a text file and checked nothing On 2026-10-24 two runs sent the same pair: in the first the unflagged copy was not found, as before; in the second it was, so Drive sometimes reads `application/octet-stream` itself, and a run shows the flag's effect only when it does not | The description stands. The live driver keeps the step: it uploads the same bytes with and without the flag, searches for the word, and says so when the copy without the flag is found too. An empty search after 30 s is reported as unverified, as the other searches are |
| A download by the current revision's id can be checked against the file's `md5Checksum` (what `download_file` assumes since 2026-10-09; before, a download with any `revision` went unchecked) | **Confirmed live 2026-10-09** on the third run. A CSV downloaded by its head revision's id read "verified against Drive's md5", as did plain downloads of a CSV and of a 6 MiB binary. The same file's older revision read "not compared". The discovery document (revision 20261005) calls `md5Checksum` "The MD5 checksum for the content of the file" and `headRevisionId` "The ID of the file's head revision"; it does not say the two describe the same bytes | `download_file` compares the bytes when `revision` is empty or is the head revision's id. An older revision is not compared, and the result says so |
| The live driver can read from its own source which flag each option it did not send waits for (what its end-of-run report assumes since 2026-10-09; before, it said every such option sat behind `-file`, `-share` or `-drive`) | **Confirmed live 2026-10-09** on the third run, the first with the new report. It passed `-write` alone and sent 147 of 170 options in 267 calls. It named the eight options its source sends and the run did not, each with the flag it lacked: `-labels` for one, `-destructive` for four, `-share` for two, and "`-blocked` or `-share`" for `unshare_file.principal`. None fell to the warning kept for an option no missing flag explains | The report names each unsent option with the flag the run lacked, so the owed list at the head of this block names modes rather than options. A tool the run never called, here `list_drives` and `manage_drive`, is named once rather than by option |
| A Markdown file in Drive is a blob like any other, whose bytes `update_content` may replace whole (what this server assumed until 2026-10-14) | **Changed by Google on 2026-10-05.** The launch post, <https://workspaceupdates.googleblog.com/2026/10/preview-edit-and-collaborate-on-Markdown-files-natively-across-Drive-and-Docs.html>: people "view, edit, and collaborate on Markdown (.md or .markdown) files directly in Google Docs" and "without changing the file type". The Help Center, <https://support.google.com/docs/answer/18289341>, read 2026-10-14: "Work with others right away using comments". Where those comments are kept is published nowhere. **Measured live 2026-10-13 and 2026-10-14:** a comment made in the Docs editor is written into the file's text, as `<!-- gdoc_comment_begin: KEY -->` and `<!-- gdoc_comment_end: KEY -->` around the passage and an entry under the key in a `gdoc_comments` block that has to end the file, and `comments.list` does not return it. Text after the block made the editor show the block as body text. A `RESOLVED` status written into the text, on an entry without the token the editor gives its own, made the Docs API fail to read the file until it was put back | `update_content` on a `text/markdown` file refuses content that loses one of those comments unless `allow_comment_changes` (§7.2), and `read_file`, `get_file` and `list_comments` say where they are (§7.7). The live driver writes a comment into a file's text the way the editor does and checks the refusal, the override and the notes |
| Replacing a file's bytes keeps the file's type (what `update_content` from a local file assumed, sending the type the local file's name gave it) | **Refuted live 2026-10-13:** the uploaded media's Content-Type becomes the file's type. A Markdown file replaced with bytes sent as `text/plain` became a `text/plain` file, which the Docs API then refused to read (400); a metadata `mimeType` change answered 200 and was ignored; only another upload sent as `text/markdown` undid it. The live driver measures the change and the undoing on every `-write` run | `update_content` sends the file's own type unless `mime_type` names another, from inline content and a local file alike, which is what its schema already said |
| A file name's extension gives the same type wherever the server runs (what `upload_file`'s "by extension" assumed) | **Refuted** in the Go 1.27.2 source, `mime/type.go`, `type_unix.go` and `type_windows.go`, read 2026-10-14: the built-in table has no `.md` or `.markdown`, and the system's own tables are loaded over it, on unix `/usr/share/mime/globs2`, `/etc/mime.types` and four other paths, and on Windows the registry's "Content Type"; `setExtensionType` stores over a built-in entry, so even `.csv` and `.docx` answer what the machine says. Without a mapping the bytes were sniffed, and `http.DetectContentType` answers `text/plain`. RFC 7763, <https://www.rfc-editor.org/rfc/rfc7763> (March 2016), registers `text/markdown` with "File extension(s): .md, .markdown" | `internal/mediatype` names the extensions of the types this server reads or converts, and is asked before the platform: `.md` and `.markdown` are `text/markdown`, `.csv` is `text/csv`, and so on, everywhere. `upload_file` uses it, and so does `create_file`, whose inline text now takes a text type from its name instead of always `text/plain` |
| Google Docs treats only `text/markdown` as a Markdown file (what this server assumed while building the check) | **Refuted live 2026-10-14.** A file created as `text/x-markdown` kept that type in Drive, and the Docs API read it exactly as it read a `text/markdown` control made the same minute from the same bytes: two paragraphs, `# Probe` as `HEADING_1`, the same named styles. A media update sent as `text/x-markdown` kept that type and the Docs API read the new text. A `text/plain` `.md` is refused by the Docs API (400, measured 2026-10-13), and so is `text/x-web-markdown` (400 `INVALID_ARGUMENT`, 2026-10-14), so that alias is not one | `mediatype.IsMarkdown` takes both types, for the comment check and for every note; the registry names `text/x-markdown` a Markdown file |
| A revision kept forever can be unpinned again (what `manage_revision`'s description said: "or unpin one so Drive may discard it again") | **Refuted for every revision but the current one.** <https://developers.google.com/workspace/drive/api/guides/manage-revisions>, updated 2026-09-03: "Once a blob file revision is set to "Keep Forever", it can only be downloaded or deleted." <https://developers.google.com/workspace/drive/api/guides/handle-errors>, same date: `illegalKeepForeverModification`, 400, "occurs when trying to set a blob file revision marked as "Keep Forever" to `false`", fixed by deleting the revision. Measured live 2026-10-13: unpinning the current revision answered 200, an older pinned one 400. The live driver unpins the current revision and expects the refusal for the one `keep_previous_revision` pinned | `manage_revision` says a pin on a revision that is not current is for good and explains the refusal, naming `delete_revision`; `keep_previous_revision`'s description and result say the same, and the fake refuses as Drive does |
| A commit message holds only what a person typed, so every address in one is a leak (what `leaks history` assumed) | **Refuted for two trailers.** git-commit(1): `--signoff` adds "a `Signed-off-by` trailer by the committer at the end of the commit log message", and dependabot signs every commit that way. <https://docs.github.com/en/pull-requests/committing-changes-to-your-project/creating-and-editing-commits/creating-a-commit-with-multiple-authors>: "Add one or more `Co-authored-by` trailers to a commit message to attribute a commit to multiple authors", and "If a co-author keeps their email address private, use their GitHub-provided `no-reply` email". <https://docs.github.com/en/account-and-profile/reference/email-addresses-reference> gives that address as `ID+USERNAME@users.noreply.github.com`. Both trailers are the author header's record in another place. Run 2026-10-14, the scan flagged seven things, all on `main` for weeks: five such trailers, a type-name prefix, and the first code of conduct's contact address | `leaks history` blanks those two trailers, as it skips the header; `users.noreply.github.com` is a documented domain; the type-name prefix is an allowed id and the old code of conduct an allowed blob, each with its reason. CONTRIBUTING asks for no-reply addresses, since a work address in a trailer is no longer flagged. The scan is a step in closing a phase |

**Phase 10 additions (2026-10-23).** The family standard's re-check of
2026-10-23 read every source below again, with the quotes, against the
110 most-used MCP servers and the clients that run them. Those rows
were not probed live from this repository; each says what its verdict
rests on. The last row was found by the live runs of the same day.

| Convention | Verdict | Effect |
|---|---|---|
| A server should confirm a write that cannot be undone, or that widens access, with the person itself, through MCP form elicitation (what 2.0.0 built) | **Refuted for this family, decided 2026-10-22.** The MCP specification puts consent to a tool call on the host, and its elicitation is a way "to request additional information from users", with no consent rule. Claude Code's documentation (<https://code.claude.com/docs/en/mcp>, <https://code.claude.com/docs/en/hooks>): an `Elicitation` hook answers with no dialog. Codex accepts a form with no fields by itself under full access. Open reports on Claude Code's VS Code extension, Desktop's Code tab and Cowork: the question declared and not drawn, or left until it times out. No client's permission rules match an elicitation. Of the 91 most-used servers with risky writes, 83 never ask the person; the 8 that do each pick their own scope | The question is gone (§4a). What held on every client stays: the flags, `confirm`, the guards, `dry_run` |
| A client that prompts by annotations prompts by `destructiveHint` | **Refined.** Codex prompts by it in its default tool mode, and claude.ai documents that it decides its prompt. VS Code never reads `destructiveHint`: it prompts on every tool without `readOnlyHint: true` | `readOnlyHint` only on a tool that changes nothing; `destructiveHint` accurate. `reply_comment` and `manage_labels`, which replace what was there, are now destructive |
| A remembered approval can be scoped to an argument value, so a wide form may be an argument of an ordinary tool (what `allow_anyone`, `allow_domain`, `transfer_ownership` and `approve` in `manage_approval` assumed) | **Refuted.** claude.ai, Codex, VS Code and Copilot remember "always allow" per tool or per server; Claude Code's permission rules match an MCP argument only in a command-line deny rule. Argument-level control exists only where an operator writes it (hooks; Gemini CLI's policy rules). Notion, Slack, Canva, Figma, Asana and Airtable's servers offer no sharing tool; Box's sharing tools are off until an admin enables them | Each wide form is its own tool, behind the destructive flag (§4a) |
| `requiresUserInteraction` is one more prompt a client may show | **Refined.** Claude Code, v2.1.199 or later, prompts on every call of a marked tool in every interactive mode, whatever the allow rules; from v2.1.246 it offers no "don't ask again"; where no prompt can be drawn (`dontAsk`, `-p` without a permission host) it denies the call, and only an Agent SDK host's `canUseTool` can approve it. Anthropic documents it for "tools whose permission prompt is itself the point". GitLab's own server calls the same mark "advisory" (MR !252995: "Only Claude Code 2.1.199 and later honour the annotation"). No other client reads it | The mark on four tools whose prompt is the point, for every client, and the destructive flag still the control (§4a) |
| Turning a shared drive's `members_only` off restores access already granted to non-members | **Unverified.** The drives reference describes `driveMembersOnly` as restricting access to the drive's items to its members, and says nothing of what happens to a grant made to a non-member while it was on | `members_only` off stays an ordinary `manage_drive` switch until the probe §17 records |
| A comment or reply id uses only letters, digits, `-` and `_` (what the live driver's listing parser assumed) | **Refuted live 2026-10-23.** Every reply id a run listed carried a dot, so the parser read none of them, and the run sent neither `reply_comment`'s nor `delete_comment`'s `reply` | The driver's patterns accept the dot. The server passes a reply id to Drive as given; the next run edited three replies and deleted one by such ids |
| A bundle install has a second approval behind the server, as Claude Code's ask rules are | **Refuted, 2026-10-23 (the docs and the reference host; Claude Desktop not probed, and no page names desktop extensions).** anthropics/mcpb v2.1.2 declares `tools` for display, and its `src/` reads it only in `init`; its `src/shared/config.ts` passes a boolean to `env` as "true" or "false". claude.com/docs/connectors/getting-started: "Select Allow once to continue, or Always allow to skip the prompt for that tool from now on". claude.com/docs/connectors/building/review-criteria: readOnlyHint and destructiveHint "determine auto-permissions in Claude". Claude Desktop has nothing like Claude Code's `permissions.ask` | Every switch the bundle asks for defaults to off: `gates mcpb` starts the server from the environment the bundle's defaults give it and from none, and names any field that differs. The bundle declares no `tools` list, which would be display only and wrong for most installs, since five settings change what registers (§12) |
| A bundle's optional setting the person leaves blank reaches the server empty | **Refuted, 2026-10-23 (the reference host; Claude Desktop not probed).** anthropics/mcpb v2.1.2 `src/shared/config.ts` builds its variables from the defaults and the person's values, and `replaceVariables` leaves a variable it does not have as written. An optional key with no default is never a variable, so `${user_config.local_dir}` would reach the server as that text, which it refuses as a path that is not absolute. The 3.0.0 bundle asked only for the client file, which is required, so it was not affected | Every optional key has a default, `""` for text, files and directories, and `gates mcpb` refuses one without. The environment the defaults give the server must configure it as no environment does, which the gate checks by starting both, and `TestVarsDescribeWhatBuildDoes` holds `""` to configure nothing for every variable |
| `privacy_policies` is optional for a bundle | **Refuted, 2026-10-23 (the specification).** anthropics/mcpb `MANIFEST.md`: "Required when the extension connects to external services (first- or third-party) that process user data"; added in manifest_version 0.2, and enforced by neither the schema nor `mcpb validate` | The manifest names https://policies.google.com/privacy, which answered 200 on 2026-10-24, and `gates mcpb` refuses an empty list |
| A platform override adds to `mcp_config`, and a bundle's settings reach the server only through `env` | **Refuted, 2026-10-24 (the reference host; Claude Desktop not probed).** anthropics/mcpb v2.1.2 `src/shared/config.ts` L114-120 take `platformConfig.args \|\| result.args` and `platformConfig.env \|\| result.env`, and L173-176 substitute variables over the whole result, `args` included. An override's `args` or `env` replaces the base's, and an empty `[]` or `{}` is truthy, so it replaces the base too. An argument such as `--enable-destructive=true` reaches the server past every check on `env`; the 3.1.0 gate passed one | `gates mcpb` refuses `args` and `env` on any override, an empty one included, and holds the base `args` to `[]`, refusing it missing, null or not empty. The key stays, since no Claude Desktop launch has shown it optional |
| Any `command` the specification shows runs from the bundle | **Refined, 2026-10-24 (the specification and the reference host; Claude Desktop not probed).** anthropics/mcpb v2.1.2: `MANIFEST.md` L497 and L510-515 give a binary the command `server/my-server`, and L532 says `${__dirname}` "is replaced with the absolute path to the extension's directory"; `src/shared/config.ts` L118, L133 and L173-176 substitute variables in `command` and resolve no path. The specification shows a relative command and never says what it is resolved against, and the reference host passes it to the spawn as written. `${__dirname}` is the one form whose meaning the specification fixes. The 3.1.0 gate stripped `${__dirname}` and a leading `/`, so `/server/x` passed | `gates mcpb` requires every command to start with `${__dirname}/`, as the committed manifest's do, refuses a relative or absolute one, and resolves a command and `entry_point` only against the programs staged, never `LICENSE` or `README.md`. A probe of Claude Desktop's working directory would settle the relative form |
