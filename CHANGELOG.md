# Changelog

All notable changes to this project are documented here. The format
follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and
this project uses [semantic versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed

- **`copy_file` claimed the comments came across, and nothing could know
  that.** With `copy_comments: true` the result said "The comment threads
  were copied with it, so everybody who can see the copy can read what
  was said on the original" — asserted from the argument. `files.copy`
  answers with a File and mentions comments nowhere, so the claim had no
  source. It is the `lock_file` defect exactly, with somebody else's
  words in place of a restriction, and the class of claim this server is
  most careful about everywhere else.

  Three things kept it alive. The live driver has copied with that
  parameter since phase 4 and never looked at the copy. The test asserted
  the sentence, so a fixture written from the belief kept the belief. And
  §18 recorded the overpromise as though it were a feature: "the result
  says out loud when a copy carried somebody else's words somewhere new."

  It now says what Drive was ASKED to do, says outright that Drive does
  not report whether it did, and names `list_comments` on the copy as the
  call that settles it. Reading it back was the other option and was
  rejected for a reason phase 5 paid for: `comments.list` lags a copy, so
  an empty answer would report threads as dropped when they were merely
  late — the `empty_trash` mistake in the opposite direction. The live
  driver now lists the copy's comments, so the next run answers a
  question nobody has asked.

- **The parity gate could be defeated by typing one `#`.** It read the
  whole workflow file for `go run ./scripts/gates NAME`, so a step
  commented out to unblock a red build still counted as running — which
  is exactly the divergence between `make check` and CI that this gate
  exists to catch, reached by the commonest way of causing it. The
  Makefile side had the same hole: an indented `#` is a line Make hands
  to the shell, which does nothing with it.

  Comment lines are dropped from both files now, and on the CI side a
  gate counts only where a `run:` step names it — on the step's own line
  or inside a `run: |` block, found by indentation rather than by parsing
  YAML. Four ways a gate's name can be in the file without running are
  tested. What is left is a step disabled by an `if:` that is never true,
  which needs a real YAML parser and a dependency to see; it is a smaller
  hole than a `#`, which needs nothing.

  Found by a sibling repository porting these gates and hitting it there.

- **The error-class gate never read the published vocabulary.** It held
  every declared class to being emitted somewhere in the code, and
  `gapi.Classes()` — the list `doctor` prints, and what a model is told
  the vocabulary is — was a third list nothing checked. A class could be
  declared, emitted and missing from it, which is a class nobody can look
  up; or listed twice, which is a list edited without being read.

  Both are refused now, and the duplicate check counts occurrences rather
  than comparing lengths after a compaction: the sibling that reported
  this had used `slices.Compact`, which removes only ADJACENT equals, so
  a class written twice anywhere but beside itself passed. The fixtures
  here put the repeat at the far end.

- **The stdio smoke test threw away the reason it failed.** When the
  write of the request frames fails, it fails because the server has
  already gone — so the error in hand is a broken pipe and the
  explanation is in the server's stderr, which this discarded. A server
  that panicked during initialisation reported `write frames: broken
  pipe` and nothing else. It reports the stderr with it now.

- **The README's status line was two releases stale.** It said "Status:
  v0.3.0, phase 3" through v0.4.0 and v1.0.0, and promised Workspace
  labels as something that would "arrive in v0.4.0" after they had
  shipped. The list of unverified paths was out of date too: it named
  two, where there are four, and the five destructive tools it implied
  were unverified have since run against Drive.

  The staleness gate did not catch it, and the reason is worth writing
  down: that gate reads the README's tool TABLE, which was correct the
  whole time, and its architecture check greps for a single phase-0
  placeholder. A document can be wrong about what it *is* while every
  list inside it is right. Both status lines are now held to the newest
  version in this file — only the version, because a number that must
  match another number is the part a gate can hold, and the prose around
  it is a person's job.

### Added

- **The rule against asserting an outcome from the request has a gate,
  and §17a's proposed version of it was wrong.** The entry said: fail if
  the field is read in the same function that builds the outcome note.
  Two CORRECT sites do exactly that — the lock sentence branches on
  `lock_file` and then reads the file back, and the pinning note branches
  on `keep_previous_revision` and then asks Drive to pin and words the
  result from the answer. The function is the wrong unit; the BRANCH is
  the right one, and what makes a branch honest is that it consults
  something before it speaks.

  `gates outcomes` fails a branch that tests a boolean the caller sent
  and then writes prose describing the result without asking Drive
  anything in between. A dry run and a refusal are excluded by the rule
  itself rather than by a list: a dry run makes no call by construction
  and says "would", which is the honest form of exactly this sentence,
  and a refusal says what this server did, which is true whatever Drive
  would have answered.

  The other half of §17a's design question — which fields — is answered
  by deriving rather than choosing. Every boolean field on a service
  input struct is in scope, because a boolean input asks for a state
  where a string carries a value, so a new one is covered on the commit
  that adds it with nothing to remember.

  What the gate cannot do is judge the words: the honest form of the
  shape parses identically to the dishonest one. A branch that is right
  anyway carries a row in `testdata/outcome-claims.tsv` with the reason,
  and a row that stops matching fails, so an excuse cannot outlive the
  code it excused. There are two.

- **The live driver's option coverage is measured, and it reads 122 of
  188.** §17a has wanted this number since phase 5 grew the driver a
  great deal, and the entry said the honest thing about not having it: a
  number nobody has is not evidence of a good one.

  `gates live-cover` holds every option the binary publishes to a
  decision, recorded in `testdata/live-cover.tsv` the way
  `api-coverage.tsv` records API methods. An option the driver does not
  send is `undrivable` from this account, with what blocks it, or
  `undriven`, with what closing it would take. Ten are undrivable, and
  the reasons repeat: a second person, an administrator, or a state
  nothing here may create — a file Google has flagged as malware, a scan
  to run OCR over. The other fifty-six are gaps with a recipe, and most
  are one argument on a call that already happens. They are written down
  rather than closed, because a step added to a live driver is unverified
  until somebody runs it, and adding sixty blind is how a driver starts
  lying.

  **The driver also records what it actually sent**, which is the half
  that catches what a source reader cannot. A step can exist and never
  run — behind a condition that was false, in a list nothing passes on —
  and the words are there either way. A sibling repository proved that by
  deleting one call and watching its own static gate report full
  coverage. So every run now ends with what it sent, what it never
  called, and any option the source claims that the run did not send. The
  first run named five, all behind `-file`; passing `-file` took it to
  three.

- **The live driver's transcript is redacted by construction now, not by
  habit.** §17a has described this since phase 4 and deferred it three
  times, and the reason it kept being deferred is that the version with
  teeth is a rewrite of every print in the program rather than a check
  bolted beside them.

  Every line HAPPENED to go through the redactor before, which is not the
  same as every line HAVING to. Phase 4 found the proof — the driver
  echoed each call's arguments unredacted, which was arguably nobody's
  problem while the only address there was one the operator had typed,
  and became one the moment starting an approval put the signed-in
  account's own address into the arguments of every write run. It was
  fixed line by line. The next print somebody added while debugging would
  have looked exactly like the two beside it that are safe.

  So `scripts/internal/transcript` is now the only way these programs
  write anything, and `gates transcript` refuses every other way out:
  `fmt.Print*`, any mention of `os.Stdout` or `os.Stderr`, and the
  builtins `print` and `println`. The rule is about the DESTINATION
  rather than the function, so `fmt.Fprintf` into a buffer — which is how
  an eval task builds its fixture — is untouched, and the gate needs no
  allowlist. An allowlist is how a gate stops being believed.

  Two programs share it rather than one having it. The eval harness runs
  against the same real account and prints the same kinds of thing, and a
  rule covering one of two identical programs is a rule waiting to be
  drifted around. The gate also refuses a HOLLOW exemption: if nothing in
  the transcript package passed what it writes through the redactor,
  every other check here would be worth nothing, and moving each
  `fmt.Println` into a passthrough helper would have satisfied all of
  them. Six ways to a terminal are watched being refused in tests, and
  what comes out of the transcript is asserted rather than assumed.

  The run's own failure was the one line that never went through the
  redactor at all: it was the last thing printed, after the session, and
  it carries whatever the error carried.

- **The README carries the things a released server's README should**, and
  did not: a **Versioning** section saying what is stable within a major
  version and what counts as breaking, a **Security** section pointing at
  `SECURITY.md`, the actual commands to verify a release rather than a
  sentence saying it can be verified, and how to complete `login` over
  SSH — the callback lands on the remote host's loopback and the port is
  drawn at random, so it has to be forwarded, and nothing said so.

  The verification commands are the ones run against v1.0.0 itself, not
  transcribed from a template.

- **A code of conduct**, the Contributor Covenant 3.0, linked from the
  README and from `CONTRIBUTING.md`. Reports go through GitHub's private
  security-advisory flow, the same route `SECURITY.md` already used —
  no address, because the leak gate refuses one anywhere in the tree and
  a policy document is the wrong place to make the first exception.
  `SECURITY.md` existed and nothing linked to it either; both are in the
  documentation list now.

- **A Claude Desktop bundle on every release.** Open the `.mcpb` and
  Claude Desktop installs the server and asks for your OAuth client
  JSON, with no config file to edit — macOS, Windows and Linux on both
  architectures each.

  The version comes from one place: goreleaser's, which stamps the
  binary and is written into the manifest as it packs. The committed
  manifest carries a placeholder and the packer refuses anything else,
  so a manifest in the tree cannot claim a stale version. Packing runs
  as the universal binary's post hook — the one point where every binary
  exists and `checksums.txt` has not been written — which is what makes
  it possible for the bundle to be in that file, and therefore under the
  same signature as the archives. It is not what puts it there:
  goreleaser hashes the artifacts it built, and a file a hook drops into
  `dist/` is not one, so `checksum.extra_files` covers it and
  `release.extra_files` uploads it. A sibling repository following the
  hook alone packed a bundle that agreed about its version everywhere it
  was asked and was absent from `checksums.txt`, which is
  indistinguishable from a correct build unless somebody looks.

  The referential half of the packer's validation now runs on every
  commit as `gates mcpb`, with no build at all: whether `entry_point`,
  the platform commands and every `${user_config.x}` name something that
  will be staged is a question about the NAMES, and the names are static
  even when the binaries are not. A manifest pointing at a file nobody
  packs used to be a release-day failure and is a commit-day one now.
  The manifest is also refused for declaring a `platform_overrides` entry
  for a platform `compatibility.platforms` does not claim — an override
  nothing can reach is the same defect as a command nobody staged.

  macOS needed a universal binary, which this repository did not build;
  it is built for the bundle alone and kept out of the ordinary
  archives. Linux gets a launcher instead, because a manifest names a
  command per platform and has no key for the architecture.

  The bundle is packed in Go. The official packer is Node, and reaching
  for it would make `make check` depend on an interpreter nobody
  declared — a `.mcpb` is a deflate zip and the standard library writes
  one. What that CLI buys is validating the manifest against its
  published schema, and the replacement is better aimed rather than
  merely equivalent: the manifest is checked against the FILES ACTUALLY
  BEING PACKED. An `entry_point`, a platform override or a
  `${user_config.x}` that names something absent is well formed by any
  schema and produces a bundle that installs and then does nothing. All
  three are tested.

- **The README now looks like a released project's.** Status badges for
  CI, the latest release, the Go reference and the licence; a link to the
  latest release beside the `go install` line, with `--ignore-missing` on
  the checksum command because `checksums.txt` covers every archive and
  you will have taken one; a **How it works** section with the package
  layout and what each part is for; and a **Development** section naming
  the make targets and what `make check` actually runs.

### Note for whoever picks this up

Everything above is unreleased and sits on a topic branch. Nothing is
pushed. The open decision is whether it goes out as 1.0.1 or waits for
company: no code path changed, so nothing forces a release — but the
README people read is the one on the default branch, which updates on
merge rather than on a tag, and it was two releases stale until now.

Four things are recorded in §17a rather than done: the MCP registry
entry, a `live-cover` gate over the driver's option coverage, the
`transcript` gate, and a gate for "never assert an outcome the response
did not carry".

## [1.0.0] - 2026-09-06

### Added

- **The five destructive tools have run against Drive**, for the first
  time since they were written. `livedrive -destructive` makes a shared
  drive, exercises `delete_file`, `delete_revision`, `delete_comment`,
  `empty_trash` and `delete_drive` inside it, and deletes the drive
  again. §17a had deferred this since phase 2 for a good reason:
  `empty_trash` cannot be scoped to a folder, so without a drive of its
  own it takes the whole account's trash. Every call is scoped to the
  scratch drive, and the scoping is structural — `empty_trash` is named
  in exactly one method, and a test over the driver's syntax tree fails
  if a second mention appears.

  The same drive reaches the two states §17a said needed one: a file
  locked by an approval, and `lock_file`. An approved file cannot be
  cleaned out of a scratch FOLDER, which is why this waited for
  something that can be deleted whole.

### Fixed

- **`make check` and CI did not run the same gates, in both directions.**
  The `check` target calls itself "Everything CI runs" and was wrong
  twice over: `api-coverage` — the gate holding every one of the API's 91
  methods to a recorded decision — ran only locally, so it guarded
  nothing on a pull request; `schema-diff` ran only in CI, so the tool
  surface could be changed and pushed before anything objected.

  Both lists are correct now, and a `parity` gate compares them — against
  the gate program's own registry rather than only against each other, so
  a gate that exists and is run by neither is caught too. The reason they
  drifted will not go away: they live in different files and whoever adds
  a gate is thinking about one of them. Found by a sibling repository
  doing a cross-repo comparison, which is the same argument one level up
  — nothing inside a repository was going to notice.

  The gate then drifted from the program's own usage text in the very
  commit that added it, which is the argument for the registry: the
  dispatch, the usage and the parity check now read one list.

- **`list_activity` announced a new Drive API on every ordinary entry.**
  Drive records some activities without saying what happened, and phase 4
  built the code to tell that apart from an action kind this server has
  no words for — the first is nothing to worry about, the second means
  Google has added a thirteenth kind. It got the wire backwards. The
  ordinary case was read as a MISSING `primaryActionDetail`, and a probe
  of 400 activities finds that shape zero times: Drive sends `{}`, ten
  times in 400. So every ordinary entry went on being reported as the
  alarming one, which is the confusion the split was written to end.

  Both fixtures were written from the belief rather than from a response,
  so the test passed while asserting the opposite of what Drive does. The
  member names now decide it, and they are the only thing that can — an
  empty object and a member this server cannot name leave every field of
  the decoded struct nil, so the two were the same Go value. On a real
  account the count goes from ten false alarms to none.

  A kind Drive really has grown is now NAMED rather than counted. "Drive
  has grown one" leaves the reader with a probe to write before they can
  begin; the member name is the word they would be looking for, and this
  server has it in hand.

- **`lock_file` promised a lock Drive does not apply.** Starting an
  approval with it said "the file is LOCKED while the approval is open:
  nobody can change its content, including you" — written from the
  argument, and printed directly above a card showing no restriction at
  all. Two live runs then changed the content successfully. The sentence
  is read off the file now, so it is right whether or not Drive locks,
  and it does not credit this approval with a lock that was already
  there. `manage_approval`'s description and `lock_file`'s own schema
  said the same thing and have been corrected: a schema is read BEFORE
  the call, so an argument that overpromises there is worse than a
  result that does.

  Approving a file does lock it, exactly as described, and that is now
  verified live — the restriction reads "Locked for File Approval" and
  the next content change is refused for violating it.

- **`empty_trash` claimed an outcome it cannot know.** `files.emptyTrash`
  has no response — the reference gives it none — so nothing can be read
  back about what went. It said "is empty. Everything that was in it is
  gone for good" until a live run trashed a file, emptied that shared
  drive's trash, and restored the same file on the very next call. It now
  says Drive accepted the call, that the method reports nothing at all,
  and that the view it works from lags. The dry run's count carries the
  same warning: it counted a file trashed seconds earlier as nothing.

- **A shared drive was unreachable by its own id until the listing caught
  up.** Everything taking a `drive` argument resolved through
  `drives.list`, which is eventually consistent after a create, so a
  drive made moments ago was reported as an unknown NAME — with a list of
  unrelated drive names attached — while the caller was holding its id.
  `findDrive` falls back to `drives.get`, including when the listing is
  empty, which is the case an account with one new shared drive is in.

- **`delete_revision` disagreed with itself about a revision.** Drive's
  revision endpoints lag a write in both directions: one run had a
  revision `list_revisions` had just shown answer 404 and then delete
  successfully seconds later, and another had the dry run find it and the
  delete a second later not. Both refusals now say "or not yet" and
  suggest trying again, instead of explaining that the revision must have
  expired.

- **A refused approval named every cause but the likely one.** Answering
  an approval that is already approved, declined or cancelled is refused
  with the same bare `Permission denied` Drive gives someone who is not a
  reviewer, so the message has to offer the whole set. It offered two of
  three, and left out the only one the caller can have caused itself. The
  live run walked into exactly that — cancel an approval, then answer it
  — and was told to check a reviewer list the account was already on,
  which is a dead end. The finished case is named first now, and the
  message points at `list_approvals`, which says which of the three it is.

  Only where it can be true: the same refusal serves `list_approvals` and
  starting an approval, and neither names one that could be finished.
  Leading those with it would point the reader at a state that is not
  there — and tell somebody whose `list_approvals` just failed to call
  `list_approvals`.

## [0.4.0] - 2026-09-06

### Added

- **Three more agent evals, and the thirteen that existed have all run.**
  Ten of them had never been run since phase 3 built them; all thirteen
  pass against a real account. The three new ones cover phase 4's own
  surface, because the thirteen covered phases 0 to 3 and nothing else —
  and an eval is the only thing that tests a tool DESCRIPTION rather than
  a code path.

  A task can now say it is **unreachable**: it asks, before the agent
  runs and before any tokens are spent, whether this account can present
  the conditions it needs. Applying a label needs an administrator to
  have published one, and without that the task is unwinnable — a model
  would be marked down for a rule the server is right to have. An
  unreachable task is reported as not checked, which is not a pass. The
  idea came from a sibling repository that found a task of its own that
  could never have passed.

- **Workspace labels**, behind `GDRIVE_LABELS=true`. `list_labels` shows
  the definitions this account may use, with every field and the values
  it takes; `manage_labels` puts one on a file, sets or clears a field,
  or takes it off. One label and one field per call.

  Labels come through two APIs, and the split is where the mistakes
  live. The values on a file are Drive's, on the ordinary scope; the
  definitions are the separate Drive Labels API, with its own scope and
  its own enablement in the Cloud project. So an account that can reach
  Drive but not the Labels API can still apply and remove a label it
  knows the id of — only `set_field` is refused there, because the API
  has one setter per field type and the type lives in the definition.

  A wrong selection choice is answered with the choices that would have
  worked. A choice id is a generated string that appears nowhere else, so
  a refusal that did not list them would be a dead end.

- **Approvals**: `list_approvals` and `manage_approval` (start, approve,
  decline, cancel, comment, reassign). Two things are said in the
  description and again in every result, because neither is what "start
  an approval" sounds like it does: every action MAILS somebody, with no
  way to turn it off, and an approval can LOCK the file — at once with
  `lock_file`, or once it is approved. Declining completes an approval on
  its own where approving waits for everybody, and Drive cannot remove a
  reviewer at all.

- **Drive Activity**: `list_activity`, behind `GDRIVE_ACTIVITY=true`.
  What happened to a file, or to everything in a folder, with filters by
  kind and by time. It says who only as "you" or "somebody else", and
  says why: Drive's activity feed identifies people by an internal id and
  gives no name or address for them. A reader not told that would assume
  the names went missing here.

- **A Google Vid downloads.** It is the one file type with no bytes on
  the file endpoint and no export — Drive answers `fileNotExportable` —
  so `download_file` starts a long-running operation, polls while Drive
  renders the MP4, and fetches what it hands back. A render that takes
  longer than two minutes is reported as still running rather than as a
  failure, which needed a new error class: `pending` is not `server`, and
  a model told `server` would report a failure that did not happen.

- **`copy_file` can bring the comments**, with `copy_comments`. Off by
  default, and the result says out loud when a copy carried somebody
  else's words somewhere new.

- **`search_files` matches custom properties**, with `property`. A key
  alone finds every file carrying it; `key=value` matches the value too.

- **`update_file` can mark a file as opened**, with `viewed`, which is
  what puts it at the top of Drive's Recent view.

- **`upload_file` can ask Drive to index the content**, with
  `use_content_as_indexable_text`, so a type Drive does not read on its
  own can still be found by its words.

- **Every API method is used on purpose or left out on purpose.**
  `testdata/api-coverage.tsv` records all 91 methods of the three APIs
  this server can reach, 51 used and 40 not, each with its reason. `make
  api-coverage` holds the record to the code in both directions and runs
  in `make check`; `make api-diff` refetches the discovery documents and
  reports what has changed.

### Fixed

- **The live driver's transcript carried the maintainer's own name, in
  every result that named them.** The redactor's idea of a name required
  a capital letter, and a Workspace account with no display name set
  shows the address's local part instead — lowercase, dotted. It survived
  every position the redactor knows, because the SHAPE refused it before
  the position was ever consulted. The fixtures were the other half:
  every invented name in them was capitalised, so the tests agreed with
  the bug. Found by reading a live transcript rather than by any test.

- **`search_files` by property refused what Google's guide documents.**
  The search guide gives `properties has { key='department' }` as its own
  example of finding a file by a key whatever its value; Drive answers it
  400 "Invalid Value", in every spelling and for `appProperties` too.
  Both halves are required, and the server now says so rather than
  spending a round trip to be told less.

- **`list_activity` called an entry with no action "a kind this server
  has no words for".** Of 400 activities queried live, three carried no
  action detail at all — ordinary, and nothing to do about it. An action
  kind this server cannot name would mean Google had added a thirteenth,
  which is worth knowing. They are counted and reported separately now.

- **`get_file` had never been able to show a label.** Drive's
  `includeLabels` is a comma-separated list of label IDS — not a flag and
  not a wildcard — and this server sent `includeLabels=*`, which Drive
  answers 400. Nothing noticed for three phases: the feature is off by
  default, and no test could have seen it because the fake accepted
  whatever it was sent. Found by reading the discovery document, and
  confirmed by a probe against a real account, which is what makes it a
  fact rather than one bad response. `files.listLabels` is the method
  that answers "which labels are on this file", and it needs no labels
  scope at all.

- **Every date-valued label field decoded to nothing.** The wire type
  tagged the member `date` where Drive sends `dateString`. It failed
  silently, an absent member being indistinguishable from an unset one.

- **A `POST` that only reads took the write budget and would not retry.**
  `activity:query` is the first read-only POST this server makes.
  Deriving the rate class and the retry rule from the HTTP method — which
  is what phase 3 did, and was right about the dangerous direction —
  gets this one backwards: a listing spent from the write quota and
  failed closed on a dropped connection. A request carries a `reads`
  marker now, whose zero value is still the safe one.

- **The live driver's transcript hid a name only when an address stood
  beside it.** `internal/model` prints a person three ways — `Name
  (you)`, `Name <address>`, and a bare `Name` — and the redactor could
  find only the middle one. So the signed-in account's own name went
  into every transcript, and phase 3 made it worse by adding comments,
  whose author Drive gives no address for at all. A name has no shape of
  its own; what identifies it is the position this server printed it in,
  and those positions are a closed set because `internal/render` wrote
  every one of them. A full live transcript now carries the owner's name
  zero times, where it carried it in dozens of lines.

  The test that holds it does not describe the output, it **renders** it:
  a person is put through every renderer that can print one, and a name
  that survives fails. A renderer that starts printing a person somewhere
  new is caught without anybody remembering to add a case — which is
  exactly what did not happen when comments arrived. Its floor caught
  three fixtures that proved nothing, one of them a real fixture bug.
- **The leak gate treated a subdomain of a documentation domain as
  real.** RFC 2606 reserves `example.com`, `.org` and `.net` and
  everything beneath them, so `someone@corp.example.net` is as safe as
  `someone@example.net`. The exact-match rule flagged one while a test
  was being written to close a real leak — a gate that makes fixtures
  weaker is working against its own purpose.

- **The download eval asserts the behaviour it was explaining.** It had
  been rewritten to stop failing on a working download — `download_file`
  appends a short id so two files of one name land beside each other
  rather than on each other — and then carried a comment saying why it
  did not check the name. It now downloads two files that really do share
  a name and asserts both survive, which is the same cost and does not
  decay into a comment. Suggested by a sibling repository.

- **A share refused for a fixable reason no longer claims your
  organisation forbade it.** Sharing with an address that has no Google
  account behind it answers 400 `invalidSharingRequest`, and every
  `invalidSharingRequest` was mapped to `[blocked]` — so the server
  reported "your organisation's sharing policy does not allow this…no
  option here can work around it" when Google's own message said the
  opposite: check the Notify people box. Google uses that reason for a
  policy refusal and for a malformed request, and the status tells them
  apart. It is `[blocked]` at 403 and `[invalid]` at 400 now, and the
  fix Google named survives in the message. Found by the new
  `livedrive -blocked` check, which then had to be fixed too: it had
  compared the class against `blocked`, seen `blocked`, and called it
  confirmation.

- **A correction the phase-1 branch stranded, and one claim it was
  right about.** `.goreleaser.yaml` said the separate
  `--output-signature` and `--output-certificate` flags were "deprecated
  and, with the new bundle format, ignored". cosign's v3.0.1 release
  notes say something different and more useful: `--bundle` moved from
  optional to **required**, and they do not mention the old flags at
  all. So the first `v0.0.1` release did not degrade quietly, it failed
  with no output path — which is the whole reason that release attempt
  died. Re-verified against those notes before landing.

- **Every workflow pins its shell, at workflow level.** The Windows
  runner's default shell is PowerShell, and it does not read
  `-coverprofile=cov.out` the way bash does: the coverage profile went to
  a file no step referred to, and nothing noticed while the only step
  that read it was skipped on that platform. v0.3.0 fixed it on the one
  job that had failed; this puts it on the file, so a job added later
  inherits it, and `gates pins` now refuses a workflow without it —
  including one that sets the shell under a job, which reads as correct
  and is not.
- **The two eval tasks that could never pass now say what they check.**
  `download-a-file` looked for the file under the name Drive holds, and
  `download_file` deliberately appends a short id so two files of one
  name land beside each other; `share-as-commenter-quietly` asserted a
  grant to `someone@example.com`, which Drive refuses because it is
  IANA's reserved documentation domain. The first checks what actually
  landed, and the second scores the call and says out loud that the grant
  went unverified unless `-share` names an address Google will accept.
  All thirteen tasks pass against a real account now.

## [0.3.0] - 2026-09-06

Collaboration, resources and the numbers. Six new tools, one of them
registered only when the deployer asks and needing `confirm: true` on the
call as well; three `gdrive://` resources; a recursive copy; and the
first benchmarks, which refuted two of the performance targets this
project had written down.

### Added

- `list_comments`, `add_comment` and `reply_comment`: the threads on a
  file, with their replies and whether each is still open. Comments live
  on the file in Drive, not inside the document, so these work for a PDF
  or an image as well as for a Google Doc — which is the reason to have
  them here rather than in a server built on the Docs API. A comment made
  through this server is unanchored: pinning one to a passage means
  knowing where that passage is, and that is the Docs API's job.
  `reply_comment` answers, resolves, reopens or edits; Drive records
  resolving as a reply of its own, so everybody who can see the file sees
  who closed a thread.
- `list_access_requests` and `resolve_access_request`: who has asked to
  be let into a file, and accepting or denying one. Only an approver can
  see them, and the API cannot create one — that happens when somebody is
  turned away from a file. Accepting grants a permission, so it is a
  sharing tool: `GDRIVE_SHARING=off` removes it, `capabilities.canShare`
  is checked first, and the result reports who could see the file before
  and who can see it after. A request that names more than one role is
  refused as `[ambiguous]` rather than accepted as one of them.
- Gated behind `GDRIVE_ENABLE_DESTRUCTIVE=true`: `delete_comment`, which
  also needs `confirm: true`. There is no trash for a comment — Drive
  keeps the thread with its words removed — so resolving is what closes a
  conversation and this is what removes it.
- **Resources.** `gdrive://<id>` is a file's text, `gdrive://<id>/meta`
  is its description, and `gdrive://<id>/children` is a folder's first
  page. A reference with a slash in it has to be percent-encoded, which
  is the price of templates that cannot shadow each other. There is no
  static resource list: enumerating a Drive is a listing, and a client
  would pay for one on every connection.
- **`copy_file` with `recursive`** copies a folder and everything in it.
  The tree is walked in full before anything is written, and one that
  does not fit the budget is refused with nothing copied rather than
  copied halfway — a listing that stops short is a listing that stops
  short, and a copy that stops short leaves a folder that looks complete
  and is not. `dry_run` says how big it is first. A shortcut inside a
  tree is made again pointing where it points now, not at the copy.
- **`make bench`** and **`make evals`**. The benchmarks measure what §11
  of the architecture promised; the evals run thirteen tasks through an
  agent with only this server's tools and score both the end state and
  the trace — no invented ids, and `allow_anyone` never passed unasked.

### Changed

- **One media type registry.** Five tables in three packages knew what a
  media type meant: a display name, a search filter, an export name, the
  format a read takes and the format a download defaults to. They are one
  table now (`internal/mediatype`), which is what makes adding a kind one
  edit instead of four.
- `download_file`'s schema now offers `zip` and `json`, which the server
  has always accepted and the description never mentioned. The check that
  ties the two together found it the first time it ran.
- **`get_file` costs one call plus one per folder above the file**, not
  "at most two" as §11 claimed. Two holds for a file at the top of My
  Drive; below that the location line is a climb. Nothing changed in the
  code — the target was wrong, and it is now stated as the code behaves
  and asserted in a test.

## [0.2.0] - 2026-09-05

Access, shared drives and history — and the run that found what the tests
could not. Twelve new tools, four of them registered only when the
deployer asks and each needing `confirm: true` on the call as well.
Verified against a real Google Workspace account over three runs: the
first two reported "all calls behaved as expected" while three results
were wrong, because a call succeeding and a call telling the truth are
different questions.

### Added

- `list_permissions`: who can see a file or a shared drive, with each
  grant's role in plain words, when it expires, whether it reaches people
  by link or by search, and where it came from. An inherited shared-drive
  grant says so and names its source, because that is the only place it
  can be removed.
- `share_file`: grant or change one principal's access, reporting who
  could see the file before and who can see it after — the grant is the
  small half of the answer. Granting to somebody who already has access
  changes their role rather than adding a second grant. A link anyone can
  open needs `allow_anyone: true`; handing over ownership needs
  `transfer_ownership: true`. No notification mail unless `notify` is
  set, which is the opposite of Drive's own default; where Google forces
  it on, the result says so. An organisation's policy refusal comes back
  as `[blocked]` with Google's own words and who set it.
- `unshare_file`: revoke one grant, or the link that let anybody open it,
  and say what access is left. An inherited grant is refused with its
  source named, and an owner's access is not revoked but transferred.
- `list_drives` and `manage_drive`: the shared drives this account can
  see with what it may do in each, and create, rename, hide, unhide or
  restrict one. Membership is not here — a member is a permission on the
  drive, so `share_file` does it and one place decides who sees what.
- `list_revisions` and `manage_revision`: a file's version history newest
  first with the current version marked, and pinning so Drive does not
  discard a version after thirty days. For a Google document the result
  repeats Google's own caveat that the list can be incomplete.
- `list_changes`: the changes feed. With no token it hands back the
  starting point and says the feed has no beginning; with one it lists
  what happened and carries the token for next time. A trashed file and
  one that is gone for good read differently, because only one can be
  undone.
- Gated behind `GDRIVE_ENABLE_DESTRUCTIVE=true`: `delete_file`,
  `empty_trash`, `delete_drive` and `delete_revision`. Each also needs
  `confirm: true` on the call, because a registered tool is one a model
  will reach for eventually. `empty_trash` is the only one that names no
  item, so it reports how much is in the trash before destroying it.

### Fixed

- **A file id spelling a sibling endpoint could turn a bounded delete
  into an unbounded one.** Drive puts non-id endpoints under `/files` as
  sibling segments, so `delete_file` on an id of `trash` would have built
  `DELETE /files/trash` — `files.emptyTrash`, destroying an entire trash
  instead of one file. `url.PathEscape` does not prevent it, because
  every character in the word is legal in a path segment. Every
  `/files/{id}` path now goes through one guard that refuses the reserved
  segments; it was unreachable before only because a lookup two layers
  above happened to fail first.
- **Deleting the top of a drive is refused by this server, not only by
  Google.** `root` is Drive's alias for My Drive's root folder and a
  shared drive's id is its own root folder's id, so either could be
  passed where a file was expected. Google refuses both through
  `capabilities.canDelete`; a bounded call becoming an unbounded one must
  not rest on a field the other side computes, so it is now refused here
  as well, and a test asserts the refusal holds with the capabilities
  stripped off.
- **A throttled request could be classified as a permission error.**
  Google spells one condition two ways — `rateLimitExceeded` in the
  legacy error envelope and `RATE_LIMIT_EXCEEDED` in a
  `google.rpc.ErrorInfo` detail — and this client prefers the detail
  while comparing the camelCase spelling exactly, so every reason that
  arrived the modern way missed. Reasons are now compared in a form that
  folds both spellings. The in-memory Drive used to send the same string
  in both places, which is why no test caught it; it now sends each in
  its own spelling, as Google does.
- **`dailyLimitExceeded` is recognised, and deliberately not retried.**
  It is a 403 quota reason like the others, but backing off cannot free a
  daily quota, so retrying only spent attempts and the advice "wait a
  minute and try again" was false. It is reported as rate limiting with
  what actually happened.
- **A token refresh could hang for the life of the process.** The refresh
  runs inside the oauth2 transport against the context the token source
  was built with, so the per-request deadline never reached it, and
  without a client of our own it used `http.DefaultClient`, which has no
  timeout. `docs/security.md` claimed every token refresh ran under a
  deadline; it does now, and so does `login`'s code exchange.
- **A null entry in a Drive response could take the whole server down.**
  JSON can carry a null in an array; `model.NewChange` returned nil for
  one and the renderer dereferenced it. A malformed page now costs one
  missing row rather than a SIGSEGV, guarded in the service and again in
  the renderers, which are pure and are the last thing between a response
  and the process. The same shape was closed for revisions and drives.
- **`empty_trash` counted a different set of items than it deletes.** The
  count listed everything trashed this account could see — a shared
  drive's trash, and files owned by other people — while the call deletes
  only this account's own trashed files. On an account with a busy shared
  drive it could announce hundreds of items and destroy a dozen. It is
  the one destructive call whose whole safety story is saying how much it
  is about to destroy.
- **An unreadable permission list was reported as "that grant does not
  exist".** `unshare_file` swallowed a failed `permissions.list` and then
  concluded nothing matched, telling the model a file was already
  unshared when the truth was unknown — wrong in the direction that
  matters. It now says what actually went wrong.
- **`share_file` silently narrowed a link grant.** `discoverable` was a
  plain bool, so "not passed" and "false" were the same request:
  changing the role on a file that was findable by search quietly made it
  by-link-only. It is a pointer now, and leaving it out keeps whatever
  the grant has.
- **An expiry could be set and moved but never removed.** `expires:
  never` clears one; leaving `expires` out still means "do not touch it".
- **A 429 could override the daily-quota decision.** The status was
  consulted before the reason, so a 429 carrying `dailyLimitExceeded`
  was retried through the whole backoff schedule — exactly the loop the
  fix above exists to prevent. The reason decides; an unlabelled 429 is
  still treated as a burst, which is the safe reading.
- **The ownership-transfer note asserted a flow that had not been
  observed.** It stated that a consumer account produces a pending
  transfer; this server never sets `pendingOwner` and nobody has watched
  Drive's behaviour here. The note now states the one certain
  consequence — this account becomes a writer — and reports a pending
  transfer only when the answer shows one. Spike F settles the rest.
- **`manage_revision` rendered a poorer card when it worked than when it
  did not.** The success path lost the followed shortcut and the shared
  drive's name, so the same input described itself differently on the
  second call.
- **The first `list_changes` call contradicted itself**, saying "nothing
  has changed since that token" about a call that named no token, which
  reads as a completed poll.
- **`manage_drive` with `action: restrict` and no restrictions** reported
  that every restriction already had the value asked for, when none had
  been asked for.
- **`TestLogsCarryNoTraceOfWhatWasTouched` had stopped covering the whole
  surface.** It was written for phase 1's tools and eight more were added
  around it, including the only ones that take an email address as an
  argument. It covers them now, a domain joined the forbidden fixtures,
  and `method=DELETE` joined the assertion that the writes actually
  reached the network. `docs/architecture.md` §17b had gone on claiming
  otherwise.
- **`goreleaser-action` was still not pinned.** `~> v2.18.0` reads like a
  pin and is not: the action's own README says the input takes "a max
  satisfying semver one", so any 2.18.x could decide what the release
  artifacts are. It is `v2.18.0` now, with no operator. The narrowing
  from `~> v2` had been recorded in the evidence log as a fix.

### Fixed (found by the live runs, and confirmed fixed by a third)

- **A file card showed the exposure the call had just changed.** The card
  was rendered from the file read before the write, so `share_file` on a
  private file reported `sharing: private to you` in the same result
  whose change line said the file was now reachable by anyone with the
  link. That line is the one a person checks to see what they just
  exposed, and it was stale exactly when it mattered most. Both sharing
  tools now render from the file as it is afterwards, which also replaces
  the separate permission re-read they used to do.
- **Removing the last grant reported "shared, but no grants are visible
  to this account"** instead of "private to you", because the summary
  was built from a fresh permission list and the stale file's `shared`
  flag.
- **An inherited grant on a My Drive file was blamed on a shared drive.**
  The reference says `inheritedFrom` "is only populated for items in
  shared drives", so an empty one means a folder above — not a drive.
  Every My Drive file with an inherited grant said "inherited from the
  shared drive", including the owner's own grant on a file that had never
  been near one.
- **The live driver could not tell a working changes feed from a broken
  one.** Drive's feed is eventually consistent, and asking a second after
  a write returned "0 changes" on two runs — which reads as a feed
  working and reporting nothing. It now polls, and says which happened
  rather than printing an empty answer and moving on. The feed does
  work: the third run reported the change and handed back a fresh token.
- **The live driver left a permission id unredacted.** A permission id
  for a person is twenty digits with no letter, and the redactor's rule
  required a capital and a digit. It identifies a Google account.

### Added (tooling)

- Spike F in the live driver now **reads the transfer back**. It records
  the owner before, transfers, then reads the file again and compares:
  a call that answers 200 and leaves the owner where it was looks
  identical to one that worked, from the result alone. It reports three
  outcomes — the owner changed, the owner did not change, or the file
  could not be read back at all — because "unknown" and "it worked" must
  not print the same. The owner is compared, never printed.

- Both leak modes now fail rather than passing quietly when they find
  nothing to scan. "I found nothing" and "I had nowhere to look" printed
  the same sentence, so a gate run from the wrong directory would have
  reported a clean tree for ever.
- `gates pins`, in `make check` and in CI: every tool version a workflow
  installs must be exactly one version. It exists because a comment could
  not hold this shut — the comment beside the wrong value said which half
  of the pin mattered, and the value was still a range. It rejects `~>`,
  `^`, `latest` and a bare major, and fails rather than passing quietly
  if it finds no workflows or no versions to check.

- `gates leaks history` had never done the job its own documentation
  describes, in either direction. It scanned annotated tag objects
  including the `tagger` line git writes itself, so it failed on this
  repository's own tags — and a gate that always fails is a gate that
  gets ignored. It also skipped commit objects entirely, so "an id in a
  commit message", the case the gate's comment names, was never checked
  at all. A blob is now scanned whole; a commit or tag is scanned from
  its message down, because an identity is public in every repository by
  construction and is not something anyone chose to publish here. Three
  tests cover it, including the same address in a header and in a
  message with two different verdicts.
- `.claude/settings.local.json` and the editor's temporary copies of it
  are ignored. One reached a commit through `git add -A`; its content is
  a permission allowlist — public documentation domains and shell command
  patterns — and it is out of the working tree now, though the blob
  remains in the history (see below).

## [0.1.0] - 2026-09-05

Content and organising: a file's text out, a file in, and everything
that arranges what is there. Verified against a real Workspace account,
including a file moved into a shared drive and back, which refuted five
things the design had asserted since phase 0 — two of them making a tool
fail outright.

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
- The sharing line of a file in a shared drive counted its people and
  then counted the inherited grants again — "shared with 4 people: 4 can
  edit … 4 inherited from the shared drive" invites the arithmetic
  4 + 4 — and named the drive twice. Where the grants come from is part
  of the same clause now. Seen for the first time when a file was moved
  into a real shared drive: the fake had no members to inherit from.

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

[1.0.0]: https://github.com/mmedum/google-drive-mcp/compare/v0.4.0...v1.0.0
[0.4.0]: https://github.com/mmedum/google-drive-mcp/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/mmedum/google-drive-mcp/compare/v0.2.0...v0.3.0
[0.0.1]: https://github.com/mmedum/google-drive-mcp/releases/tag/v0.0.1
