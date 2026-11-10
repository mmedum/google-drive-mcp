# Architecture

How `google-drive-mcp` is put together, and the decisions a contributor
should not undo without a better reason than the one that put them here.
Part 1 is the overview. Part 2, from [Decisions](#decisions), is one
entry per decision, cited elsewhere by its D-ID. What was verified about
Google lives in the comment at the code that depends on it, with the
reference linked and the live step that checks it named.

## Context

`google-drive-mcp` is a Model Context Protocol server for Google Drive.
One Go binary runs as a subprocess of an MCP client and speaks MCP over
stdio. It holds one person's OAuth refresh token and calls the Drive API
v3 over raw REST, with wire types of its own (D-01, D-09). When the
deployer turns them on, it also calls the Drive Labels API and the Drive
Activity API (D-58). Nothing is hosted.

The parties around the server:

- **The person** creates their own Cloud project and Desktop OAuth
  client, signs in once with `google-drive-mcp login`, and every call
  acts as them, with their own access (D-01, D-59).
- **The deployer** decides what the server may do, through `GDRIVE_*`
  settings the client passes at start: read-only mode, the destructive
  tools, the sharing tools, the local directory, labels and activity
  (D-23, D-35, D-48, D-57, D-58). The deployer is usually the person,
  setting up their own client.
- **The organization's administrator** sets a sharing policy that Google
  enforces on every call. The server reads it through `canShare` and
  Google's refusals, and never copies it (D-35).
- **The client** draws the approval prompt for each call from the tool
  annotations and its own rules. The server asks the person nothing
  itself (D-44).
- **The model** chooses the calls and their arguments. It can be wrong,
  or persuaded by a file or a comment somebody else wrote. So nothing it
  sends turns on a tool, reaches past the organization or changes a
  setting (D-11, D-38, D-42, D-47).
- **Everyone who can reach a file** writes its content, its comments and
  its access requests, and all of that is data to this server (D-11).

The server sends a credential only to Google: `www.googleapis.com` and
the other `googleapis.com` hosts, `accounts.google.com`,
`drive.google.com` and the `googleusercontent.com` hosts a download link
points at. Every URL is checked with its port before a credential is
attached, and there is no telemetry (D-04).

It works at the file boundary (D-02). Everything Drive does to a file is
in scope: finding it, where it sits, who can reach it, folders and
shortcuts, moves and copies, content in and out, sharing, shared drives,
revisions, the changes feed, comments and access requests. On Workspace,
labels and approvals are in scope too. What is inside a Google Doc,
Sheet or Slides deck belongs to that kind's own API; this server reads
such a file through Google's export and says so.

The platform has gaps nothing here can close, so nothing here promises
to close them. Drive cannot copy a folder in one call, so a tree copy is
a budgeted walk (D-19). It cannot read a PDF's or an image's text
without making a Doc (D-22), or export a document past its export cap or
a sheet other than the first. It cannot give a file two parents, create
an access request, remove an inherited shared-drive grant on the child,
or undo a permanent delete. It pushes a change only to a public endpoint
(D-87).

It is not an editor for Docs, Sheets or Slides content (D-02), a sync
client (D-87), a hosted service (D-01) or a domain-administrator tool
(D-89). It does not do client-side encryption, Apps Script, the Drive UI
integration or the app data folder.

It exists because most of what goes wrong with a file goes wrong at the
boundary: where it sits, who can already reach it, and what a share
would widen. Other servers lose shared drives, guess between candidates
and widen access without saying so. This one shows the location and the
reach, refuses to guess, and refuses a widening nobody named (D-12,
D-14, D-36, D-42).

## Quality goals

In priority order:

1. **Access never widens by accident.** Google's policy decides what may
   be shared, and `share_file` reaches named people and groups inside
   the organization. Every wider form is its own tool behind the
   destructive flag, holding the name of what it opens. A move, a copy
   or limited access turned off that would reach past the organization
   is refused. Every sharing result shows who could reach the item
   before and after (D-35, D-36, D-38, D-40, D-41, D-42).
2. **Nothing is destroyed without a way back unless the deployer said
   so.** Removal is the trash. A permanent delete exists only under the
   destructive flag and takes `confirm`. There is no bulk delete or
   share, and a bounded call never names more than one resource (D-48,
   D-49, D-50).
3. **The model never guesses which file.** Ids are the contract. A name
   or path matching several items is `[ambiguous]` with the candidates,
   and every result says where a file sits (D-12, D-13, D-14).
4. **Results say what came back.** A write reports what Drive's answer
   carried, never an outcome built from the request. Where Drive's reads
   lag its writes, the result says so (D-29, D-30). The tests run
   against a fake that matches no more than Drive does, and live runs
   check the beliefs it encodes (D-67, D-68).
5. **The stream, the logs and the repository stay clean, and the surface
   is a contract.** Stdout carries frames only, and logs carry no
   payload. Content that is not text never passes through the context
   window, the repository holds nothing real, and a released tool or
   field keeps working (D-05, D-06, D-07, D-23, D-64).

## Codemap

- `cmd/google-drive-mcp` is the program. With no subcommand it serves;
  `login`, `logout`, `status` (text or `--json`), `doctor`, `--version`
  and `--dump-schemas` are the rest. It names stdout and stderr once and
  passes them down (D-05). It serves without credentials and answers
  `[auth]` per call (D-62).
- `internal/auth` runs Google's desktop flow on a loopback port with
  PKCE, and holds the scope set for each mode and opt-in API. Its token
  source runs on a bounded client of its own. It also inspects and
  revokes a grant (D-53, D-56, D-59).
- `internal/config` reads the `GDRIVE_*` environment and the bound flags
  through `config.Lookup`, and validates them once at start.
  `config.Vars` is the registry of every variable the server reads,
  which the staleness and `mcpb` gates read. A removed setting still
  turned on is refused (D-44, D-63).
- `internal/credentials` resolves the refresh token from the
  environment, the OS keyring, or an owner-only file, with a warning
  when the keyring fails (D-60).
- `internal/gapi` is the raw REST client for Drive, Drive Labels and
  Drive Activity. It holds what every request shares: the host allowlist
  with the port, resource-key headers, the read, write and sharing
  limiters, deadlines, retries by HTTP method, the guard on reserved
  path segments, long-running downloads, resumable uploads, and the
  classes of Google's failures. Nothing here imports MCP (D-04, D-24,
  D-28, D-50, D-52, D-53).
- `internal/gapi/drivetest` is the in-memory Drive behind `httptest`:
  parents, trash, permissions with inheritance, revisions, shared drives
  with a lagging listing, a changes log, resumable sessions that store
  part of a chunk, ranged reads, short pages and injected errors. It
  refuses what Drive refuses (D-67).
- `internal/gdrive` holds the hand-written wire types for the fields
  this server reads and sends. It imports nothing (D-09).
- `internal/mdcomment` finds the comments Google Docs keeps in a
  Markdown file's text, from a stream and under bounds, and says what a
  replacement would lose. It imports nothing from this repository
  (D-33).
- `internal/mediatype` is one table per media type: its display name,
  kind filter, export name, the format a read takes, the format a
  download defaults to, and the extensions that name it on every machine
  (D-34).
- `internal/model` is the server's view of a file: its kind in words,
  its location with what is above it, the sharing summary, grants and
  who a change reaches, and what the account may do (D-14, D-15, D-42).
- `internal/office` reads the text of Word, Excel and PowerPoint files
  and their OpenDocument counterparts from byte ranges, under hard
  bounds. It has no network and imports nothing from this repository
  (D-21).
- `internal/office/officetest` builds small Office and OpenDocument
  files for the tests and the live driver (D-08).
- `internal/redact` masks an account address in what `status`, `doctor`,
  a transport error and the drivers print (D-06, D-72).
- `internal/ref` parses what a tool takes where it points at something:
  an id, every Google URL shape with its resource key, or a path from My
  Drive or a shared drive. It parses only and has no network (D-12).
- `internal/render` writes every word a caller reads: the card, listings
  and trees with their footers, permissions, revisions, changes,
  comments quoted as somebody else's words, write reports and refusals
  (D-11, D-16, D-25).
- `internal/server` wires the MCP SDK: the server instructions, the
  `gdrive://` resource templates, and the schema dump through an
  in-memory session (D-26, D-64).
- `internal/service` is where the rules live: references and paths with
  their short caches, search and the folder-tree walk, budgets, content
  in and out, the local directory, the sharing rules and the reach of a
  move or copy, the trash and the gated deletes, drives, history,
  comments, access requests, labels, approvals and activity. It is where
  a failure becomes a `[class]` tool error (D-12, D-17, D-19, D-23,
  D-35, D-42, D-51, D-54).
- `internal/tools` registers the MCP tools, each with its annotations,
  its registration switches and, for a few, the mark. Handlers validate
  input, call the service and shape the reply (D-25, D-40, D-45, D-46,
  D-48, D-57).
- `internal/userconfig` stores non-secret per-profile state: the client
  file's path, the account, where the token went and the granted scopes
  (D-61).
- `internal/version` holds the version the build stamps in, with the
  module's build info as a fallback.

Dependencies run one way: `tools` to `service`, to `gapi`, to `gdrive`.
`service` also uses `model`, `render`, `ref`, `office`, `mdcomment` and
`mediatype`, and nothing under `internal/gapi` imports MCP.

Beside the packages, `scripts/gates` is every check the repository runs
on itself, written in Go behind one dispatcher (D-10, D-76, D-77).
`scripts/livedrive` is the live driver (D-70, D-71), and `scripts/evals`
scores a model driving the tools (D-73). `scripts/internal` holds the
stdio client, the transcript and its redactor, and the live-coverage
recorder they share (D-72). `testdata` holds the released surface, the
API snapshot and its verdicts, the live-coverage and outcome records,
and the renderers' goldens (D-29, D-64, D-69, D-70). `packaging` holds
the Claude Desktop bundle's manifest and launcher, and the MCP registry
entry (D-83, D-84, D-85).

## Request flow

A tool call crosses the layers in one direction:

```
MCP client
   │  JSON-RPC over stdio; stdout carries nothing else
   ▼
internal/server     SDK session, instructions, resource templates
   ▼
internal/tools      decode the input; annotations, registration, the mark,
   │                text for a read, text and JSON for a write
   ▼
internal/service    resolve the reference; budget; the sharing rules and
   │                the reach of a move or copy; [class] errors
   ├──► internal/ref        ids, URLs and paths, no network
   ├──► internal/model      kind, location, sharing, capabilities
   ├──► internal/office     Office text from byte ranges, no network
   ├──► internal/mdcomment  a Markdown file's comments, no network
   └──► internal/render     every word a caller reads
   ▼
internal/gapi       one request: host allowlist, limiter, deadline,
   │                retry by method, classify
   ▼
internal/auth       refresh on a bounded client
   ▼
Drive API v3, Drive Labels, Drive Activity, Google OAuth
```

### Start

`main` loads the configuration and refuses a bad one before it serves, a
removed setting still turned on included (D-44, D-63). With no refresh
token the server serves anyway, and every call answers `[auth]` naming
`login` (D-62). `tools.Register` adds every tool the configuration
allows. Read-only mode keeps the reads and `download_file`, sharing off
drops the tools that grant, and a destructive, label or activity tool
without its setting is left out (D-35, D-48, D-57, D-58).

### A read

A client calls `get_file` on a path. The service parses the reference:
an id, a URL whose resource key is remembered, or a path (D-12). A path
is walked one segment at a time with `name =` and `trashed = false`, and
each resolved `(parent, name)` pair is cached briefly (D-54). No match
names the folder's siblings, and more than one match is `[ambiguous]`
with every candidate. A shortcut in the middle of a path is followed;
the last segment is followed only by a tool that reads (D-13).

`gapi` sends one `files.get` with a fixed field list. The request goes
through the read limiter, under the per-attempt deadline, to an allowed
host only, and a refusal to begin is retried with backoff (D-04, D-52,
D-53). A shared-drive item adds a `permissions.list` for the sharing
summary. `model` builds the view: the kind in words, the location by
climbing the parents, the sharing, and what the account may do (D-14,
D-15). `render` writes the card, and the tool returns text alone (D-25).

### A guarded write

A client calls `move_file`. The service resolves the item and the
destination, reads both permission lists, and works out who could reach
the item after the move. Direct grants stay, inherited ones go, the
destination's arrive, and a move into a shared drive drops the owner.

If what the move adds includes anyone with the link, a domain or someone
outside the organization, or a list cannot be read, the move is refused
`[blocked]`, naming each (D-42). With sharing off, any gain is refused
`[forbidden]` (D-35). A `dry_run` stops here and says "would" (D-31).

Otherwise `files.update` moves it with `addParents` and `removeParents`,
a PATCH, so a retry is safe (D-28). The service reads the permissions
again and reports before and after, and says so when Drive answered
other than predicted (D-29, D-36). The write evicts the cache entries it
touched (D-54). With `files`, each item gets its own outcome, and a
failure leaves the moves before it in place (D-49). The result is text
and JSON (D-25).

### Content out and in

`download_file` and `upload_file` refuse without `GDRIVE_LOCAL_DIR`, and
resolve a local path through its symlinks before checking that it is
inside (D-23). A blob downloads through `alt=media`, streamed to disk
under a stall timer and checked against Drive's md5. A document goes
through `files.export`, and a video through a long-running operation.
Nothing local is overwritten (D-24).

An upload goes multipart while it is small and resumable past that, in
chunks. A cut connection is recovered inside the call from what the
session says it holds. It carries a pre-generated id where Drive takes
one, so a retry cannot make two (D-24, D-27). `read_file` is the inline
counterpart: it fetches only the byte window it shows, exports a
document, or reads an Office file from byte ranges (D-20, D-21).

### A share

`share_file` resolves the file and reads `capabilities.canShare` first.
It checks that the principal is a named person or group inside the
organization, and refuses anything wider `[blocked]`, naming the tool
that does it (D-35, D-38, D-40). A principal with a grant already has it
updated. Notification mail is off unless asked for (D-37), and the
result shows who could reach the item before and after, read back from
Drive (D-36).

## Invariants

- Stdout carries only MCP JSON-RPC frames (D-05).
- No log line carries a file name, a path, an address, a query or
  content (D-06).
- Nothing in the repository comes from a real Drive, account or
  deployment (D-07, D-08).
- A credential goes only to Google's hosts, checked with the port
  (D-04).
- A name or path that matches several items is never resolved by picking
  one (D-12).
- No ordinary tool grants past the organization, and nothing widens by
  omission (D-38, D-42).
- A risky variant is a tool of its own, never an argument (D-40).
- A permanent delete exists only under the destructive flag and takes
  `confirm` (D-48), and a bounded delete never names more than one
  resource (D-50).
- The server asks the person nothing (D-44), and no tool writes its
  settings (D-47).
- `destructiveHint` is false only on a write that only adds (D-45).
- A dry run sends nothing (D-31).
- A result is built from Drive's answer, never from the request (D-29).
- A write that may have landed is never repeated (D-28).
- Every attempt, token refresh and transfer chunk runs under a deadline
  (D-53).
- A released tool, output field or resource template changes only in a
  major version (D-64).
- Every API method and published field has a written verdict (D-69).
- Nothing the live driver or the evals print reaches a terminal
  unredacted (D-72).
- `make check` and CI run the same gates (D-77).

## Cross-cutting concerns

**Configuration.** Every setting is a `GDRIVE_*` variable with a flag
bound to it, and a flag overrides the environment. Every reader goes
through `config.Lookup`, which reads a value of spaces alone as unset.
Validation runs once at start, and a removed setting still turned on is
refused (D-63). Read-only mode, the destructive flag, sharing off,
labels and activity each change what registers, and some change the
scopes `login` asks for (D-56, D-57, D-58). The full list is in
[configuration.md](configuration.md).

**Approval.** The client approves each call, and the annotations decide
its prompt, so they are exact (D-44, D-45). Claude Code's
`requiresUserInteraction` mark sits on the tools whose prompt is the
point (D-46). What holds on every client is the registration flags,
`confirm`, the server's guards and `dry_run`. A risky variant is a tool
of its own, because a client remembers an approval per tool and never
per argument (D-40). The README lists the tools.

**Errors.** Every tool error is a tool result in the form
`[class] message`, and the class tells the caller what to do next. The
vocabulary is closed and declared in `gapi.Classes` in
`internal/gapi/errors.go`, where each class's comment says what it
means, and `internal/service` aliases it. `make classes` fails on a
class emitted and not declared, and on one declared and never emitted.
Google's own words are quoted only from its error envelope (D-51).

**Eventual consistency.** Drive's reads lag its writes on several
surfaces: the changes feed, `drives.list` after a create, a shared
drive's trash, the trash after `emptyTrash`, and `revisions.get` after a
write. A result never asserts what the response did not carry, and where
a listing lags and a `get` exists, an id reaches the `get`. A tool never
sleeps and retries; where the answer may be stale, it says so (D-29,
D-30). Each lag is recorded at the call it affects.

**Reliability.** Reads, writes and sharing each take their own limiter.
A transient failure backs off with jitter and honors `Retry-After`.
Repeatability comes from the HTTP method, and a write that may have
landed is `[ambiguous_outcome]` (D-28, D-52). Every attempt and every
token refresh has a deadline (D-53). The caches are short and never
decide correctness (D-54).

**Logging and what is pasted.** `slog` writes to stderr. A line carries
truncated ids, counts, byte counts, latencies and error classes, and
nothing a person named or wrote (D-06). `status`, `doctor` and the
drivers' transcripts mask the account (D-72). The threat model is in
[security.md](security.md).

**Testing.** The unit suite runs against `internal/gapi/drivetest`,
which refuses what Drive refuses and matches no more than Drive does
(D-67). Renderer goldens live in `testdata/golden`, and the Office
parser is fuzzed (D-21). The coverage floor is per package over a list
derived from the tree (D-74). Call counts, memory and linearity are
tests, and milliseconds stay in the benchmarks (D-75).

`make live` drives the shipped binary against a real account in scratch
space, and a person reads its transcript (D-70, D-71). It is the check
that can show a belief about Google wrong (D-68). `make live-cover`
fails on a tool option no live step drives without a recorded reason.
The evals score a model's end state and its trace (D-73). How to run a
live check is in [CONTRIBUTING.md](../CONTRIBUTING.md#the-live-run).

**Documentation.** A decision lives once, in part 2 here. What was
verified about Google lives in the comment at the line that depends on
it: the behavior, the reference link, where the reference is wrong or
silent, and the live step that checks it. A refuted belief goes at that
line or in a decision's `Rejected:` line.

`testdata/api-coverage.tsv` and `testdata/api-fields.tsv` hold the
verdict on every published method and field (D-69). The docs gate holds
every D-ID citation to an entry, every "Held by" cell to something that
exists, every live step and integration test a comment or a doc names to
one that exists, and the codemap to the tree. The staleness gate holds the
README, [configuration.md](configuration.md) and the CHANGELOG to the
code (D-66).

**Distribution.** A release is built from a tag: reproducible archives,
signed checksums and a provenance attestation, verified from outside
(D-82). The Claude Desktop bundle asks for what a person sets in another
client's configuration, every switch off by default (D-84, D-85). The
registry entry is published last (D-83). Releasing is in
[release.md](release.md).

## Decisions

| ID | Decision | Status | Held by |
| --- | --- | --- | --- |
| D-01 | One stdio binary per person, signed in to their own Google account through their own OAuth client | accepted | smoke, review |
| D-02 | The server stops at the file boundary: what is inside a Doc, Sheet or Slides deck belongs to that kind's own API | accepted | TestUpdateContentRefusesWhatHasNoBytes, TestReadFileOfADocSaysWhereItsContentIsEditedOnce, TestReadFileSaysASheetIsOnlyItsFirstSheet, TestListCommentsNamesWhereEachKindPinsAComment |
| D-03 | Tool names are snake_case verb_noun, no dots, distinct from a Docs or Sheets server's names | accepted | review |
| D-04 | The server talks only to Google's hosts over HTTPS, checked with the port before a credential is attached | accepted | TestRefusesToSendCredentialsElsewhere, TestAllowsGoogleHosts |
| D-05 | Stdout carries only MCP JSON-RPC frames | accepted | forbidigo, smoke, TestStatusJSONFlagIsTheWholeOfStdout, TestTheLinuxLauncherPicksABinaryAndKeepsStdoutClean |
| D-06 | Logs carry truncated ids, counts and latencies, never names, paths, addresses, queries or content | accepted | TestLogsCarryNoTraceOfWhatWasTouched, TestATransportErrorDoesNotCarryTheSearchTerms, TestATokenFailureCarriesNoQuery, TestShortIDTruncates |
| D-07 | Nothing from a real Drive, account or deployment enters the repository, and two scanners hold it | accepted | leaks, precommit, TestCatchesWhatALiveRunCanDragIn, TestEveryAllowedIDCarriesAReason, TestHistoryScansMessagesAndNotTheIdentitiesGitWrites, TestHistoryFindsWhatTheTipNoLongerHas, TestACompiledBinaryIsAFindingAndNotASkip, TestAURLWhoseHostDoesNotParseIsAFinding |
| D-08 | Fixtures are synthetic, and a fixture id says so | accepted | leaks, TestSyntheticIdsSaySoInTheirOwnText |
| D-09 | Own wire types over raw REST: no generated client, and no code from another project | accepted | depguard, api-fields |
| D-10 | Everything the repository runs on itself is Go | accepted | review |
| D-11 | What a file or a comment says is data: the server never acts on it, and quotes comment text as somebody else's words | accepted | TestCommentTextIsMarkedAsSomebodyElsesWords, review |
| D-12 | A reference is an id, a Google URL or a path, and one that matches several items is `[ambiguous]` with every candidate | accepted | TestResolveAmbiguousPathListsCandidates, TestNamesDifferingOnlyInCaseAreAmbiguous, TestResolveAmbiguousDriveName, TestParseURLShapes, TestIDShapedWordThatNamesNoFileIsTriedAsAName, TestAMistypedIDIsNotRetriedAsAName, TestARequestNamingSeveralRolesIsRefusedRatherThanGuessedAt |
| D-13 | The tools that read follow a shortcut; the tools that organize, share or trash act on the shortcut itself | accepted | TestResolveFollowsAShortcutOnlyWhenAsked, TestResolveFollowsAShortcutInTheMiddleOfAPath, TestAPathNamingAShortcutResolvesToTheShortcut, TestGetFileOnAShortcutDescribesTheShortcut, TestTrashActsOnTheShortcutNotItsTarget |
| D-14 | Every result shows where a file sits | accepted | TestLocation, TestLocationOfAFileSharedWithMe, TestDeepLocationSaysWhatIsAboveRatherThanInventingIt, TestASearchPageCostsOneListAndBoundedParentLookups |
| D-15 | A file is described as the Drive UI shows it: kind, owner, sharing and what the account may do, in words | accepted | TestFileCardGolden, TestSharingSummaries, TestKindNames, TestAGoogleDocumentReportsNoSize, TestListDrivesShowsWhatThisAccountMayDo |
| D-16 | Every listing is one page per call, budgeted, and says where it stopped and how to continue | accepted | TestTreeHonorsTheItemBudget, TestEmptyListingStillCarriesTheWarningAndTheContinuation, TestBudget, TestSearchUnderFolderPagesThroughEachLevel |
| D-17 | A continuation token continues only the query it came from | accepted | TestASearchPageContinuesOnlyTheSearchItCameFrom, TestSearchUnderFolderRefusesAPageWhoseFoldersChanged |
| D-18 | Search takes typed fields and says that Drive matches word prefixes, not substrings | accepted | TestSearchNameIsAWordPrefixNotASubstring, TestNameContainsMatchesWordPrefixesOnly, TestEscapesInLiterals, TestSearchByPropertyRefusesAHalfWrittenPair, live |
| D-19 | A tree operation refuses a tree over its budget before it starts, rather than doing half | accepted | TestSearchUnderFolderRefusesATreePastTheCap, TestATreeOverTheBudgetIsRefusedBeforeAnythingIsCopied |
| D-20 | `read_file` fetches only the byte window it shows, and has no size limit | accepted | TestReadFileWindowsALargeBlobRatherThanRefusingIt, TestReadFileWindowsALargeFileAndSaysHowToContinue, TestReadFileRefusesARangeAnsweredWithTheWholeFile |
| D-21 | Office and OpenDocument files are read locally from byte ranges, under hard bounds | accepted | FuzzExtract, FuzzPartXML, TestATooLongDirectoryIsRefusedBeforeItIsRead, TestReadingStopsWhenTheXMLBudgetIsSpent, TestReadFileRefusesTheOldBinaryOfficeFormats |
| D-22 | `extract_text` reads a PDF or an image through a temporary Doc copy that it deletes in the same call | accepted | TestExtractTextReadsAScanAndDeletesItsCopy, TestExtractTextDeletesOnlyTheGoogleDocItsCopyMade, TestExtractTextDeletesItsCopyEvenWhenTheCallIsCanceled, TestExtractTextCallsALostCopyAmbiguousAndDoesNotRepeatIt |
| D-23 | Files move only through one local directory, never through the context window, and unset means no transfer | accepted | TestUploadsRefuseAPathOutsideTheLocalDirectory, TestDownloadFileNeedsTheLocalDirectory, TestDownloadFileNeverOverwrites, TestLocalDirMustBeAbsolute |
| D-24 | Transfers stream in chunks, resume inside the call, and are checked against Drive's md5 | accepted | TestUploadResumableContinuesAfterAnInterruption, TestUploadResumableContinuesFromMidChunk, TestUploadResumableGivesUpWhenNothingIsStored, TestADownloadDoesNotHoldTheFile, TestDownloadFileWritesAndVerifies, TestDownloadFileVerifiesTheHeadRevisionByID |
| D-25 | Read tools return text only; write tools return text and JSON carrying everything the text does | accepted | TestReadToolsThroughTheProtocol, TestWriteToolsThroughTheProtocol, TestExtractTextReturnsTheTextAloneAndLeavesNoCopy |
| D-26 | Resources carry a file's text, its card and a folder's first page, never a listing or a subscription | accepted | TestReadingTheThreeResources, TestTemplatesWithASharedPrefixDoNotShadowEachOther, TestAPathReferenceIsPercentEncoded |
| D-27 | A create carries a pre-generated id where Drive is known to take one, so a retry cannot make two | accepted | TestCreateFileCarriesAPreGeneratedIDWhereDriveTakesOne, TestGeneratedIDsGoOnlyWhereDriveTakesThem, TestACreateThatCannotCarryAnIDIsNeverRepeated, TestCreatingASharedDriveTwiceWithOneRequestIDMakesOneDrive |
| D-28 | Repeatability follows the HTTP method, and a write that may have landed is `[ambiguous_outcome]` | accepted | TestRepeatabilityDefaultsToTheSafeAnswer, TestARateLimitIsRetriedWhateverTheRequestIs, TestASharingWriteIsNeverRepeatedAfterAnAmbiguousFailure, TestOnlyAWriteThatMayHaveLandedIsAmbiguous, TestOpeningAResumableSessionIsRetried, TestActivityQueryIsTreatedAsARead |
| D-29 | A result says what Drive's answer carried, never an outcome built from the request | accepted | outcomes, TestTheGateCatchesTheMistakeItWasWrittenFor, TestEveryBooleanInputIsInScope, TestTheExemptionRecordIsHeldToTheBranches, TestLockFileReportsWhatDriveDidRatherThanWhatWasAsked, TestRestrictReportsOnlyWhatActuallyChanged, TestMoveFileSaysWhenDriveAnswersOtherThanPredicted |
| D-30 | A tool never sleeps and retries; where Drive's reads lag its writes, the result says so | accepted | TestAFreshlyCreatedDriveIsReachableByID, TestManageRevisionExplainsAMissingRevision |
| D-31 | The writes that move, share, trash or destroy take `dry_run`, which sends nothing | accepted | TestADryRunOfOneFileCopiesNothing, TestDryRunSharesNothing, TestDryRunChangesNoDrive, TestMoveSeveralDryRunListsEachOutcomeAndMovesNothing |
| D-32 | A create refuses a same-named sibling unless `allow_duplicate` | accepted | TestCreateRefusesADuplicateNameUnlessAsked, TestCreateDriveRefusesASecondOfTheSameName |
| D-33 | Replacing a Markdown file keeps the comments Google Docs wrote into its text | accepted | TestUpdateContentRefusesToLoseAMarkdownFilesComments, TestUpdateContentRefusesAMarkdownFileSavedWhileItWasChecked, TestUpdateContentNamesASaveItReplaced, FuzzScan |
| D-34 | One registry says what each media type is called, filtered, exported and read as, and is asked before the platform's tables | accepted | TestTypeByExtensionAnswersTheSameOnEveryMachine, TestEveryTypeAppearsOnce, TestUploadFileTypesAMarkdownFileTheSameOnEveryPlatform |
| D-35 | Google's sharing policy is the authority: the server checks `canShare` first and offers sharing `all` or `off` | accepted | TestShareRefusedWhenDriveSaysThisAccountCannotShare, TestSharingOffRemovesOnlyTheToolsThatWiden, TestSharingRejectsTheModeThatWasDropped, TestSharingOffKeepsDownloadsAndLimitedAccessTight, TestSharingOffKeepsRestrictionsOn, TestAPolicyRefusalComesBackAsBlockedWithGooglesOwnWords |
| D-36 | A sharing result shows who could reach the item before and after | accepted | TestShareFileGrantsAndShowsExposureBeforeAndAfter, TestTheCardAfterASharingChangeShowsTheNewExposure, TestAcceptingGrantsTheRoleAskedForAndReportsExposure |
| D-37 | Notification mail is off unless asked for | accepted | TestShareFileGrantsAndShowsExposureBeforeAndAfter, TestTransferOwnershipMailsAndSaysSo |
| D-38 | An ordinary tool grants only named people and groups inside the organization | accepted | TestShareFileRefusesWhatReachesPastTheOrganization, TestAnOutsideRequestIsRefusedAndCanBeDenied, TestEachWideShareTakesOnlyItsOwnKindOfGrantee, TestAGroupGrantSaysWhoItReaches |
| D-39 | Outside is any address whose domain is not the account's, and on a personal account it is every address | accepted | TestAPersonalAddressIsAlwaysOutside, TestAPersonalAccountSharesWithNobodyThroughShareFile, TestShareFileRefusesWhenItCannotReadTheAccount |
| D-40 | Every risky variant is its own tool, never an argument | accepted | TestTheWideSharesNeedTheDestructiveFlag, TestManageDriveLeavesDomainUsersOnlyOffToItsOwnTool, TestManageApprovalLeavesApprovingToApproveFile |
| D-41 | A wide tool takes the name of what it opens, and refuses `[stale]` when it is not Drive's | accepted | TestAWideShareHoldsItsFileToTheName, TestLiftDriveRestrictionHoldsTheDriveToItsName |
| D-42 | Nothing widens by omission: a move, a copy or limited access turned off that would reach past the organization is refused, naming who | accepted | TestAMoveThatReachesAnyoneIsRefusedNamingThem, TestAMoveWhoseReachCannotBeReadIsRefused, TestACopyThatReachesAnyoneIsRefused, TestTurningLimitedAccessOffToAnyoneIsRefused, TestMoveSeveralRefusesEachItemThatReachesPastTheOrganization |
| D-43 | A new file made or uploaded into a folder open to more people stays an ordinary write | accepted | review |
| D-44 | The server asks the person nothing; per-call approval is the client's | accepted | TestNoToolAsksThePerson, TestARemovedSettingIsRefusedUnlessOff |
| D-45 | Annotations are exact: `readOnlyHint` only where nothing changes, `destructiveHint` false only on a write that only adds | accepted | TestOnlyToolsThatAddSayTheyAreNotDestructive |
| D-46 | The `requiresUserInteraction` mark is on `delete_file`, `empty_trash`, `delete_drive` and `share_by_link`, for every client | accepted | TestTheMarkSitsOnlyWhereItsPromptIsThePoint |
| D-47 | No tool writes the server's own settings | accepted | TestNoToolWritesTheServersOwnSettings |
| D-48 | Removal is the trash; permanent deletion is registered only with `GDRIVE_ENABLE_DESTRUCTIVE` and needs `confirm` | accepted | TestDestructiveToolsExistOnlyWhenAskedFor, TestDestructiveToolsRefuseWithoutTheDeployersSwitch, TestDestructiveToolsAlsoNeedConfirmOnTheCall, TestDeleteCommentIsGatedTwice, TestDestructiveToolsAreNeverAvailableReadOnly |
| D-49 | There is no bulk delete and no bulk share; `move_file` alone takes a list | accepted | review |
| D-50 | A bounded destructive call refuses a reference that names more than one resource | accepted | TestAFileIDThatNamesASiblingEndpointIsRefused, TestDeleteFileWillNotDestroyTheTopOfADrive |
| D-51 | Every error is a tool result, `[class] message`, from a closed vocabulary | accepted | classes, TestEveryDeclaredClassIsEmittedAndTheOtherWayRound, TestThePublishedVocabularyMatchesTheConstants, TestToolErrorsCarryAClass, TestErrorClasses |
| D-52 | Requests are paced per process and backed off as Google's guide asks | accepted | TestEveryAttemptTakesALimiterToken, TestBackoffStaysUnderTheDocumentedCap, TestGivesUpAfterTheAttemptBudget, TestRetriesServerErrorsAndHonorsRetryAfter, TestADailyQuotaIsRateLimitedButNotRetried |
| D-53 | Every attempt, token refresh and transfer chunk runs under a deadline | accepted | TestATokenRefreshCannotHangForever, TestLoginTimesOut, TestHTTPTimeoutBounds |
| D-54 | Caches never decide correctness, and a write evicts what it touched | accepted | TestAWriteInvalidatesTheCaches |
| D-55 | Every request reaches shared drives, and an incomplete search says so | accepted | TestSearchFindsSharedDriveFiles, TestCommentEndpointsSendNoSupportsAllDrives |
| D-56 | Scopes are `drive`, or `drive.readonly` in read-only mode, and the label and activity scopes only when those are on | accepted | TestScopes, TestEveryScopeLoginCanRequestIsInTheReadme, staleness |
| D-57 | Read-only mode registers only the reads and `download_file` | accepted | TestReadOnlyModeRegistersNoWriteTools, TestReadOnlyModeKeepsEveryListingAndNoWrite, TestWritesAreRefusedInReadOnlyMode |
| D-58 | Labels and Drive Activity are opt-in: each setting registers its tools and asks for its scope | accepted | TestLabelToolsRefuseWhenTheDeployerHasNotTurnedLabelsOn, TestActivityIsOffUntilTheDeployerTurnsItOn, TestScopes |
| D-59 | Sign-in is Google's desktop loopback flow on 127.0.0.1, a random port and PKCE | accepted | TestLoginHappyPath, TestLoginRejectsAStateMismatch, TestParseClientSecretRejectsWebClient |
| D-60 | The refresh token lives in the keyring, or an owner-only file with a warning when the keyring fails, and the environment overrides both | accepted | TestKeyringIsPreferred, TestFallsBackToFileWhenKeyringIsBroken, TestEnvironmentOverridesEverything, TestReadingTheTokenFileWarnsOnlyWhenTheKeyringIsBroken |
| D-61 | Profiles keep separate secrets, tokens and accounts, and a login rewrites what it records | accepted | TestProfilesAreSeparate, TestProfileRule, TestSaveReplacesTheFileWhole |
| D-62 | The server starts and serves without credentials, answering `[auth]` per call | accepted | smoke, TestWithoutCredentialsEveryToolAnswersAuth, TestServerRunsWithoutAService |
| D-63 | Configuration is `GDRIVE_*` with a bound flag each, read through one lookup and validated once at start | accepted | staleness, TestEnvironmentThenFlagWins, TestInvalidValuesAreReportedTogether, TestEveryVariableTheCodeNamesIsInEnvVars, TestASettingOfSpacesIsUnset, TestTheConfigurationPageIsHeldByWholeWord |
| D-64 | The released tool surface is a contract | accepted | schema-diff, schema-ack, TestATypeChangeBreaksOneWay, TestSchemaAckNeedsEveryItemNamed, TestCheckBaseline, TestABreakIsABreakOfARelease |
| D-65 | Schemas are flat: no nested objects and no arrays of objects | accepted | TestSchemasAreFlat |
| D-66 | A source change carries a CHANGELOG entry, and a release commit's sit under its untagged version | accepted | staleness, TestTheChangelogRule, TestTheChangelogRuleCountsWhatShips, TestTheChangelogRuleNeedsARelease, TestTheChangelogRuleMeasuresFromTheNewestRelease |
| D-67 | Tests run against an in-memory Drive that refuses what Drive refuses and matches no more than Drive does | accepted | TestNameContainsMatchesWordPrefixesOnly, TestFullTextMatchesWholeTokensAndPhrases, TestEveryCommentReadAsksForFields, TestApprovalsAlwaysAskForFields, TestIncludeLabelsTakesIDsAndNotAWildcard |
| D-68 | Behavior the reference does not pin down is checked against the discovery document and probed live before code depends on it | accepted | review, live |
| D-69 | Every method of the three APIs and every published field is used or left out on purpose | accepted | api-coverage, api-fields, TestTheSnapshotAndTheRecordAgree, TestFieldsProblemsCatchesAFieldGoogleAdds, TestAPIFieldsGate |
| D-70 | Green gates are not done: the live driver drives every tool against the account, and a person reads the transcript | accepted | live-cover, live, TestTheUndrivenCeilingOnlyEverGoesDown, TestARunSaysWhatItActuallySent, TestAStepThatExistsAndDoesNotRunIsNamed |
| D-71 | The live driver works only in what it made: a scratch folder, and a scratch shared drive for the destructive run | accepted | TestEmptyTrashIsNamedInOnePlace, TestAStopSignalStillCleansUp, TestOwnershipGoesOnlyToTheTransferAddress, live |
| D-72 | The live driver and the evals print only through one redacting transcript | accepted | transcript, TestEveryLinePrintedIsRedacted, TestEveryWayToATerminalIsRefused, TestAHollowExemptionIsRefused, TestNoRendererLeaksAPersonThroughTheRedactor |
| D-73 | Evals score the end state and the trace, through this server's tools alone | accepted | TestAnInventedIdIsCaught, TestWideningToolsAreAlwaysAFailure, TestEveryTasksPlaceholdersCanBeFilled, live |
| D-74 | Coverage is floored per package over a list derived from the tree | accepted | cover, coverage, TestEveryPackageUnderInternalIsMeasuredOrExemptOnPurpose, TestCoverageFailsBelowTheFloor |
| D-75 | Call counts, memory and linearity are tests; milliseconds stay in the benchmarks | accepted | TestATenThousandItemTreeStaysLinear, TestASearchPageCostsOneListAndBoundedParentLookups, TestADownloadDoesNotHoldTheFile |
| D-76 | A gate is watched failing, and a gate that reports a number has a floor or a record | accepted | review, TestTheClassGateRefusesToPassWithNothingToRead, TestParityRefusesToCompareNothing, TestTheLeakScanSaysHowMuchItReadAndRefusesNothing |
| D-77 | `make check` and CI run the same gates | accepted | parity, TestParityAcceptsThisRepository, TestParityCatchesDriftInEitherDirection, TestParityHoldsAGateCallsArguments, TestParityHoldsTheTreesTagsToAVetPass, TestParityHoldsMakeCheckToWhatAPushRuns, TestParityRefusesACommandWhoseFailureMakeIgnores |
| D-78 | CI runs the tests and the coverage floor on Linux, macOS and Windows | accepted | review |
| D-79 | Every action is pinned to a commit SHA, and every tool it installs to one exact version | accepted | pins, TestActionPins, TestPinsRejectsEveryRangeForm, TestAToolDownloadedAtLatestIsNotPinned, TestPinsNamesEachProblem, TestReadersRefuseALineBreakTheyDoNotRead |
| D-80 | A workflow starts read-only and raises a permission only in the job that needs it | accepted | review |
| D-81 | `main` is released code, changed only through a pull request, and tags are pushed one at a time | accepted | review |
| D-82 | A release is built from a tag, reproducible, signed, attested and verified from outside | accepted | release-notes, registry-publish, TestReleaseNotesTakesOnlyItsOwnVersion, TestThePublishedEntryTakesItsHashFromTheSignedChecksums, TestTheReleaseAttestsItsArchivesBundleAndChecksumFile, TestReleaseAttestProblems, review |
| D-83 | The MCP registry entry is held to the registry's own validator and published last | accepted | registry, TestTheCommittedEntryObeysTheRegistrysOwnRules, TestEveryRuleTheRegistryEnforcesInCodeIsRefusedHere, TestARegistryEntryThatBreaksTheSchemaIsRefused |
| D-84 | The bundle manifest is held to the files the packer stages, the platforms goreleaser builds and a pinned schema | accepted | mcpb, TestThePlatformsGoreleaserBuildsAreStaged, TestTheManifestsOwnDeclarationIsHeld, TestTheCommittedManifestNamesFilesThePackerStages, TestTheStampedManifestIsHeldToTheSchema |
| D-85 | The bundle asks for what a person sets in another client's configuration, every switch off by default | accepted | mcpb, TestTheBundleAsksForEverySettingNotLeftOut, TestTheBundleSettingsRefuseEachBreak, TestTheDefaultsReportEveryUnreadableDefault, TestVarsDescribeWhatBuildDoes |
| D-86 | The server confirms a write by asking the person through elicitation | rejected | — |
| D-87 | Changes arrive by a watch or push channel | rejected | — |
| D-88 | A revision is published to the web | rejected | — |
| D-89 | The server acts with domain administrator access | rejected | — |

### D-01 One stdio binary per person, signed in to their own Google account through their own OAuth client

The server is one Go binary that runs as a subprocess of the MCP client
and speaks MCP over stdio. It holds one person's refresh token and calls
Google as that person. Nothing is hosted, and there is no shared
deployment.

Each person creates their own Cloud project, enables the Drive API, and
creates a Desktop app OAuth client. Nothing deployer-specific is built
into the code, the repository or a release, so a copy of the binary
carries no one's client or token. Everything the server calls is
generally available, so no preview enrollment is needed.

The reason is reach. A `drive` token opens every file its owner can
open, and shares them. Per-person sign-in keeps every call inside that
person's own access. It keeps their token in their own keyring rather
than in a service someone else runs. A Desktop client also keeps the
redirect on the person's own machine (D-59).

Rejected: remote HTTP with a Web application client and the host's
callback, the shape of Google's own Drive MCP server, which also asks
for preview enrollment; hosting one server for many people.
Cost: each deployer creates a Cloud project and a Desktop OAuth client
before the first `login`, in the order `doctor` checks it. An External
app in Testing gets a refresh token that expires after seven days, so a
consumer account signs in again each week
([token expiration](https://developers.google.com/identity/protocols/oauth2#expiration)).

### D-02 The server stops at the file boundary: what is inside a Doc, Sheet or Slides deck belongs to that kind's own API

In scope: everything the Drive API does to a file from the outside. That
is finding it, where it sits, who can reach it, folders, shortcuts,
moves and copies, content in and out, sharing, shared drives, revisions,
the changes feed, comments and access requests, and on Workspace, labels
and approvals. Out of scope: what is inside a Google Doc, Sheet or
Slides deck, which belongs to the Docs, Sheets and Slides APIs.

A Drive server that also edited documents would be a second, worse Docs
server, and a person running both would get two tools for one act. So
`read_file` reads a Google document through Google's export and says
which API edits it: a Doc as Markdown, a Sheet as the csv of its first
sheet, a deck as plain text
([files.export](https://developers.google.com/workspace/drive/api/reference/rest/v3/files/export)).
`update_content` refuses a Google document and names the API that edits
it.

Comments are here because Drive's comment API reaches every file, and
the value is the files that are not Docs. Pinning a comment to a place
in a document is each kind's own API: Sheets, Docs and Slides each
publish an `InsertCommentRequest`
([Sheets](https://developers.google.com/workspace/sheets/api/reference/rest/v4/spreadsheets/request#InsertCommentRequest)).

Rejected: per-sheet and per-range reads, which are a Sheets API feature;
pinning a comment to a place in a document, which each kind's own API
does.
Cost: a Sheet reads as its first sheet only, and a comment made here
sits on the file. `list_comments` names, for each kind, the place a
comment pins to there and the API that pins it.

### D-03 Tool names are snake_case verb_noun, no dots, distinct from a Docs or Sheets server's names

Tool names are snake_case verb_noun with no dots, such as `get_file`,
`search_files` and `share_by_link`. Claude Code prefixes each with
`mcp__<server>__`.

A person runs this server beside servers for Docs and Sheets, and those
name their tools `search_documents`, `export_document` and `read_range`.
A model choosing between two tools whose names say the same thing picks
one by guesswork. So every name here says what it acts on in Drive's
words, a file, a folder, a drive, a revision, and none collides with a
document server's: `search_files` and `download_file` here, where
google-docs-mcp says `search_documents` and `export_document`. A verb
first reads as an act, which is what a client's approval prompt shows.

A name is chosen once. A released tool name is part of the surface
contract and changes only in a major version (D-64), so a name has to be
right before its first release rather than after.

Rejected: dots in a name; a generic name that a Docs or Sheets server
already uses.
Cost: names run longer than generic ones, and a name that turns out to
be wrong stays until the next major version. No test checks the shape of
a name, so review holds it.

### D-04 The server talks only to Google's hosts over HTTPS, checked with the port before a credential is attached

The server sends a credential over HTTPS to Google's hosts and to
nothing else: `www.googleapis.com` and the other `googleapis.com` hosts,
`accounts.google.com`, `drive.google.com`, and the
`googleusercontent.com` hosts a long-running download's link points at.
`internal/gapi` checks every URL before a credential is attached: the
scheme is `https`, the host is on the list, and the URL carries no port.
A redirect or a wrong base URL cannot carry an access token anywhere
else.

The reason is what a token reaches. A `drive` token reads, shares and
deletes every file the account can reach, so a request that goes astray
with it attached is a breach, not a bug. The port matters because some
URLs come from a response body rather than from this code, such as a
revision's export link. A port is refused rather than stripped, since a
well-known host on a port somebody else listens on is not Google.

There is no telemetry and no update check. The server has no reason to
reach anything Google does not run.

Rejected: stripping the port before matching the host; telemetry and
update checks.
Cost: the allowlist is not a setting, so no configuration points the
server at a proxy or a test endpoint. Tests reach the in-memory Drive
through an `AllowURL` given in code.

### D-05 Stdout carries only MCP JSON-RPC frames

MCP's stdio transport says the server "MUST NOT write anything to its
`stdout` that is not a valid MCP message", and "MAY write UTF-8 strings
to its standard error (`stderr`) for logging purposes"
([transports](https://modelcontextprotocol.io/specification/2025-11-25/basic/transports)).
A stray print corrupts the JSON-RPC stream, and the client stops working
without saying why.

So logs go to stderr through `slog`. `main` names the process's streams
once and passes them down as `io.Writer`. forbidigo refuses `fmt.Print*`
and `os.Stdout` everywhere else, with `scripts/` excluded; its rules
carry their own messages, so a settings block that fails to load shows
up as forbidigo's default message.

The smoke gate drives the shipped binary over stdio and fails on any
line that is not a frame. `--version`, `--dump-schemas` and
`status --json` write to stdout because they do not serve, and
`status --json` writes one object and nothing else. The Linux launcher
in the Claude Desktop bundle `exec`s the binary rather than calling it,
and writes any failure to stderr.

Rejected: holding it by discipline rather than a linter and a test; a
launcher that runs the binary as a child or writes a failure to stdout.
Cost: only `main` names the process's streams, and everything else takes
an `io.Writer`.

### D-06 Logs carry truncated ids, counts and latencies, never names, paths, addresses, queries or content

A log line carries truncated ids, counts, byte counts, latencies and
error classes. It never carries a file or folder name, a path, an
address, a search term or content. `doctor` and `status` print the
account to the terminal, never to the log.

A Drive holds contracts, payroll and medical letters, and a file's name
often says which. A log somebody pastes into a bug report has to be safe
by construction rather than by their care. A search term travels in a
request URL, and a transport failure renders the whole URL, so `gapi`
strips the URL from a transport error before anything copies its text. A
token failure carries no query for the same reason.

An id is cut to a six-character prefix. A retry, its backoff and its
outcome are separate log lines, and without a key that ties them a
failure cannot be traced to its call. Six characters of a long random id
cannot be looked up and identify nothing on their own.
`TestLogsCarryNoTraceOfWhatWasTouched` drives the whole surface at debug
level with fixtures nobody would write by accident: names, addresses, a
domain, a comment, a reply and an access request's message. It fails if
one reaches the log, and asserts the HTTP methods that reached the fake.

Rejected: whole ids; no id at all, which leaves a retry untraceable to
its call.
Cost: a six-character prefix at debug level is the only key that ties a
log line to a call, and a transport error's text is reduced to the kind
of failure it was.

### D-07 Nothing from a real Drive, account or deployment enters the repository, and two scanners hold it

The repository holds no organization name, file, folder or drive id,
Drive URL, account email, Cloud project id, OAuth client id or secret,
and no name or content of a real file. That covers commit messages and
fixtures as well as code.

Two scanners hold it. gitleaks scans the staged changes in the
pre-commit hook and every commit of HEAD's history in `make gitleaks`,
which `make check` and CI run, with rules for Google client ids, client secrets and refresh
tokens on top of its defaults. `make leaks` scans every file git tracks or would
commit, and a compiled binary is a finding rather than a skip, since a
content scanner cannot read one. A URL whose host does not parse is held
as a Drive link, since no rule can say whose it is, so it may carry no
id. Every allowed id carries its reason.
`scripts/gates leaks history` reads every object the repository has
held: a blob whole, a commit or a tag from its message down.

The history scan leaves out what git writes and a person does not type.
The identity on a commit or tag is public in every repository. A
`Signed-off-by` trailer and a `Co-authored-by` trailer record the same
identity in the message
([co-authors](https://docs.github.com/en/pull-requests/committing-changes-to-your-project/creating-and-editing-commits/creating-a-commit-with-multiple-authors)).

Rejected: rewriting a public history that signed releases point into, to
remove a stray editor file that holds no secret; scanning the identity
git writes on a commit or tag.
Cost: the commit identity and those two trailers are not scanned, so a
work address there is the contributor's to avoid, and CONTRIBUTING.md
asks for a no-reply address. `leaks history` walks every object, so it
runs at release rather than in `make check`.

### D-08 Fixtures are synthetic, and a fixture id says so

Every fixture is synthetic: invented names, invented addresses at
`example.com`, and ids that say so. An id made up for a fixture carries
the word `Fixture`, so the leak scan tells it from a real one without an
allowlist entry. Drive builds its ids from random bytes and does not
produce that word by chance.

The reason is the rule above (D-07). A fixture copied from a real
listing carries a real id, a colleague's name or a customer's file name,
and a test file is read and pasted as freely as any other.
`internal/office/officetest` builds the Office and OpenDocument files
the tests and the live driver need from whatever text the caller passes,
so no real document is needed either. The renderer goldens in
`testdata/golden` are produced from those fixtures.

The scan treats an id without the marker as real, and
`TestSyntheticIdsSaySoInTheirOwnText` holds both halves: a marked id
passes, and the same id without the marker is flagged. A test that needs
an id of a real shape assembles it from parts, so the gate does not flag
its own test.

Rejected: a fixture copied out of a real listing or a real account.
Cost: a fixture id has a recognizable word in it, so a test of id shapes
builds its samples from parts, and every kind of fixture is written by
hand rather than recorded.

### D-09 Own wire types over raw REST: no generated client, and no code from another project

The server calls Google's REST endpoints with `net/http` and decodes
into hand-written wire types in `internal/gdrive`, which imports
nothing. `internal/gapi` builds the requests. The module imports no code
from any other project; its direct dependencies are the MCP Go SDK, the
OAuth and rate libraries from `golang.org/x`, and a keyring library, and
`go.mod` is the list.

The generated `google.golang.org/api` module drags in gRPC,
OpenTelemetry and the cloud auth stack for a binary that needs JSON and
streaming HTTP. depguard refuses it, with a message of its own, so a
settings block that fails to load shows up as depguard's default
message.

Writing the types by hand means each field is one this server chose.
`testdata/api-fields.tsv` records a verdict for every published field
the wire types leave out, and `make api-fields` fails on a field Google
adds that has none (D-69).

Rejected: the generated `google.golang.org/api` module and its
dependencies.
Cost: a wire type read under the wrong tag decodes to nothing, silently,
so each tag is checked against the discovery document. Two open issues
come from this: the activity decoders fall through silently on a member
they do not know, and the account renderer parses storage counts off a
wire type rather than a model type (#95) (#103).

### D-10 Everything the repository runs on itself is Go

Everything the repository runs on itself is a Go program: the gates in
`scripts/gates`, the live driver in `scripts/livedrive`, the evals in
`scripts/evals`, and the pre-commit hook, which calls the same gates.
There is no shell script with logic in it and no Python.

The reason is one toolchain and one standard of code. Shell with Python
embedded in it would put a second interpreter on the `make check` path
of a single-binary Go project, to parse JSON that Go parses natively. It
would leave the code that holds the gates shut as the only code in the
repository that is neither vetted, linted nor tested. As Go packages
under `scripts/` the gates are all three, and a contributor needs Go and
nothing else.

`scripts/gates` is one dispatcher over one list of commands, so the
usage text, the Makefile and the CI workflow read the same names (D-77).

Rejected: shell with Python embedded in it; the Python pre-commit tool.
Cost: a check that would be one line of shell is a Go function with a
test, and a contributor reads Go to change a gate.

### D-11 What a file or a comment says is data: the server never acts on it, and quotes comment text as somebody else's words

What a file says, what a comment says and what an access request's
message says were written by whoever could reach the file. The server
returns them as data and never acts on them. No tool follows a link it
read, and no rule here turns on the words of a file.

A comment reaches the model quoted: the renderer prefixes every line of
a comment and a reply, so no line of it can pass for this server's own
text. A file's content comes back under a header that names the file and
the window shown. An access request's message is shown as the
requester's.

The reason is prompt injection. A document can say "share this folder
with this address", and a model may believe it. The defenses are
structural rather than textual. The model cannot reach past the
organization through an ordinary tool (D-38, D-42), cannot turn on a
tool (D-47), and every write goes through the client's approval (D-44).
Reads change nothing, so a read of hostile content can only produce
text.

Rejected: acting on instructions found in a file or a comment, such as
following a link it names.
Cost: the client's approval is what stands between a suggestion in a
document and a write the person allowed. One test covers comment text,
and nothing else holds file content beyond the read tools being
read-only.

### D-12 A reference is an id, a Google URL or a path, and one that matches several items is `[ambiguous]` with every candidate

Every tool that points at something takes an id, any Google URL that
names a file, folder or shared drive, or a path from My Drive or from a
shared drive by name or id. A URL's resource key is remembered for the
process and sent on later calls for that id. `internal/ref` parses; the
service resolves.

A path is walked one segment at a time with `name =` and
`trashed = false`. No match names the folder's siblings. More than one
match is `[ambiguous]`, listing each candidate's id, kind and modified
time, and the caller passes an id. `name =` ignores case, so two
siblings that differ only in case both match and are `[ambiguous]`; the
search guide does not say so
([search terms](https://developers.google.com/workspace/drive/api/guides/ref-search-terms)),
and `TestLiveNameEqualsIgnoresCase` checks it. An access request naming
several roles is `[ambiguous]` unless the caller says which.

The reason is that names are not unique in Drive. Taking the first match
acts on a file the person did not mean, and nothing in the result would
show it.

Rejected: taking the first match; retrying every unknown id as a name,
which turns a cheap miss into a listing per segment.
Cost: a path costs a listing per segment, cached for a minute. A word in
the id alphabet long enough to be an id is tried as one first, and is
retried as a name under My Drive only when it holds no digit.

### D-13 The tools that read follow a shortcut; the tools that organize, share or trash act on the shortcut itself

A tool that reads follows a shortcut to its target and says so in its
header. A tool that organizes, shares or trashes acts on the shortcut
itself, and the result says "this is a shortcut to X". A shortcut part
way along a path is followed by every tool, because it is a way through
to a folder.

The reason is what the person is looking at. A shortcut is the thing
sitting in the folder, and trashing or moving it is what a person means
when they name it there. Following a shortcut at the end of a path would
make `get_file` on that path describe the target and cache the target's
id for the path, so a later `trash_file` on the same path would trash
the target.

So the walk follows only the segments before the last, and the last
obeys the tool's choice. The two answers are cached apart, so a read
that followed a shortcut cannot hand its target to a write that should
not.

Rejected: following a shortcut at the end of a path for every tool.
Cost: the same path is cached twice, once followed and once not, and a
caller who wants the target from a write tool passes the target's id.

### D-14 Every result shows where a file sits

Every file in a result carries the folder it sits in, written from the
root: `My Drive/Projects/2026`, a shared drive by name, "Shared with me"
for a file whose folder this account cannot see. Names are not unique in
Drive, and a file's meaning depends on where it sits.

A location is built by climbing the parents, with each folder read once
and cached. Where the climb runs out of budget, or a parent cannot be
read, the gap is shown where it is: `My Drive/…/2026`. A cut path with a
trailing ellipsis would say that 2026 sits directly under My Drive,
which is the wrong claim when the unknown part is the ancestors.

A search page needs its hits' parents, which a listing does not carry,
so it looks up a bounded number of distinct parents and shows ids past
that. `TestASearchPageCostsOneListAndBoundedParentLookups` holds the
count of calls.

Rejected: a trailing ellipsis on a cut path, which places the known
folder directly under the root.
Cost: a card costs a read per folder above the file, cached, and a
search page with hits in many folders shows some locations as ids.

### D-15 A file is described as the Drive UI shows it: kind, owner, sharing and what the account may do, in words

`get_file` returns a card in the words the Drive UI uses: the kind
("Google Sheet", "PDF", "shortcut to a Google Doc"), the owner, the
location, a sharing summary, link state, and what the signed-in account
may do. It also gives size and times as people read them, who modified
it, and the export formats a Google document offers. One `files.get`
with a fixed field list gives it, with a `permissions.list` for a
shared-drive item.

The model decides what to do from this card, so it says what a person
would see. What the account may do comes from `capabilities`, which
Drive computes
([files](https://developers.google.com/workspace/drive/api/reference/rest/v3/files)).
A file in a shared drive with no grants of its own reads as reachable by
everyone in the drive, never as private. A Google-native document's
`size` is metadata Drive keeps rather than anything a person can get, so
it is left out.

`list_drives` says what the account may do on each drive, because the
`Drive` resource carries capabilities and no role.

Rejected: naming a shared-drive role from capabilities, which would be a
guess shown as a fact; showing a Google document's `size`.
Cost: an open question: a Google Form's card may under-report who can
reach it. `ListPermissions` never asks for the published view, so a
Form's responders may be missing; an open issue compares the card with
`includePermissionsForView=published` (#93).

### D-16 Every listing is one page per call, budgeted, and says where it stopped and how to continue

Every listing returns one page per call and ends with a footer: a note
on what was left out, Google's incomplete-search warning when it fires,
and the token or `continue_from` that continues it. `list_folder` with
`recursive` walks breadth first under a depth and an item budget, and
names the folders it did not enter.

The reason is cost on both sides. A list costs Drive twenty times a read
in quota units
([limits](https://developers.google.com/workspace/drive/api/guides/limits)),
and a client truncates a long tool result. A listing that ran to its end
in one call would spend both without the model asking for it.

The footer is written whether or not the page had rows. Drive answers
empty pages that still carry a `nextPageToken`, and partial pages before
the end
([files.list](https://developers.google.com/workspace/drive/api/reference/rest/v3/files/list)).
An incomplete search that matched nothing is where the warning matters
most. So an empty page is never the end unless it carries no token.

Rejected: leaving out the footer on an empty page; a listing that runs
to its end in one call.
Cost: a model pages through a large folder one call at a time, and the
tool descriptions state the cost of a listing.

### D-17 A continuation token continues only the query it came from

A next page's token wraps Drive's with a digest of the request: the
query, which holds every filter and any folder set, the order, the drive
and the corpus. A continuation whose request differs is refused, and so
is a token this server did not give out. With `under_folder` the token
also carries the folder and a digest of the folder set, and a
continuation over a set that changed since the first page is refused.

Drive says a token "is typically valid for several hours" and to start
over when it is rejected. It does not say the token is bound to the
query
([files.list](https://developers.google.com/workspace/drive/api/reference/rest/v3/files/list)).
Sent beside a different query, it could continue a listing the caller
did not ask for, and the result would read as page two of the other
query.

Rejected: passing Drive's token through beside whatever filters the next
call carries.
Cost: a continuation under another filter, order or scope is refused,
and the caller starts again. A folder set kept a few minutes is walked
again once it expires, and a changed set refuses the continuation.

### D-18 Search takes typed fields and says that Drive matches word prefixes, not substrings

`search_files` builds the Drive query from typed fields: `name`, `text`,
`kind`, the folder or drive, owner, visibility, who it is shared with,
dates and order, with `raw_query` ANDed in for the rest. The service
escapes every literal. The tool's description, the server instructions
and an empty result say how Drive matches.

`name` matches when each of its words begins some word of the file's
name, in any order. `text` searches by whole words, and Drive's partial
matching of content is undocumented. Drive does no substring search: a
search for the middle of a word finds nothing. The search terms guide
documents `contains` without saying either
([search terms](https://developers.google.com/workspace/drive/api/guides/ref-search-terms)).
`TestLiveSearchIsPrefixNotSubstring` checks it against the account.

A property search needs a key and a value, since Drive refuses the
key-only form the search guide gives as its own example.

Rejected: a substring search, which Drive does not do; describing
`contains` as the reference words it.
Cost: no substring search, and no filter for "shared outside the
organization", which Drive's query language has no term for. A model
looking for part of a word lists a folder instead.

### D-19 A tree operation refuses a tree over its budget before it starts, rather than doing half

An operation over a folder tree counts the tree before it changes
anything, and refuses one past its budget with the count. `copy_file`
with `recursive` walks the tree first, and refuses before any write when
it is too big or a folder inside it cannot be listed. `under_folder`
search walks the folders below its folder first, and refuses a tree past
its folder budget before searching.

The reason is that a half-done tree is worse than none. A copy that
stops at its budget leaves a partial tree that looks finished, and a
search over part of a tree reports "nothing found" for files in the part
it skipped. Drive cannot recurse in one call, so the walk is the
server's, and the budget is known before the first write.

The folder budget for a search keeps the query's length well under what
Drive accepts, which no reference states.
`TestATreeOverTheBudgetIsRefusedBeforeAnythingIsCopied` holds the copy.

Rejected: copying half a tree and reporting what was left; searching
part of a tree.
Cost: a folder-tree search covers a bounded number of folders, and a
larger tree is searched folder by folder. An open issue covers speed: a
tree copy and a tree walk visit one item at a time, and a fan-out per
level needs the shared budget made safe and a fake that can be slow
(#99).

### D-20 `read_file` fetches only the byte window it shows, and has no size limit

`read_file` returns text in windows: `offset` and `max_chars` choose the
window, and the header names `continue_from`. For a blob it sends a
`Range` header covering only the window shown, so the head of a large
log is one small request. There is no size limit on this path, because
the range bounds the transfer and the file's size never reaches the
process.

Drive serves a byte range of a blob through `alt=media`
([downloads](https://developers.google.com/workspace/drive/api/guides/manage-downloads)).
A response that is not a 206 for a range past the start is refused as
`[unexpected]` rather than read as the window.

A Google document has no byte range: `files.export` returns the whole
export, capped by Google
([files.export](https://developers.google.com/workspace/drive/api/reference/rest/v3/files/export)).
The server keeps the export for a few minutes, so paging through it
costs one export.

Rejected: a size limit on the read path, which would refuse a file the
range lets the tool read.
Cost: an export is capped by Google and held whole for paging, and a
document past the cap is read through `download_file` or not at all.

### D-21 Office and OpenDocument files are read locally from byte ranges, under hard bounds

An Office file, `.docx`, `.xlsx`, `.pptx` and their OpenDocument
counterparts, is read here out of its own bytes by `internal/office`.
The parser takes an `io.ReaderAt` over byte ranges with a small block
cache, so it fetches the zip's directory and the parts that carry text
and never the images beside them. Nothing goes to disk. Drive offers no
text of a blob, and converting one through Google creates a file.

Every read is bounded. The directory is counted from the end record
first, since `archive/zip` reads the whole central directory into memory
before it returns. A part's inflation ratio is checked by its declared
sizes and again by the bytes it pulls, with Apache POI's ratio as the
limit, since a declared size bounds the read from one side only. XML
parsed, one token, nesting and counts each have a cap, and a file past
one is `[unsupported]` or stops short with a note. `FuzzExtract` and
`FuzzPartXML` hold that no input panics or runs past its caps.

Rejected: converting through Google, which creates a file in the
person's Drive; trusting `archive/zip`'s declared sizes and its
directory read.
Cost: the old binary `.doc`, `.xls` and `.ppt` are refused with the ways
forward, and a heading saved by a localized Word that does not keep the
English style name reads as body text.

### D-22 `extract_text` reads a PDF or an image through a temporary Doc copy that it deletes in the same call

`extract_text` reads the text in a PDF or an image with Google's OCR.
Drive offers OCR only as part of an import into a Google Doc
([uploads](https://developers.google.com/workspace/drive/api/guides/manage-uploads)).
So the tool copies the file into a Doc and exports the copy as plain
text. It deletes the copy for good in the same call, or with `keep_copy`
keeps it and names it.

The copy goes to the root of My Drive with `ignoreDefaultVisibility`, so
it inherits nobody's access and skips an organization's rule that shares
every file created there
([files.copy](https://developers.google.com/workspace/drive/api/reference/rest/v3/files/copy)).
The delete reaches only the Google Doc that Drive's answer to the copy
named, never the source, and runs under its own deadline even when the
call was canceled. Checked by the live steps "extract_text reads the
words in a scanned PDF" and "the temporary copy extract_text deleted is
gone for good".

A Doc refuses a pre-generated id, so the copy cannot be repeated safely
(D-27). A copy Google did not confirm is `[ambiguous_outcome]`, naming
the `raw_query` that finds it by a marker unique to the call.

Rejected: putting the copy in the trash, which leaves a copy of the
person's file there for 30 days.
Cost: the delete skips the trash without the destructive flag (D-48). A
copy Google did not confirm is never repeated, and Google's OCR size
limits for a PDF or an image are not verified live.

### D-23 Files move only through one local directory, never through the context window, and unset means no transfer

Files move between Drive and the machine only through one directory the
deployer names, `GDRIVE_LOCAL_DIR`. `download_file` writes there and
`upload_file` and `update_content` read from there, and nowhere else.
Content never passes through the context window unless it is text the
model reads or writes inline. With the directory unset there is no file
transfer at all, and inline text still works both ways.

A local path is cleaned and resolved through its symlinks before the
check that it is inside, so `..` and a link out of the directory are
both refused. A download never overwrites a file: it writes a name of
its own with a short id in it, and the result names it. The directory
must be an absolute path.

The reason is reach in the other direction. A model that could read any
path could upload a file the person never meant to share, and one that
could write any path could overwrite one. Bytes that are not text also
cost tokens and help nothing in a context window.

Rejected: unregistering the transfer tools when the directory is unset,
since a missing tool tells a model nothing about why.
Cost: with the directory unset only inline text moves, and a download is
capped by `GDRIVE_MAX_DOWNLOAD`.

### D-24 Transfers stream in chunks, resume inside the call, and are checked against Drive's md5

Content streams between Drive and disk in chunks and is never held whole
in memory. `upload_file` sends a small file multipart and anything
larger as a resumable upload in chunks of a multiple of 256 KiB, each
under its own deadline
([resumable uploads](https://developers.google.com/workspace/drive/api/guides/manage-uploads#resumable)).
A cut connection is handled inside the call. The client asks the session
what it holds with `Content-Range: bytes */total`, reads the `Range` on
the `308`, and continues from there.

The `308` may name less than the chunk just sent, so the upload keeps
the current chunk and resends from wherever the session says it got to.
A `308` without a `Range` reads as nothing stored, which a proxy that
strips the header also produces, so a no-progress counter stops the
loop. The blob's md5 is compared with the one Drive returns.

A download streams `alt=media` to disk. The response headers come under
the per-attempt deadline. The body is guarded by a stall timer that runs
only while a read is outstanding, so a connection that stops sending is
cut and one that is merely slow is not. The md5 is compared when the
download is the head revision, by no revision or by its id, and a
mismatch removes the file.

Rejected: bounding a download by the per-attempt deadline, which would
abort every large file; assuming a chunk is stored whole.
Cost: an export, a converted upload and an older revision have no
checksum to compare, and the result says "not compared".

### D-25 Read tools return text only; write tools return text and JSON carrying everything the text does

A read tool returns one text block. A write tool returns a text block
and structured content, and the JSON carries everything the text says:
the file card, and for sharing the exposure before and after.

The reason is what a client shows. When a result carries both, Claude
Code shows the model only the structured form, so a read returning both
looked like metadata with the content left out. The specification makes
the text block the form every client understands
([tools](https://modelcontextprotocol.io/specification/2025-11-25/server/tools)).
A write's JSON is a record a script or a later call can read without
parsing prose, and since a client may show either half, both carry the
same facts.

`extract_text` is a write whose result is a file's text, up to a large
window. A JSON copy would double it, and the model would see only the
copy. So it returns a text block alone, as `read_file` does, and its
header names the kept or leftover copy's id and link.
`TestExtractTextReturnsTheTextAloneAndLeavesNoCopy` holds that it
returns no structured content.

Rejected: both halves on a read, since a client may then show the model
only the structured one; a JSON copy of `extract_text`'s text.
Cost: `extract_text` is the one write that returns text alone, so a
script reading its result parses the header.

### D-26 Resources carry a file's text, its card and a folder's first page, never a listing or a subscription

Three resource templates carry what a client may attach rather than call
for. `gdrive://{file}` is the text `read_file` gives, under one budget,
with the answer's media type saying which form it is.
`gdrive://{file}/meta` is the file card, and
`gdrive://{folder}/children` is the first page of a listing, both as
`text/plain`, because the renderer lays out columns and calling them
Markdown would be untrue.

A template matches through an anchored RFC 6570 pattern in which a
simple expansion excludes `/`, so `gdrive://{file}` cannot match
`gdrive://x/meta` ([RFC 6570](https://www.rfc-editor.org/rfc/rfc6570)).
A reserved expansion, `{+file}`, would match across the slash and shadow
the other two. A resource that is not there carries the protocol's
not-found code with the URI in its data, and a sentence saying what to
do next.

There is no static resource list, since that would be a Drive listing
with no budget, and no subscriptions, since Drive pushes only to a
public endpoint (D-87).

Rejected: a reserved expansion `{+file}`, which would shadow the other
templates; a static resource list; subscriptions.
Cost: a reference with a slash in it, a path or a URL, must arrive
percent-encoded, and the server instructions say to pass an id.

### D-27 A create carries a pre-generated id where Drive is known to take one, so a retry cannot make two

`files.create` and `files.copy` accept an id from `files.generateIds` in
the body, which makes a retry land on the same file
([files.generateIds](https://developers.google.com/workspace/drive/api/reference/rest/v3/files/generateIds)).
So every create, upload and copy carries a pre-generated id where Drive
takes one, and a duplicate-id answer after an ambiguous failure is
`[ambiguous_outcome]`. A recursive copy asks for the whole tree's ids in
one call. `drives.create` carries a `requestId`, kept for the retry.

The reference does not say which files refuse an id. The Docs Editors
formats refuse it with one message and a shortcut with another, and
neither is listed. So the rule names what is known to work: a folder, or
a type that is not Drive's own. A Google-native type nobody has tried
falls on the safe side. `TestLiveGenerateIDsAreUsableAndUnique` checks
the ids against the account.

A create that cannot carry an id is never repeated after a failure that
may have landed (D-28), and the duplicate-name guard catches a second
one (D-32).

Rejected: a list of the formats that refuse an id, which fails open on
the next kind nobody listed.
Cost: creating a Doc, Sheet, Slides deck, Drawing or Form is not
idempotent, and a single create spends a round trip on its id. An open
issue asks for one id source, which needs a live measurement first
(#97).

### D-28 Repeatability follows the HTTP method, and a write that may have landed is `[ambiguous_outcome]`

Whether a request may be repeated comes from its HTTP method. GET,
PATCH, PUT and DELETE mean the same thing applied twice, so they repeat
after a transient failure. A POST repeats only when the request says
why: it carries a pre-generated id, or it only reads. A POST added later
and given no thought fails closed.

A 429 or a rate-limit reason is a refusal to begin, so it is repeated
whatever the request is. A 5xx or a connection lost after sending is
ambiguous: the write may have landed. A write that cannot be repeated
then answers `[ambiguous_outcome]`, naming the call that finds out, such
as `list_permissions` after a share. A delete retried after a 5xx that
then finds nothing is ambiguous too.

Sharing is the case that matters. A grant applied after the caller
believed the call failed is exposure nobody is watching. The Drive
Activity query and `files.download` are POSTs that only read, and carry
a marker whose zero value is the safe one. Opening a resumable session
allocates a URI and nothing else, so it is marked repeatable at its call
site.

Rejected: a retry rule that reads the request's kind; a flag whose zero
value means safe to repeat; one rule for every transient failure.
Cost: a POST that only reads has to carry its marker, and a create that
cannot carry an id fails on a 5xx instead of retrying.

### D-29 A result says what Drive's answer carried, never an outcome built from the request

A result says what Drive's answer carried. A sentence about the outcome
is built from the response, a read back, or a field Drive returned,
never from the arguments the caller sent. Where the response carries
nothing, the result says so.

`files.emptyTrash` has no response body, so `empty_trash` says Drive
accepted the call and that the method reports nothing
([files.emptyTrash](https://developers.google.com/workspace/drive/api/reference/rest/v3/files/emptyTrash)).
An approval started with `lock_file` reads the lock off the file, since
Drive applies none at the start. `copy_comments` says what Drive was
asked to do and names `list_comments` on the copy as the call that
settles it. A move and a sharing write read the permissions back. An
activity with an empty action detail is counted apart from a kind the
server has no words for.

`make outcomes` holds the checkable shape. It fails a branch that tests
a boolean the caller sent and then writes a result sentence without
asking Drive anything in between. A dry run and a refusal are excluded
by the rule itself.

Rejected: reading a copy's comments back, which lags the copy and would
report threads dropped that are late; a gate rule that the field is read
in the same function.
Cost: a result says when Drive reports nothing, as for `copy_comments`
and `empty_trash`. A branch that is right anyway needs a row with its
reason in `testdata/outcome-claims.tsv`.

### D-30 A tool never sleeps and retries; where Drive's reads lag its writes, the result says so

A tool never sleeps and retries to wait for Drive to settle. Where
Drive's reads lag its writes, the result says the answer may be stale,
and where a listing lags and a `get` exists, an id reaches the `get`.

The lag is a property of Drive, not of one endpoint. It shows in the
changes feed after a write, in `drives.list` after `drives.create`, in a
shared drive's trash count, in the trash after `emptyTrash`, and in
`revisions.get` after a write. Each is recorded at the call it affects.
A shared drive is found by `drives.get` when the listing does not have
it yet, which is not lagged, so a drive just created is reachable by its
id. A revision `revisions.get` cannot find says "or not yet" and
suggests trying again.

Sleeping inside a call would hold an MCP request open for a time nothing
bounds, since nothing says how long Drive takes. Asserting a settled
state afterwards would be the outcome-from-the-request mistake again
(D-29). Saying plainly that the answer may be stale is honest, and a
model can act on it.

Rejected: sleeping and retrying inside a tool call.
Cost: a fresh revision or drive may answer "or not yet", and the caller
asks again.

### D-31 The writes that move, share, trash or destroy take `dry_run`, which sends nothing

The writes that move, share, copy, trash or destroy take `dry_run`:
`move_file`, `copy_file`, `trash_file` and `restore_file`, the sharing
tools, `resolve_access_request`, `manage_drive`,
`lift_drive_restriction` and the gated deletes. A dry run runs every
check the call would run and returns before anything is sent. It says
what the call would do, never that it did.

A dry run is how a caller sees what a call would reach before it does.
So the refusals hold under it: a move or copy that would reach past the
organization is refused `[blocked]` under a dry run too (D-42), and so
is a wide share whose name does not match (D-41). A dry run of a move
reports `would_move` for each item. Each tool reads the flag itself and
returns before its first write. Tests of a copy, a share, a drive change
and a batch move hold that a dry run sends nothing.

The creates, uploads, content updates and the comment tools take no
`dry_run`, and nothing in the client refuses a write under one.

Rejected: a dry run that skips the checks, and so answers yes for a call
the real one refuses.
Cost: each tool reads the flag itself, so only a test of that tool shows
its dry run sends nothing, and not every tool has one. An open issue
proposes `dry_run` on every write, with a test per tool (#83).

### D-32 A create refuses a same-named sibling unless `allow_duplicate`

`create_folder`, `create_file` and `upload_file` refuse a name that a
sibling in the destination already has, unless `allow_duplicate: true`.
The refusal is `[exists]` with the sibling's id. `manage_drive` refuses
a second shared drive of the same name.

Drive allows two items of one name side by side, and each looks like the
other in every listing. The common way to get two is a lost result: a
create lands, the answer is lost, and the model calls again. A create
that carries a pre-generated id collapses the retry inside the client
(D-27), but the model's own second call is another request. A Google
document cannot carry an id at all, so this guard is the only thing
between a retried create and a twin.

The check is a listing of the destination for the name, so it costs one
list per create.

Rejected: leaving duplicate names to the model, which cannot tell a twin
from the original afterwards.
Cost: one listing per create, and a caller who wants two of a name says
so with `allow_duplicate`.

### D-33 Replacing a Markdown file keeps the comments Google Docs wrote into its text

Google Docs opens a Markdown file stored in Drive and edits it in place,
and writes the comments made there into the file's own text. It writes a
begin and an end marker around the passage, and an entry under the
comment's key in a `gdoc_comments` block that must end the file.
`comments.list` does not return them, and where they are kept is
published nowhere
([Docs help](https://support.google.com/docs/answer/18289341)). A
replacement that drops one deletes it for good.

So before `update_content` replaces a Markdown file, it reads the file
and the replacement as streams through `internal/mdcomment`. It refuses,
as `[invalid]` naming each comment, content that drops an entry or a
marker, changes an entry's status, or goes on after the block. It also
refuses a `mime_type` that is not Markdown, which Drive would make the
file's type. A `text/x-markdown` file is checked the same way. Checked
by the live step "new content that loses a kept comment is refused".

A person editing the file in Docs saves a few seconds after each change.
So `update_content` looks at the head revision once more just before the
upload and refuses `[exists]` if it moved, and afterwards names any save
that landed in between.

Rejected: treating a `.md` file as a blob whose bytes may be replaced
whole.
Cost: a save between the last look and the upload is replaced and only
named, and `allow_comment_changes` skips the check and the last look.

### D-34 One registry says what each media type is called, filtered, exported and read as, and is asked before the platform's tables

`internal/mediatype` is one table keyed by media type: the display name,
the kind filter, the export name, the format a read takes, the format a
download defaults to, and the extensions that name it. Adding a kind is
one edit. It is asked before Go's `mime` package, so `.md`, `.csv` and
`.docx` give the same type on every machine.

Go's built-in table has no `.md`, and the system's own tables are loaded
over it: `/etc/mime.types` and others on Unix, the registry on Windows.
So the same file name could upload as `text/markdown` on one machine and
`text/plain` on another, and a `text/plain` `.md` is a file the Docs API
refuses to read. RFC 7763 registers `text/markdown` for `.md` and
`.markdown` ([RFC 7763](https://www.rfc-editor.org/rfc/rfc7763)).
`create_file` takes its text type from the name the same way.

Rejected: the platform's media-type tables, which answer differently
from machine to machine.
Cost: `download_file`'s format list is written out in its schema and
held to the table by a test. An open issue covers the readable kinds,
which are listed in the table and again in the read plan (#101).

### D-35 Google's sharing policy is the authority: the server checks `canShare` first and offers sharing `all` or `off`

Google decides what may be shared. The administrator's external-sharing
setting and a shared drive's restrictions are enforced by Drive on every
call, and this server does not copy them. Before a sharing write it
reads `capabilities.canShare` and refuses `[forbidden]` when it is
false, as the sharing guide asks
([sharing](https://developers.google.com/workspace/drive/api/guides/manage-sharing)).
A refusal from the policy comes back `[blocked]` with Google's own
words.

What the server adds is brakes on the agent doing something the person
is allowed to do but did not ask for. The deployer sets `GDRIVE_SHARING`
to `all`, the default, or `off`. Off leaves the sharing tools
unregistered, keeps `list_permissions`, and refuses a looser shared
drive, a looser file switch, and a move or a copy that more people could
reach. `invalidSharingRequest` is a policy refusal only at 403; at 400
it is `[invalid]`, with Google's message saying how to fix the call.

Rejected: an `internal` mode that copies the administrator's rules,
which Google already enforces per organizational unit.
Cost: an open issue covers the prelude: the sharing writes each check
`canShare` and read the exposure before acting, written out by hand, so
the order is held by memory. One helper returning the resolved file and
the exposure before would hold it (#102).

### D-36 A sharing result shows who could reach the item before and after

Every sharing result, and every move and copy, shows who could reach the
item before and who can after. The text says it in words, and the JSON
carries the permission id and both summaries. A principal that already
has a grant is updated, and the result shows the change, such as
`can view → can edit`.

A share is the one write whose harm is not visible in the item itself. A
person reading "shared" cannot tell whether a file went from three
colleagues to four, or from private to anyone with the link. Before and
after makes the change the thing reported.

The after is read back from Drive, never built from the request.
`accessproposals.resolve` answers with no body at all, so what accepting
a request did can only be read back
([accessproposals.resolve](https://developers.google.com/workspace/drive/api/reference/rest/v3/accessproposals/resolve)).
A card read after a sharing change shows the exposure after, since the
caches the change touched are evicted (D-54).

Rejected: reporting an acceptance from the request, since `resolve`
returns nothing to read.
Cost: a sharing write reads the permissions twice, and an open issue
notes it builds the file's model twice, one round trip more than it
needs on a shared-drive item (#100).

### D-37 Notification mail is off unless asked for

Every grant sends `sendNotificationEmail` false unless the caller passes
`notify: true`. Google's default is true for a user or a group, and the
parameter is not allowed on a `domain` or `anyone` grant
([permissions.create](https://developers.google.com/workspace/drive/api/reference/rest/v3/permissions/create)),
so it is omitted there.

The reason is that a mail is a side effect the person cannot take back.
A model sharing a file to make a task work should not put a message in a
colleague's inbox that the person never wrote or saw. When the person
wants the colleague told, `notify` and `message` say so.

Some acts mail regardless, and the result says when. Google requires the
mail on an ownership transfer, so `transfer_ownership` mails and says it
did. Every approval verb mails somebody, with no notify flag in the API,
and `manage_approval`'s description and every result say so. An address
with no Google account behind it can only be invited with the mail, and
Google's refusal says so.

Rejected: Google's default of mailing users and groups.
Cost: an ownership transfer and every approval verb mail regardless of
the caller, and no test asserts the default by name.

### D-38 An ordinary tool grants only named people and groups inside the organization

`share_file` grants named people and groups inside the organization, and
nothing else. An address outside it, `anyone`, a `domain:` grant and the
role `owner` are refused `[blocked]`, naming the tool that does each
(D-40). `resolve_access_request` accepts a request from inside the
organization as an ordinary grant, and refuses one from outside, naming
`share_outside_organization` or Drive itself. Denying is ordinary.
`unshare_file` stays ordinary, since it narrows.

The line is drawn at the organization because sharing outside it is the
exfiltration path, and a share to a colleague is routine. A request and
its message come from the person asking, which under injection is the
attacker. A group is shared with as a group, and Google decides each
member's access when they open the item. So a grant to a group reaches
everyone in it, including people added later, and the result says so.

Rejected: accepting an outside request as an ordinary grant; putting
every grant inside the organization behind the destructive flag.
Cost: a group grant reaches members added later, and outside members the
policy lets in. A colleague's address under injection is still one
somebody named. Open issues: whether any readable field shows a group
that admits outside members (#85); narrowing a wide grant needs the wide
tool (#87); one rule for "past the organization" (#89); and
`list_access_requests` stopping at an empty page that carries a token
(#104).

### D-39 Outside is any address whose domain is not the account's, and on a personal account it is every address

Outside means an address whose domain is not the signed-in account's. A
secondary domain of the same organization counts as outside, and so does
every address when the account's own cannot be read. `share_file` says
which. A personal Google account has no organization, so every address
is outside it, and `share_file` grants nobody there; `get_account` and
the refusal say so.

The rule is conservative on purpose. Sharing with an outsider is the
exfiltration path, and a rule that errs should err toward refusing, with
the outside tool still there for a person who turns it on (D-40). The
account's domain comes from `about.get`, so no setting is needed. When
the account's address cannot be read, a person cannot be told inside
from outside, and a move's refusal says that rather than calling them
outside.

Rejected: counting a personal account's mail provider as its
organization.
Cost: a secondary domain of the same organization counts as outside. An
open issue covers a personal account at another mail provider: the
server knows only Gmail's domains as personal, so that provider's
addresses count as inside. Deciding "personal" from `about.get`, or a
setting naming the organization's domain, would close it (#86).

### D-40 Every risky variant is its own tool, never an argument

Every risky variant of an act is a tool of its own, never an argument on
an ordinary tool. A link for anyone or a domain is `share_by_link`,
someone outside the organization is `share_outside_organization`, and
handing a file to another owner is `transfer_ownership`. Turning a
shared drive's `domain_users_only` off is `lift_drive_restriction`. Each
is registered only under `GDRIVE_ENABLE_DESTRUCTIVE` and needs
`confirm: true`. Approving is `approve_file`, and `manage_approval`
refuses `approve`, naming it.

A client remembers an approval per tool, never per argument value. A
wide form carried as an argument rides along unseen once its tool is
always allowed, and a client's always-allow does not look at argument
values. As its own tool, a wide form gets its own prompt, its own
annotation and its own registration switch.

`manage_drive` refuses `domain_users_only` off `[blocked]`, naming the
tool, and keeps the other switches, which turn on a limit or narrow.

Rejected: `allow_anyone`, `allow_domain`, `transfer_ownership` and
`discoverable` as arguments of `share_file`; `approve` as an action of
`manage_approval`.
Cost: an open question: turning `members_only` off stays an ordinary
`manage_drive` switch until a live probe shows whether it restores
access already granted to people who are not members (#84).

### D-41 A wide tool takes the name of what it opens, and refuses `[stale]` when it is not Drive's

Each wide share takes `file_name`, and `lift_drive_restriction` takes
`drive_name`: the item's name as Drive has it. The server compares it
exactly with the name of the item the reference resolved to, and refuses
`[stale]` when they differ, dry run included. A shared drive's root also
answers to the drive's own name.

A client's approval prompt shows the call's arguments, and a name is
what a person recognizes there; an id says nothing. With the name in the
call, the prompt names what the share opens, and the server holds the
call to it. A model that resolved the wrong item, or an item renamed
since it was read, is refused before anything is granted.

`[stale]` says the witness does not match what Drive has, as the sibling
servers use the word. The caller reads the item again rather than
correcting the argument blind.

Rejected: an id alone, which says nothing in a client's prompt.
Cost: a rename between the read and the call refuses the share, and the
caller reads the item again. The comparison is exact, so a name typed
from memory with different spacing or case is refused too.

### D-42 Nothing widens by omission: a move, a copy or limited access turned off that would reach past the organization is refused, naming who

A move, a copy and a folder's limited access turned off each reach
whoever the destination reaches, and that is nowhere in the call. Each
is refused `[blocked]`, dry run included, when what it would add
includes anyone with the link, a domain, or a person or group outside
the organization. The refusal names each by address, domain or link, and
the same refusal comes when who it would reach cannot be read. A gain
only to people and groups inside the organization is an ordinary write,
with before and after in the result.

Moving an item "re-evaluates and applies the new parent's permissions to
the item and its children"
([sharing](https://developers.google.com/workspace/drive/api/guides/manage-sharing)).
So the server reads the item's permissions and the destination's, and
works out the after: direct grants stay, inherited ones go, the
destination's arrive. A copy takes its destination's grants and none of
the original's. Checked by the live step "a move that would reach anyone
with the link is refused".

Rejected: a "wide move" tool, since sharing the item first through the
wide tool leaves the move nothing to add.
Cost: a move or copy reads both permission lists first. The
destination's owner counts as an editor, since what Drive gives them is
undocumented. A moved limited-access folder's own setting is not used,
so the prediction may refuse one move too many.

### D-43 A new file made or uploaded into a folder open to more people stays an ordinary write

`create_file` and `upload_file` into a folder shared with anyone, or
with people outside the organization, stay ordinary writes. They are not
held to the rule that refuses a move or a copy there (D-42).

The difference is whose content it is. A create or an upload puts
content the caller wrote where the caller named. Nothing that fewer
people could reach before becomes reachable by more, the way a post in a
space reaches its members. A move or a copy takes existing content,
which someone else may have written for fewer people, to a place whose
reach is nowhere in the call. That is exposure of something that was
narrower, which a create cannot produce.

The client's approval of a create still shows the destination, and the
result shows the created file's location and who can reach it.

Rejected: holding a create and an upload to the move rule.
Cost: content the caller wrote reaches whoever the folder reaches,
without a refusal. No test pins that a create into an open folder stays
ordinary, so review holds it.

### D-44 The server asks the person nothing; per-call approval is the client's

The server never asks the person a question. Per-call approval is the
client's: it draws its prompt from the annotations and its own rules,
and the server adds none of its own. The test client declares
elicitation, and `TestNoToolAsksThePerson` drives every registered tool
on a protocol that asks either way, failing on any question.

What holds on every client stays, and is the safety this server counts
on: the registration flags, `confirm: true` on the gated tools, the
witnesses and guards in the service, and `dry_run`. Every widely used
client has a mode that runs a registered tool with nobody approving the
call. So a tool that must not run unattended is not registered, and the
README gives the ask rules for the writes that share, sign off or
delete.

`GDRIVE_REQUIRE_PROMPT`, the removed setting that refused the asking
writes, is refused at start when set to anything but `false`. Ignoring
it would quietly allow what its owner set it to stop.

Rejected: confirming a write by asking the person (D-86); ignoring a set
`GDRIVE_REQUIRE_PROMPT`.
Cost: a client running with nobody watching makes every registered
write, so `GDRIVE_ENABLE_DESTRUCTIVE` stays off for one.

### D-45 Annotations are exact: `readOnlyHint` only where nothing changes, `destructiveHint` false only on a write that only adds

The annotations are exact, because they decide the client's prompt.
`readOnlyHint: true` sits only on a tool that changes nothing, and
`destructiveHint` is false only on a write that only adds:
`create_file`, `create_folder`, `create_shortcut`, `upload_file`,
`copy_file`, `add_comment`, `download_file`, and `extract_text`, which
removes only the copy it made. Every other write says true.

The MCP schema defines `destructiveHint` false as "the tool performs
only additive updates", with true as the default, and `readOnlyHint` as
"the tool does not modify its environment"
([ToolAnnotations](https://modelcontextprotocol.io/specification/2025-11-25/schema#toolannotations)).
So trashing, moving, restoring, renaming, unsharing, a drive's settings,
a grant's role and an approval's reviewers all say true, though each can
be set back and none is gated. `download_file` writes a local file, so
it is not read-only. Codex prompts by `destructiveHint`, claude.ai
documents that it decides its prompt, and VS Code prompts on every tool
without `readOnlyHint`.

Rejected: a reversible write saying `destructiveHint: false`.
Cost: claude.ai and Codex prompt on writes that can be set back, and VS
Code prompts on `download_file`, which changes nothing in Drive.

### D-46 The `requiresUserInteraction` mark is on `delete_file`, `empty_trash`, `delete_drive` and `share_by_link`, for every client

`_meta["anthropic/requiresUserInteraction"]` is set on these tools, for
every client: `delete_file`, which skips the trash; `empty_trash` and
`delete_drive`, each an unrecoverable delete of a whole container; and
`share_by_link`, a public or domain-wide share. No other tool carries
it.

Claude Code prompts on every call of a marked tool, whatever the allow
rules say, and refuses it where no prompt can be drawn. Anthropic scopes
the mark to "tools whose permission prompt is itself the point"
([Claude Code MCP](https://code.claude.com/docs/en/mcp)). These tools
are that: each removes or exposes something nobody can take back, and
each is rare. A mark on a delete done many times in a row makes a person
answer a prompt per item, and people stop reading prompts they see that
often.

Only Claude Code reads the mark, so it is a hint. The control is that
the tools are not registered unless the deployer turns on
`GDRIVE_ENABLE_DESTRUCTIVE`.

Rejected: the mark on `delete_revision` and `delete_comment`, a prompt
per item; the mark only for a client without elicitation, and a typed
confirmation; the mark on `transfer_ownership`, which is flag-gated and
needs `confirm`.
Cost: only Claude Code reads it, so the flag that registers the tool
stays the control.

### D-47 No tool writes the server's own settings

No tool writes the server's own settings. They come from the `GDRIVE_*`
environment and the bound flags, read once at start, and from the
profile `login` writes. Nothing a tool call carries can turn on the
destructive tools, leave read-only mode, turn sharing back on, point the
local directory somewhere else, or change which account is used.

The reason is that the model must not widen its own reach. Each setting
is a decision the deployer made about what a model may do with this
account, and a model persuaded by a file it read would otherwise have a
way to undo it. A setting that a tool could change would also make the
client's approval of that tool an approval of every tool the change
registers.

`TestNoToolWritesTheServersOwnSettings` drives every registered tool and
holds the environment and the profile unchanged after each call, so a
tool added later is covered on the commit that adds it.

Rejected: a tool that changes a setting at the model's request, such as
turning on the destructive tools for one task.
Cost: changing a setting means editing the client's configuration and
restarting the server, and a person does that by hand.

### D-48 Removal is the trash; permanent deletion is registered only with `GDRIVE_ENABLE_DESTRUCTIVE` and needs `confirm`

Removal is the trash. `trash_file` and `restore_file` are always
registered; the result names the item, says "folder with its contents"
for one, and reminds that the trash empties after 30 days. Removal that
skips the trash, `delete_file`, `empty_trash`, `delete_drive`,
`delete_revision` and `delete_comment`, is registered only under
`GDRIVE_ENABLE_DESTRUCTIVE=true`, and each call needs `confirm: true`.
Read-only mode never registers them.

`files.delete` skips the trash and cannot be undone, and so does
`files.emptyTrash`
([delete](https://developers.google.com/workspace/drive/api/guides/delete)).
A model can be wrong about which file it means, and the trash is what
makes that recoverable. The flag is the deployer's decision that this
model may destroy, and `confirm` is the call's own statement that it
means to. A tool that is not registered cannot be called by a client
that ignores the annotations, which the specification treats as
untrusted hints.

`TestDestructiveToolsAlsoNeedConfirmOnTheCall` holds that the flag alone
is not enough.

Rejected: registering a destructive tool with a warning attached.
Cost: `extract_text` deletes its own temporary copy for good without the
flag, since the copy was never the person's (D-22). A deployer who wants
permanent deletes turns them on for every call of the session.

### D-49 There is no bulk delete and no bulk share; `move_file` alone takes a list

There is no bulk delete and no bulk share. Each removal and each grant
is one item per call, so "remove these forty files" is forty approvals
in the client. A folder is the unit for bulk work, and trashing one is
reversible.

`move_file` is the exception: it takes `files`, a bounded list, to one
destination. A move can be moved back, and the part that cannot be taken
back, who saw the item meanwhile, is refused item by item whenever it
would reach past the organization (D-42). Every item is read and checked
before anything moves.

Each item gets its own outcome: `moved`, `unchanged`, `refused` with the
reason, or `failed` with Drive's answer. A failure leaves the moves
before it in place and does not stop the ones after. A failed item is
read back, so one Drive moved anyway is reported as moved.

The reason is that an approval per removal or share keeps each one
visible to the person. A move removes nothing and grants nothing by
name, so an approval per item would be approvals of nothing.

Rejected: bulk operations in the surface; one approval per item for a
move.
Cost: removing or sharing many files is one approval each, and there is
no bulk rename.

### D-50 A bounded destructive call refuses a reference that names more than one resource

A bounded destructive call never names more than one resource. Every
path built from a file id goes through `fileSegment`, which refuses the
segments Drive uses for endpoints of its own under `/files`. `trash` is
`files.emptyTrash`, so `DELETE /files/trash` would empty a whole trash
in place of deleting one file. `delete_file` refuses `root` and a shared
drive's id, which is its root folder's id.

Escaping cannot prevent this: every character of `trash` is legal in a
path segment, so `url.PathEscape` leaves it alone. And it belongs where
the URL is made, not three layers away where a lookup happens to answer
404 first. `delete_file` does not lean on `capabilities.canDelete`
either, because that is a field the other side computes and sends.
`TestDeleteFileWillNotDestroyTheTopOfADrive` strips the capabilities off
its fixture to show the refusal does not depend on them.

The general rule: each value a reference type accepts that names more
than one resource, or a container of others, is refused at each bounded
destructive call.

Rejected: `url.PathEscape` as the guard; relying on
`capabilities.canDelete`, which Google computes.
Cost: a segment Drive adds under `/files` has to be added to the
reserved list by hand, and `make api-diff` is what shows one Google
adds.

### D-51 Every error is a tool result, `[class] message`, from a closed vocabulary

Every failure is a tool result in the form `[class] message`, never a
protocol error, and the class comes from a closed vocabulary declared in
`gapi.Classes` and aliased by `internal/service`. The class tells the
model what to do next, and the message carries the fix. Google's own
words are quoted only from its error envelope; a failure without one,
such as an HTML page from Google's front end, gives the status and what
it means.

Drive's failures need more classes than invalid, not-found, auth and
unavailable. `ambiguous` is the addressing design (D-12), `blocked` is a
refusal no argument here changes, and `ambiguous_outcome` is a write
whose result is unknown (D-28). `pending` is work Google has begun and
not finished, and `stale` is a witness that does not match Drive (D-41).
Google's reason decides the class before its status, since
`teamDrivesFolderMoveInNotSupported` arrives as a 403 and means "cannot
be done", not "may not"
([errors](https://developers.google.com/workspace/drive/api/guides/handle-errors)).
`make classes` fails on a class emitted and not declared, and on one
declared and never emitted.

Rejected: the six classes `invalid`, `not_found`, `auth`, `conflict`,
`unavailable` and `unsupported`, which would fold `ambiguous`, `blocked`
and `ambiguous_outcome` into `invalid`.
Cost: `blocked` carries two meanings, the organization's policy and this
server's rule, and the message says which.

### D-52 Requests are paced per process and backed off as Google's guide asks

Requests are paced per process: a read limiter, a write limiter, and a
tighter sharing limiter for Drive's separate sharing quota. A transient
failure backs off exponentially with jitter, floored at a quarter of
the window and capped, honoring
`Retry-After`, for a bounded number of attempts. The limiter is taken
inside the retry loop, once per attempt.

Drive charges units by method against a per-user budget, and over it
answers 403 with a rate-limit reason or 429, asking for exponential
backoff
([errors](https://developers.google.com/workspace/drive/api/guides/handle-errors)).
A retry is triggered by exactly those answers, so a limiter taken only
on the first attempt would push hardest when Drive asked for less.
`dailyLimitExceeded` is a daily project quota that backing off cannot
free, so it is classified `rate_limited` and not retried, and the
message says what blocks it.

Google writes a reason as `rateLimitExceeded` in the legacy envelope and
as `RATE_LIMIT_EXCEEDED` in a `google.rpc.ErrorInfo` detail. Reasons are
compared in both spellings, and the fake sends each in its own.

Rejected: taking the limiter on a call's first attempt only; retrying
`dailyLimitExceeded`.
Cost: the limiters are per process, so two clients each running the
server pace separately against one account's quota.

### D-53 Every attempt, token refresh and transfer chunk runs under a deadline

Every attempt runs under `GDRIVE_HTTP_TIMEOUT`, and so does every
transfer chunk and every token refresh. The whole call also runs under
the MCP request's context, so a client's cancel stops it.

A refresh runs inside the oauth2 transport, against the context the
token source was built with rather than the request's. With no client of
its own it would use `http.DefaultClient`, which has no timeout, and a
token endpoint that accepted a connection and never answered would hang
every later call. So `auth.TokenSource` and `login`'s code exchange each
run on a client with a deadline. `TestATokenRefreshCannotHangForever`
uses a listener that accepts and never answers.

A download is the exception to the per-attempt deadline, since a large
file cannot arrive inside it (D-24). Its response headers come under the
deadline, and its body under a stall timer that cuts a connection that
stops sending.

Rejected: a token refresh on `http.DefaultClient`; the per-attempt
deadline over a whole download.
Cost: a download that keeps sending runs as long as it sends, bounded in
size by `GDRIVE_MAX_DOWNLOAD` and in time only by the client's cancel.

### D-54 Caches never decide correctness, and a write evicts what it touched

The caches are short, and they never decide correctness. A resolved
`(parent, name)` pair is kept for a minute, so a burst of calls on one
path costs one walk. File metadata is coalesced for a few seconds, keyed
by id. A write evicts what it touched.

A create evicts the entry for its own `(parent, name)` whatever it did,
because a create can make a path ambiguous. A second `notes.txt` beside
the first would otherwise leave the cached path answering with the older
file's id for the life of the entry, which is the guess D-12 refuses. A
rename, a move or a trash also evicts by value. The cache key is built
in one function, so an eviction cannot spell it differently from a
lookup.

A cache only saves a round trip. Every answer it gives is one Drive gave
within its window, and a refusal, a resolution or a sharing summary
built from it is no less true than one built a call earlier.

Rejected: a create that leaves a cached path alone.
Cost: an open issue: a created shared drive is dropped from the drive
cache and looked up cold. Remembering it is right only when the cache is
already warm, since a cold cache seeded with one drive would hide the
others (#94).

### D-55 Every request reaches shared drives, and an incomplete search says so

Every request reaches shared drives. Each call carries
`supportsAllDrives`, and listings carry `includeItemsFromAllDrives` with
`corpora=allDrives` by default, or the one drive's `driveId` when the
search is inside it
([shared drives](https://developers.google.com/workspace/drive/api/guides/enable-shareddrives)).
When Google answers `incompleteSearch`, the listing says so in a line
the model can act on.

A search that leaves out shared drives returns nothing on an account
full of them, despite the full `drive` scope. A file in a shared drive
is often the one a person means, and a search that skips it answers "not
found" with confidence. `TestSearchFindsSharedDriveFiles` holds it, and
`TestLiveSharedDrives` checks it against the account.

The comment and reply methods take no `supportsAllDrives` at all, so the
comment client sends none and a test asserts it; a comment on a
shared-drive file is reached without it.

Rejected: sending `supportsAllDrives` on the comment methods, which take
no such parameter.
Cost: a search across every drive is slower than one in a single corpus,
and Google says to prefer `user` or `drive`; `drive` narrows a search to
one shared drive.

### D-56 Scopes are `drive`, or `drive.readonly` in read-only mode, and the label and activity scopes only when those are on

Full mode asks for `drive`, and read-only mode for `drive.readonly`.
`GDRIVE_LABELS` adds `drive.labels.readonly`, and `drive.labels` in full
mode. `GDRIVE_ACTIVITY` adds `drive.activity.readonly` in both modes,
since the Activity API has no write. A missing scope's 403 becomes
`[forbidden]` naming `login`. A changed scope set needs another `login`,
and `doctor` prints what the token holds.

`drive.file` is not enough for this server. It reaches only files the
app created or the person opened with it through the Picker, and a
command-line program has no Picker to show
([scopes](https://developers.google.com/workspace/drive/api/guides/api-specific-auth)).
A server that finds, moves and shares a person's existing files needs
the scope that reaches them, and read-only mode needs no more than
reading.

The README names every scope `login` can ask for, and the staleness gate
holds it to the code.

Rejected: `drive.file`, which reaches only files opened through the
Picker.
Cost: `drive` and `drive.readonly` are restricted scopes, so the consent
screen stays Internal on Workspace, or External in Testing with weekly
sign-in. An External app lists the scopes on its consent screen, and an
Internal one does not.

### D-57 Read-only mode registers only the reads and `download_file`

`GDRIVE_READ_ONLY=true` registers only the tools that read, and
`download_file`, and `login` asks for `drive.readonly`. A write is not
registered, and the service refuses one too, so a tool registered by
mistake still writes nothing.

`download_file` writes a local file, so its annotations do not say
read-only (D-45). It is kept in read-only mode because it changes
nothing in Drive, and getting a file out is part of reading one. The
mode is for a person who wants the model to look and not touch, and the
scope makes that hold at Google's end as well as here.

A refusal in read-only mode names what would be needed. `read_file` on a
PDF names `download_file` alone, since neither copying tool is there.

Rejected: leaving `download_file` out of read-only mode because its
annotations are not read-only.
Cost: `download_file` writes a local file in read-only mode, under
`GDRIVE_LOCAL_DIR`.

### D-58 Labels and Drive Activity are opt-in: each setting registers its tools and asks for its scope

Labels and Drive Activity are off unless the deployer turns them on.
`GDRIVE_LABELS=true` registers `list_labels` and `manage_labels` and
adds the label scopes at login. `GDRIVE_ACTIVITY=true` registers
`list_activity` and adds `drive.activity.readonly`. Each needs its own
API enabled in the Cloud project and a scope login would otherwise not
ask for.

The values on a file are Drive's: `files.listLabels` and
`files.modifyLabels` take the ordinary `drive` scope
([files.listLabels](https://developers.google.com/workspace/drive/api/reference/rest/v3/files/listLabels)).
The definitions are the separate Drive Labels API. One setting governs
both tools, because applying a label needs a label id and a field id,
which exist only in the definitions. Where the definitions are out of
reach, applying and removing still work, and only `set_field` is
refused, since the setter depends on the field's type.

The Activity API identifies an actor by a People API resource name and
nothing else
([activity.query](https://developers.google.com/workspace/drive/activity/v2/reference/rest/v2/activity/query)).
So `list_activity` says "you" or "somebody else", and a line says why.

Rejected: a label setting for the values alone, which would leave
`manage_labels` with arguments nobody can find out.
Cost: each needs its API enabled in the Cloud project, and the refusal
names both steps. Activity names only "you" or "somebody else".

### D-59 Sign-in is Google's desktop loopback flow on 127.0.0.1, a random port and PKCE

`google-drive-mcp login` runs Google's desktop flow: it opens the
consent page and listens on `127.0.0.1` at a random port, with PKCE and
a state check
([native apps](https://developers.google.com/identity/protocols/oauth2/native-app)).
It takes a Desktop app client's JSON and refuses a Web application
client's.

The loopback redirect keeps the code on the person's machine, and PKCE
binds it to this run, so a code seen elsewhere cannot be exchanged. A
Web application client needs a redirect URI on a host, which is the
hosted shape D-01 rejects. The exchange runs on a client with a deadline
(D-53), and a login that waits too long gives up.

`TestLoginRejectsAStateMismatch` holds the state check, and
`TestParseClientSecretRejectsWebClient` the client kind.

Rejected: the out-of-band flow, which Google blocks; a Web application
client.
Cost: a login over SSH needs the loopback port forwarded to the machine
with the browser.

### D-60 The refresh token lives in the keyring, or an owner-only file with a warning when the keyring fails, and the environment overrides both

The refresh token lives in the OS keyring: Secret Service, Keychain or
Credential Manager. When the keyring fails, it goes to an owner-only
file under the profile's config directory, and each read of that file
while the keyring is failing logs a warning on stderr. `GDRIVE_REFRESH_TOKEN` overrides both, for CI
and automation. `logout` revokes the grant and deletes the token.

The keyring is where the operating system keeps a secret from other
users and from a backup that copies files. A machine without a working
keyring still has to sign in, so the file is the fallback, at `0600` and
warned about. The warning appears only when the keyring is broken, so it
is not noise. The order is the one `gh` uses.

The client secret's file is the person's own and is never logged. Only
the refresh token is stored; access tokens live in memory.

Rejected: refusing to start when the keyring is broken, which leaves a
headless machine with no way in.
Cost: `logout` cannot revoke or remove a token supplied through the
environment, and the person removes it from the client's configuration.

### D-61 Profiles keep separate secrets, tokens and accounts, and a login rewrites what it records

`GDRIVE_PROFILE` names a profile, and each profile keeps its own client
file, refresh token and account under `GDRIVE_CONFIG_DIR`. A profile's
name follows `config.ProfileRule`. `login` rewrites everything the
profile records: the client file's path, the account, where the token
went and the granted scopes.

One person often has a work account and a personal one, and each needs
its own token and its own client. Keeping them apart means a call cannot
act as the wrong account because a file was shared between them.

A re-authentication that kept the old client or account would leave a
profile pointing at a client that is gone, or reporting an account it no
longer signs in as. So the record is replaced whole, written at `0600`,
rather than patched. `TestProfilesAreSeparate` and
`TestSaveReplacesTheFileWhole` hold both halves.

Rejected: keeping a stale client or account after re-authentication.
Cost: each profile signs in on its own, and a client configuration names
the profile it runs.

### D-62 The server starts and serves without credentials, answering `[auth]` per call

The server starts and serves without credentials. It warms the token off
the startup path, and on failure logs it and keeps serving. Every tool
then answers `[auth]` naming `login`.

A server that exits at startup shows the person "failed to connect" and
tells the model nothing, so neither learns that the fix is one command.
Serving lets a tool call carry the reason to where it is read. It also
keeps `--dump-schemas`, the smoke gate and the schema diff working on a
machine that has never signed in.

The smoke gate initializes the binary with no credentials, lists the
tools and the resource templates, calls `get_file`, and expects an
`[auth]` tool error.

Rejected: exiting at startup when there is no refresh token.
Cost: a missing token shows on the first call rather than at start, and
`doctor` is the check to run after setup.

### D-63 Configuration is `GDRIVE_*` with a bound flag each, read through one lookup and validated once at start

Every setting is a `GDRIVE_*` variable with a flag bound to the same
name. A flag overrides the environment, and the environment overrides
the default. Every reader goes through `config.Lookup`, which reads a
value of spaces alone as unset. The configuration is validated once at
start, and every invalid value is reported together.

Claude Code, Claude Desktop and Cursor pass a server only `command`,
`args` and `env`, so the environment is how a person configures it in
each of them. `config.Vars` is the one registry of every variable the
server reads. The staleness gate holds
[configuration.md](configuration.md) to it by whole word, and the bundle
gate derives what the bundle asks for from it (D-85).
`TestEveryVariableTheCodeNamesIsInEnvVars` finds every `GDRIVE_` name
the code spells and holds it to the registry, so a variable read outside
it fails.

Rejected: a list of variables typed by hand.
Cost: `GDRIVE_CONFIG_DIR` and `GDRIVE_REFRESH_TOKEN` are read before the
flags are parsed, so they have no flag.

### D-64 The released tool surface is a contract

The released tool surface is a contract: the tool names, their input and
output fields and types, and the resource templates.
`testdata/schema-baseline.json` holds the newest release's surface,
recorded in its release commit by `make schema-baseline`.
`make schema-diff` compares the build with it at any depth, one way: an
input may take more than it did and an output return less.

It fails on a removed or renamed tool or resource, a field removed at
any depth, an output that may be missing or null, an input that stops
taking a value it listed, and an input that becomes required. With
nothing under `[Unreleased]`, it fails on any difference at all. On a
pull request, `scripts/gates schema-ack` fails unless a commit footer
names each item of the surface that changed, an annotation, a title, a
mark or the instructions included.

A model, a saved prompt or a script builds against the surface. A field
dropped from an output breaks it without an error anywhere. A break
passes only when the CHANGELOG declares it under `**Breaking:**`, naming
the tool, which makes it a release break.

Rejected: diffing top-level inputs against the last tag, which lets a
dropped output field through and gives a deliberate break no way past;
diffing at full depth against a build of the newest release HEAD
reaches. The second costs one more worktree build per run and needs tags
in every checkout, for a surface the release commit already records and
`make schema-diff` holds to the CHANGELOG.
Cost: an open issue: a `**Breaking:**` line names a tool, so one break
acknowledged for a tool lets any other break of that tool through (#88).

### D-65 Schemas are flat: no nested objects and no arrays of objects

An input schema is flat: no nested objects and no arrays of objects. A
field is a scalar, a nullable scalar, a map of scalars, or an array of
scalars. `TestSchemasAreFlat` holds every tool's input to it.

A model fills in a flat schema reliably, and a nested one invites a
structure the model has to build and the client has to show.

Two needs set where the line falls. `update_file` has to tell "leave
this alone" from "set it to false", which a nullable boolean says.
Drive's custom properties are a map. Both are still one level deep. An
approval's reviewers are a list of addresses, a list of scalars.

Where a tool needs a list of things with several fields each, it takes
one thing per call instead (D-49).

Rejected: every argument a scalar, which cannot say "leave this alone"
or carry Drive's custom properties.
Cost: an open issue: shared phrases in the schema descriptions are
written out per tool, since a Go struct tag cannot be composed from a
constant, and composing them after registration would remove the copies
(#98).

### D-66 A source change carries a CHANGELOG entry, and a release commit's sit under its untagged version

A change to what users get carries a CHANGELOG entry under
`[Unreleased]`. That is any file but a test in a package the binary
imports, as `go list -deps` reports them, a file such a package embeds,
the bundle's manifest or its launcher, or `go.mod` outside its `require`
and `tool` lines. An entry is a bullet
at the margin outside fenced code. On a release commit the notes move
under the release's version heading, which counts while that version has
no tag.

The staleness gate reads the commits from the newest release HEAD
reaches, by semver and a prerelease included, to HEAD, as CI sees them.
It fails a change whose `[Unreleased]` section is empty and whose newest
version is tagged or empty. With no release tag, or a git or go call that cannot
answer, the rule gives no verdict rather than read it as a change. The
gate still runs its other checks, and its message says the rule could
not run and why, such as a shallow clone. `go run` exits 1 whatever the
gate's own status, so the message is what tells it from a finding.

The release workflow lifts the version's section verbatim into the
release notes, through `scripts/gates release-notes`, so the CHANGELOG
is what a person reads about a release. A change that lands with no
entry is a change no reader of the release hears about.

The release commit sits on a branch and passes CI before the merge, and
so before the tag exists. A gate that failed between the release commit
and the tag would fail the release pull request itself. Reading notes
under an untagged heading as the change's entry lets it pass, and still
refuses an undocumented change.

Rejected: failing between the release commit and the tag; counting
every Go file, which asks for an entry for a test or a gate no user sees;
measuring from the newest tag of every branch, which a release on
another line can be.
Cost: until the tag is pushed, a later source change passes on the
release's own notes, so the tag follows the merge at once. A dependency
bump passes the gate, so its entry is the author's to write.

### D-67 Tests run against an in-memory Drive that refuses what Drive refuses and matches no more than Drive does

The unit suite runs against `internal/gapi/drivetest`, an in-memory
Drive behind `httptest`. It refuses what Drive refuses, with Drive's
status and reason: a comment call without `fields`, an approvals call
without them, `includeLabels=*`, a key-only property search, a generated
id on a Google document. It matches no more than Drive does:
`name contains` matches word prefixes in any order, and
`fullText contains` matches whole tokens, stricter than Drive.

A fake that accepts what Drive refuses lets a defect ship: every comment
call would fail against Drive while every test passed. A fake that
matches more than Drive hides a query that finds nothing. One that
matches less fails a test production would pass, which gets noticed.

Where Drive's answer has a shape the fake must copy, the fixture is the
JSON a decoder reads rather than a struct literal, since a literal
cannot say "an empty object" apart from "a member missing". The fake
lags where Drive is known to lag, as `drives.list` does after a create,
so the code that copes is tested.

Rejected: a fake that matches more than Drive, which hides a query that
finds nothing.
Cost: every fake rule a live run has not confirmed is the tests agreeing
with themselves, and only `make live` checks it (D-68).

### D-68 Behavior the reference does not pin down is checked against the discovery document and probed live before code depends on it

What the reference does not pin down is checked before code depends on
it. The discovery document is read before a client method is written,
for its parameters and its response type. A behavior the reference does
not state is probed against an account, and the probe becomes a live
step. The comment at the line that depends on it records what was found,
with the source and the step.

Two kinds of error make this the rule. Memory of an API is wrong often
enough to matter: a parameter offered on the wrong call, a field Drive
refuses to select, a method with no response. And a record of a probe
can be wrong while the probe was right: a check that asks whether a
member is falsy cannot tell an empty object from a missing one. So a
record keeps the response, or what a decoder made of it, rather than a
sentence about it. A parameter list read once is a fact as of the day it
was read, which is why `make api-diff` is part of every release.

Rejected: writing a client from memory; trusting a parameter list read
once; recording a sentence about a response instead of the response.
Cost: `make api-diff` reaches the network, so it runs at release rather
than in `make check`.

### D-69 Every method of the three APIs and every published field is used or left out on purpose

Every method of the three APIs this server calls, Drive v3, Drive Labels
and Drive Activity, is either used or left out with a written reason.
`testdata/api-methods.json` is a snapshot of the methods the discovery
documents publish, and `testdata/api-coverage.tsv` gives each its
verdict. `make api-coverage` fails on a `used` entry that names no
client method, an `out` entry with no reason, a client method that no
entry claims, and a snapshot method the record does not cover.

Fields get the same treatment. `testdata/api-fields.json` is the
published fields, and `testdata/api-fields.tsv` records a verdict for
each one the wire types leave out. `make api-fields` fails on a field
with neither a wire type nor a verdict, so a field Google adds is a
failure until somebody decides about it.

The reason is that what this server leaves out should be a decision, not
an accident. A method nobody looked at is a capability nobody knows is
missing, and a field nobody decided about is one a later reader adds
without knowing why it was absent. Both gates run offline on every
commit against the committed snapshot.

Rejected: fetching the discovery documents inside the gate, which would
fail whenever Google's servers are slow.
Cost: the snapshot is refreshed by hand with `make api-diff`, which
reaches the network, so it runs at release and not on every commit.

### D-70 Green gates are not done: the live driver drives every tool against the account, and a person reads the transcript

Green gates are not done. `make live` builds the binary and drives it
over stdio against the signed-in account: every tool and every action,
in scratch space the run makes (D-71). Each call is a named step, and a
comment that depends on a behavior of Google's names the step that
checks it. Every refusal must be an expected one, and the client
declares elicitation and counts any question as a failure (D-44).

The driver's verdict is not the thing to read. A run can report every
call as expected while a result says the wrong thing: a card calling a
file private beside a line saying it was just made public. The driver
checks whether a call succeeded, not whether it told the truth, so a
person reads the transcript.

`make live-cover` holds every published tool option to a decision in
`testdata/live-cover.tsv`. An option the driver does not send is
undrivable, with what blocks it, or undriven, with what closing it would
take. A static read of the driver's source cannot tell a step that runs
from one in a branch that never does. So the driver also records what it
sent, and the end of a run names every option the source sends and the
run did not.

Rejected: trusting the driver's verdict; a static gate over the driver's
source alone.
Cost: an option that needs a second person or an administrator stays
undrivable. An open issue lists the live checks the driver does not
reach: a real ownership transfer, a policy-blocked share, a published
label, a resource key from another account, several modes and the OCR
limits (#92).

### D-71 The live driver works only in what it made: a scratch folder, and a scratch shared drive for the destructive run

The live driver works only in what it made. A `-write` run makes one
scratch folder in My Drive, does everything inside it, and trashes it at
the end. A `-destructive` run makes a scratch shared drive, runs the
permanent deletes there, and deletes the drive at the end. Cleanup
deletes every id the run made rather than trusting each section to tidy
up after itself, and a stop signal still runs it.

`files.emptyTrash` cannot be scoped to a folder, so reaching it outside
a drive of its own means emptying the signed-in account's trash. A
scratch shared drive is a container the run can delete whole, which is
also what an approved, locked file needs. So `empty_trash` is named in
exactly one method, which cannot run before the drive exists, and
`TestEmptyTrashIsNamedInOnePlace` walks the driver's syntax tree and
fails on a second mention. An ownership transfer goes only to the
address passed with `-transfer`, since it cannot be undone and Google
mails the address.

Rejected: emptying the signed-in account's trash; trusting each section
to tidy up after itself.
Cost: the destructive run needs an account that may create shared
drives, and each run leaves its scratch folder in the trash, which is
emptied by hand. An open issue: the driver writes its skip banner and
its call, look and retry shape out by hand in many places (#96).

### D-72 The live driver and the evals print only through one redacting transcript

The live driver and the evals print only through
`scripts/internal/transcript`, which passes each whole line through the
redactor before it reaches a terminal. The redactor replaces ids, links,
addresses, people and the names of what is not the run's own with
placeholders. `make transcript` refuses every other route to a terminal
in those programs: `fmt.Print*`, any mention of `os.Stdout` or
`os.Stderr`, and the builtins `print` and `println`.

A transcript is meant to be pasted into an issue, and it is printed from
a real account. The gate forbids a destination rather than a function,
because `fmt.Fprintf` into a buffer reaches nobody, and a ban on the
function would need an exception on its first day. It refuses a hollow
exemption: the one package allowed to write must pass what it writes
through the redactor, and a test asserts what comes out. It redacts the
whole line rather than the arguments somebody wrapped, so an address
arriving through a format string nobody thought about is caught. The
run's own failure line goes through it too.

Rejected: forbidding a function rather than a destination; redacting
only the arguments somebody remembered to wrap.
Cost: an open issue: the redactor leaves the organization's domain, and
once left a comment reply id (#91). So a transcript is read before it is
shared, as the run's last line asks.

### D-73 Evals score the end state and the trace, through this server's tools alone

`make evals` gives a model tasks through `claude -p` with only this
server's tools, and scores each on two things. The end state is read
back through the server. The trace shows no invented ids, ambiguity
handled by asking or listing, and no tool that shares past the
organization called unasked. Every task runs inside one scratch folder,
trashed at the end, and the output goes through the transcript's
redactor (D-72).

The trace half is the point. A task can be answered correctly by a model
that guessed an id and was lucky, and the end state cannot tell. A task
can also fail for the right reason, and only the trace shows it. An eval
is the only check here that tests a tool's description rather than a
code path: whether a model reading it picks the right tool.

`TestAnInventedIdIsCaught` and `TestWideningToolsAreAlwaysAFailure` hold
the scoring, and `TestEveryTasksPlaceholdersCanBeFilled` holds that
every task's placeholders are filled before a prompt is sent.

Rejected: scoring the end state alone.
Cost: the evals need the `claude` command, a signed-in account and real
tokens, so they are not in `make check`.

### D-74 Coverage is floored per package over a list derived from the tree

Each package under `internal/` clears a statement-coverage floor on its
own, and `make cover` runs it on Linux, macOS and Windows. The list of
packages is derived from `go list` over `internal/`, so a package is
under the floor from the day it exists. A package is exempt only by name
with its reason: the wire types, the version string and the test fake.

An average would let a well-tested renderer hide an untested policy, so
each package is held on its own. A hand-written list omits a package
silently, and the omission looks exactly like a package that does not
exist. A floor measured on one platform cannot see a test skipped on
another, so it runs on all three.
`TestEveryPackageUnderInternalIsMeasuredOrExemptOnPurpose` holds the
derivation, and `TestCoverageFailsBelowTheFloor` the floor.

Rejected: a hand-written package list; a floor measured on Linux only.
Cost: `internal/gdrive`, `internal/version` and
`internal/gapi/drivetest` are exempt by name, and `cmd/` is outside the
floor, since the test run measures `internal/` only.

### D-75 Call counts, memory and linearity are tests; milliseconds stay in the benchmarks

What a test can assert about cost, it asserts in `make check`. That is
how many calls a card or a search page makes, that a download holds
bounded memory, and that a large tree renders in linear time.
Milliseconds stay in `make bench`, which is not in `make check`.

`make check` runs under the race detector with coverage counters. A
large tree renders in a few milliseconds uninstrumented and an order of
magnitude slower there, so a wall-clock target either fails for the
wrong reason or is set so loose it asserts nothing.
`TestATenThousandItemTreeStaysLinear` asserts that ten times the items
cost within twenty times the work, plus a backstop far above anything
instrumentation explains. A call count does not depend on the machine,
so `TestASearchPageCostsOneListAndBoundedParentLookups` holds the count
exactly. A benchmark's fixture has the shape Drive would return, since a
tree a thousand levels deep measures the indent string.

Rejected: a wall-clock target in `make check`.
Cost: the benchmarks run against a fake with no latency, so they measure
this server's own work and cannot show a saving or a regression in round
trips.

### D-76 A gate is watched failing, and a gate that reports a number has a floor or a record

A gate is watched failing before it is believed: the defect it exists
for is planted, and the gate has to report it. A gate that reports a
number has either a floor it refuses to report below or a record that
fails when the number moves.

A gate that has never failed proves nothing, and one whose own test
enumerates the same shapes as the code it checks agrees with the bug it
should catch. A measurement can also fail by returning a number: a
reader that silently reads a fraction of what it should still prints a
figure. So `make classes` refuses to pass with nothing to read,
`make parity` refuses to compare nothing, and the leak scan says how
much it read and refuses an empty read. The live-coverage reader keeps a
record that fails when its count moves.

`TestTheClassGateRefusesToPassWithNothingToRead`,
`TestParityRefusesToCompareNothing` and
`TestTheLeakScanSaysHowMuchItReadAndRefusesNothing` hold the floors.

Rejected: a test that enumerates the same shapes as the code it checks.
Cost: every gate carries a test that plants its defect, and a count a
gate reads carries a floor kept by hand, well under what the tree holds.

### D-77 `make check` and CI run the same gates

`make check` and the CI workflow run the same checks, and `make parity`
compares them by what they run. Every command a target in `make check`
runs, its Makefile variables expanded, is one a `ci.yml` job a push to
`main` runs, with the same words, as the shell splits them, or one an
action stands in for. A `ci.yml` line that runs `make` with targets runs
their recipes and those of what they depend on. A command beside a gate
call on its line, or in a pipeline with one, is compared on its own.
Every go command `ci.yml` runs is one `make check` runs, and a
subcommand excused from that is one `ci.yml` runs. A command in `make
check` behind a `-` prefix or named by `.IGNORE` is refused, since make
carries on past its failure.

`scripts/gates` has one list of commands, and each says where it runs:
in both `make check` and CI, in CI on a pull request only, at release
time in `release.yml` and the workflows it calls or in
`.goreleaser.yaml`, or by hand from a Makefile target or the pre-commit
hook. A gate both run takes the same arguments in both, and an omitted
binary is the one the gate defaults to; a bare name is not the same
binary as `./` before it. Every file behind a build tag, read from the
`//go:build` lines of the files git lists, is vetted on both sides by a
pass that compiles it.
The reader does not read `if:` on a step or a job, so a command under a
condition counts as one CI runs.

A contributor who runs `make check` and sees green should be able to
trust that CI will too, and a gate that runs only in CI is one nobody
runs before pushing. `schema-ack` measures from a pull request's
merge-base, which only a pull request has, so it runs there alone.

`TestParityAcceptsThisRepository` and
`TestParityCatchesDriftInEitherDirection` hold it, the second by planting
each drift. `TestParityHoldsMakeCheckToWhatAPushRuns` holds the push
jobs.

Rejected: a list of gates kept separately in the Makefile, the workflow
and the usage text.
Cost: a command goes into both in the same words, or is excused in
`scripts/gates/parity.go` with the reason.

### D-78 CI runs the tests and the coverage floor on Linux, macOS and Windows

CI runs the unit tests and the coverage floor on Linux, macOS and
Windows, from the first commit. Path handling, permission bits and line
endings differ between them, and the server runs on all three.
`.gitattributes` pins LF, so the goldens compare byte for byte on
Windows too.

The test that matters most on each platform is the one that touches the
file system: the local directory's confinement with `..` and symlinks,
the owner-only token file, and where `os.UserConfigDir` puts a profile.
A test skipped on one platform costs coverage nobody measured, so the
floor runs on all three (D-74), and a skip states its reason.

The release builds for the same three operating systems on amd64 and
arm64 (D-82).

Rejected: running CI on Linux alone.
Cost: every change waits on three runners. `make parity` compares the
gate lists and not the operating-system matrix, so review holds the
matrix.

### D-79 Every action is pinned to a commit SHA, and every tool it installs to one exact version

Every action a workflow uses is pinned to a full commit SHA, with its
version in a trailing comment. Every tool a step installs is pinned to
one exact version, beside the action that installs it, and a container
image by its digest. setup-go reads `go.mod` alone. A tool named on any
line of a workflow, the Makefile, a command the goreleaser config runs
or a string in the Go under `scripts/` is named at one version
everywhere. The
Makefile names a tool as one `module@version` variable, and one no
recipe runs with the go command pins nothing. A recipe or the pre-commit
hook that names a known tool runs it from the PATH, and is refused.
`make pins` fails on a tag, a branch, a range of any form, an expression,
or a tool fetched at `latest`, a recipe's included once its variables
are expanded.

GitHub's hardening guide says pinning to a full-length commit SHA "is
currently the only way to use an action as an immutable release"
([secure use](https://docs.github.com/en/actions/reference/security/secure-use#using-third-party-actions)).
A step that installs a tool has two versions, and pinning only the
action leaves what it does free to move. An action pinned by SHA still
installs whatever version of its tool it is handed, and a signing tool's
next major version can change its flags under a release. A version range
with `~>` floats across every patch whatever follows it, and narrowing a
range is not pinning it.

The goreleaser config is read by key path, as the goreleaser version
`release.yml` runs reads it, and `make pins` fails when the release runs
another version than the one the reader is checked against. The readers
refuse by name a line or a shape they do not read, such as a line break
YAML reads and they do not, rather than read less.

Dependabot updates the action SHAs and keeps the comment in step.
`TestActionPins` holds the action half, and `TestPinsNamesEachProblem`,
`TestPinsRejectsEveryRangeForm` and
`TestAToolDownloadedAtLatestIsNotPinned` the tool half.

Rejected: a major tag such as `@v7`; a `~>` range for a tool version.
Cost: a tool's version is bumped by hand, since Dependabot moves the
action SHAs and not the versions passed to them.

### D-80 A workflow starts read-only and raises a permission only in the job that needs it

Every workflow declares `permissions: contents: read` at the top, and a
job that needs more raises it for itself. The release job raises
`contents`, `id-token` and `attestations`, and the CodeQL job raises
`security-events`. Every checkout sets `persist-credentials: false`.

A token a step does not need is a token a compromised step can use.
OpenSSF Scorecard's Token-Permissions check wants the top level
read-only and write raised per job
([Scorecard](https://github.com/ossf/scorecard/blob/main/docs/checks.md#token-permissions)).
Declaring permissions once at the top gives every job the widest set any
job needs. Nothing in these workflows pushes with the checkout's token,
so leaving it on disk for later steps is exposure with no use. A CodeQL
workflow runs on pushes, pull requests and weekly, so a rule added after
a merge still reaches old code
([SAST](https://github.com/ossf/scorecard/blob/main/docs/checks.md#sast)).

Rejected: permissions declared once at the top of a workflow; a checkout
that leaves its token on disk.
Cost: each job that writes names its permissions, and a step that pushes
would need its own credentials.

### D-81 `main` is released code, changed only through a pull request, and tags are pushed one at a time

`main` is released code. Every change reaches it through a pull request,
the maintainer's own and release commits included, and merges once CI is
green on all three platforms. A release tag is pushed on its own, one at
a time, and the release workflow takes it from there. GitHub drops tag
events past the third in one push, so tags pushed together can leave a
release unbuilt.

A rule with an exception for the person who releases is not a rule.
OpenSSF Scorecard's Branch-Protection check asks for pull requests gated
by a passing check. Its two-reviewer tier cannot apply to a project with
one maintainer, so the gate is CI rather than a review count. The
release steps are in [release.md](release.md).

Rejected: direct pushes to `main` by the maintainer, release commits
included.
Cost: zero approvals are required, so CI is the only gate on a merge,
and a release takes a pull request and a separate tag push.

### D-82 A release is built from a tag, reproducible, signed, attested and verified from outside

A release is built from a `v*` tag by GoReleaser, for Linux, macOS and
Windows on amd64 and arm64. Builds stamp the commit's time, so a rebuilt
tag is byte-identical. `checksums.txt` is signed with a keyless Sigstore
certificate, and every archive gets an SBOM. The build gets a provenance
attestation in the job that runs goreleaser, whose token writes it, and
whose subject-path names the archives in each format the config builds,
the bundle and the checksum file, each by an entry of its own.

The release is never a draft, and its notes are the version's CHANGELOG
section (D-66). `go install` is the second path, with the version read
from build info.

A person installing the binary needs to know it came from this
repository's workflow at this tag. Signed checksums and an attestation
let them check that without trusting the download page. A release
workflow can be green and still have published something wrong, and
signing runs only in CI with an OIDC token, so a local rehearsal cannot
reach it. So each release is verified from outside after it is
published: the checksums, the signature against the exact tag identity,
the attestation, the version in the binary and the documented install.
The steps are in [release.md](release.md).

Rejected: trusting the release workflow's own green tick.
Cost: a release is not done until somebody downloads it and checks it,
and the first tag of a change to the release path is a test of that
path.

### D-83 The MCP registry entry is held to the registry's own validator and published last

`packaging/registry/server.json` is the MCP registry entry, and
`make registry` holds it to the rules the registry enforces. The release
workflow publishes it last, after the bundle is attached, authenticating
with GitHub OIDC. `scripts/gates registry-publish` prints the entry to
publish, with the bundle's hash from the signed checksums.

The registry's bundle rules are in its validator's code and not in its
published schema. The identifier must be an HTTPS release asset on
`github.com` or `gitlab.com`, must name "mcp", and must answer a HEAD
with 200 or a redirect carrying a `Location`. GitHub answers a
release-asset HEAD with 302, so a check written to expect 200 would
refuse every GitHub release. The schema carries the length limits and
patterns, and a refusal on those arrives after the login succeeds, at
the end of a release. So the gate holds both sets, each watched failing.

Rejected: a check expecting 200 from the HEAD; fetching the schema's
limits in the gate, which would fail whenever that host is slow.
Cost: the limits are transcribed into the gate with their source, and
`make schema-refetch` is what shows they moved.

### D-84 The bundle manifest is held to the files the packer stages, the platforms goreleaser builds and a pinned schema

`make mcpb` holds the Claude Desktop bundle's manifest to what is real.
It checks the files the packer stages, the platforms `.goreleaser.yaml`
builds, and the schema the manifest declares. Every command starts with
`${__dirname}/` and names a program the packer stages. `entry_point` is
the file the default command runs, and the platforms the manifest claims
are exactly those an entry point is staged for. `$schema` names a full
release tag or a commit SHA, and the packer validates the stamped
manifest it ships against the vendored schema.

The host resolves no path in a command and passes it to the spawn as
written, and `${__dirname}` is the one form whose meaning the
specification fixes
([MANIFEST.md](https://github.com/anthropics/mcpb/blob/v2.1.2/MANIFEST.md)).
A platform override's `args` or `env` replaces the base's rather than
adding to it, so the gate refuses either on an override and holds the
base `args` to `[]`. A branch in `$schema` can change under a manifest
that claims to conform to it. A platform goreleaser adds would otherwise
be missing from the bundle in silence. So each goos-goarch pair the
builds build needs a staged binary that runs on it, and Windows arm64
runs the amd64 one. The pairs are read through the one
`.goreleaser.yaml` reader, and a build the reader cannot hold to its
pairs is refused.

Rejected: `$schema` at a branch; `args` or `env` on a platform override.
Cost: a goos or goarch added to `.goreleaser.yaml` fails the gate until
the bundle stages a binary for it, and `make schema-refetch` checks the
vendored schema against its source by hand.

### D-85 The bundle asks for what a person sets in another client's configuration, every switch off by default

The Claude Desktop bundle asks for what a person sets in another
client's configuration: the OAuth client file, the profile, read-only,
the destructive switch, the sharing tools, the local directory, the
download cap, labels and activity. Each key is its variable's name
without `GDRIVE_`, lowercased. The bundle gate derives the list from
`config.Vars`, so a variable the server adds fails it until the bundle
asks for it or the gate records why not.

Claude Desktop approves each tool, and a person can set a tool to always
allow, so a switch left off is the one guard a bundle install keeps.
Every switch defaults to off, and every other optional key to `""`,
which the server reads as unset. A blank optional key with no default
would reach the server as its literal `${…}` reference. The gate starts
the server from the environment the defaults give it and from none,
names any field that differs, and starts every set of the switches.

Rejected: a `tools` list in the manifest, which is display only and
wrong for most installs, since several settings change what registers.
Cost: an open issue: the fields for the destructive switch, sharing,
labels and activity name the tools their setting registers, and nothing
holds those names to registration (#90).

### D-86 The server confirms a write by asking the person through elicitation

The server could confirm a write itself. Before each delete, each wide
share and each loosening of a shared drive, it would put a question to
the person through MCP form elicitation, and write only on an accept. It
is declined (D-44).

The specification puts consent to each tool call on the host, and lets a
server ask through elicitation without making the answer consent: a
client gathers it "from the user or other sources"
([elicitation](https://modelcontextprotocol.io/specification/2025-11-25/client/elicitation)).
Clients answer such a question with nobody there: a Claude Code hook
accepts it with no dialog, and Codex accepts a form with no fields by
itself under full access. Others declare it and never draw it, and no
client's allow and ask rules match it. A question on every call is
answered without reading. A control that holds on some clients and not
others is not a control.

Rejected: confirming writes through elicitation; a setting that refuses
the asking writes on a client that cannot ask.
Cost: a client running with nobody watching makes every registered
write, so `GDRIVE_ENABLE_DESTRUCTIVE` stays off for one, and a
configuration that still sets `GDRIVE_REQUIRE_PROMPT` is refused at
start.

### D-87 Changes arrive by a watch or push channel

The server could tell the model when a file changes, through a Drive
watch channel or a subscription to a resource.

It is declined. A watch channel delivers its notifications to a public
HTTPS endpoint
([push notifications](https://developers.google.com/workspace/drive/api/guides/push)),
and a stdio server on a person's machine has none. Drive has a changes
feed that a caller polls instead
([changes](https://developers.google.com/workspace/drive/api/guides/manage-changes)).
`list_changes` without a token hands out a start token, and with one it
lists the changes since and returns the next token. The resources offer
no subscriptions for the same reason (D-26).

Rejected: a watch or push channel, which needs a public HTTPS endpoint.
Cost: a change somebody else makes is seen only when the model asks. The
feed lags a write (D-30), and a token's lifetime is undocumented, so
`search_files` with `modified_after` is the stateless alternative.

### D-88 A revision is published to the web

`revisions.update` can publish a revision of a Google document to the
web
([revisions](https://developers.google.com/workspace/drive/api/reference/rest/v3/revisions)).
A tool could set it. None does.

It is declined. A published revision is readable by anyone who finds it,
with no audience to choose and no grant to list. `list_permissions`
would not show it, and no result could report who can see it, which is
what every sharing result here does (D-36). A person who wants a
document public uses a link for anyone, which `share_by_link` grants
behind the destructive flag and `list_permissions` shows.

Rejected: publishing a revision to the web, an exposure with no audience
control.
Cost: a document is published to the web from the Docs editor, and
`testdata/api-fields.tsv` records `publishAuto` as left out because no
tool publishes.

### D-89 The server acts with domain administrator access

Drive lets an administrator act on a domain's files and shared drives
with `useDomainAdminAccess`, and delete a shared drive with its items
through `allowItemDeletion`
([drives.delete](https://developers.google.com/workspace/drive/api/reference/rest/v3/drives/delete)).
The server could offer that.

It is declined. The server acts as the signed-in person and sees what
they see (D-01). Administrator access reaches every file in a domain,
far past what any task a person gives a model needs. A model persuaded
by a document could then reach files the person never could. Every guard
here reasons about what this account may do, and administrator access
would make each of those answers wrong.

Rejected: acting with domain administrator access.
Cost: a shared drive must be emptied before `delete_drive` can delete
it, since `allowItemDeletion` needs administrator access, and a domain's
files are administered in the Admin console.

Next ID: D-90
