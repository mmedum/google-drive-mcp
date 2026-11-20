# CLAUDE.md — google-drive-mcp

A Go MCP server for Google Drive over stdio: one binary per person,
their own OAuth client, nothing hosted (D-01). It stops at the file
boundary (D-02). Why the code is the way it is lives in
`docs/architecture.md`, one decision per `D-NN`; read it before changing
the tool surface, the addressing, the sharing policy or the transfers.

## Commands

- `make check` runs every gate CI runs; its `check` target is the list.
  `make hooks`, once per clone, adds the pre-commit scan.
- `make live` drives the built binary against the signed-in account;
  `LIVE_ARGS` passes its modes, such as `-write`. Read the transcript.
  `CONTRIBUTING.md` covers it, `make integration` and `make evals`.
- `make api-diff` and `make schema-refetch` are release steps in
  `docs/release.md`.
- `go run ./scripts/gates` lists the gates; add a name to run one.

## Hard rules

1. **Keep everything internal out of commits**: file or folder ids or
   URLs, the names or content of real files, account addresses,
   organization names, Cloud project ids, OAuth client ids and secrets.
   That covers code, docs, fixtures, transcripts, commits, pull requests
   and logs. Naming a sibling server, such as google-sheets-mcp, is
   fine. A fixture id says it is synthetic (D-07, D-08). `make leaks`
   and gitleaks hold it.
2. **Write to stdout only in `main`.** Stdout carries only MCP JSON-RPC
   frames, because a stray print corrupts the stream; logs go to stderr
   through `slog` (D-05). forbidigo and `make smoke` hold it, with
   `scripts/` excluded.
3. **Log truncated ids, counts and latencies, not a name, path,
   address, query or content**, because a log somebody pastes into a bug
   report has to be safe by construction (D-06).
   `TestLogsCarryNoTraceOfWhatWasTouched` holds it.
4. **Return every candidate rather than taking the first match or
   guessing an id.** A name or path that matches several items is
   `[ambiguous]` with every candidate (D-12).
   `TestResolveAmbiguousPathListsCandidates` holds it.
5. **Make every widening of access visible**, because a share's harm
   does not show in the item itself. Check `canShare` first, show who
   could reach the item before and after, and send mail only when asked
   (D-35, D-36, D-37). `share_file` reaches named people and groups
   inside the organization (D-38), and past it only to narrow a grant
   that exists (D-91). A move, a copy or a folder's limited access
   turned off that would reach past it is refused (D-42).
   `TestShareFileRefusesWhatReachesPastTheOrganization` holds it.
6. **Give each risky variant its own tool rather than an argument**,
   because a client remembers an approval per tool. A link, a domain,
   an outside address, a new owner and lifting a drive's restriction
   are tools of their own behind `GDRIVE_ENABLE_DESTRUCTIVE`, repeating
   the name of what they open (D-40, D-41). Narrowing such a grant is
   not risky, so `share_file` does it (D-91).
   `TestTheWideSharesNeedTheDestructiveFlag` holds it.
7. **Keep a way back by default.** Trash is the removal; a permanent
   delete registers only with `GDRIVE_ENABLE_DESTRUCTIVE` and needs
   `confirm: true` (D-48). Delete one item per call, not in bulk
   (D-49), and a bounded delete names one resource (D-50).
   `extract_text` deletes only its own copy (D-22).
   `TestDestructiveToolsExistOnlyWhenAskedFor` holds it.
8. **Touch local files only inside `GDRIVE_LOCAL_DIR`, and stream them
   in chunks rather than holding a whole file in memory** (D-23, D-24).
   `TestUploadsRefuseAPathOutsideTheLocalDirectory` holds it.
9. **Leave approval to the client and the server's settings to the
   deployer**: the server asks the person nothing, and no tool writes a
   setting (D-44, D-47). Annotations are exact (D-45), and the
   `requiresUserInteraction` mark stays on the tools D-46 names.
   `TestNoToolAsksThePerson` holds it.
10. **Report a write that may have landed as `[ambiguous_outcome]`
    rather than repeating it** (D-28). Build an outcome from what
    Drive's answer carried, not from the request (D-29).
    `TestOnlyAWriteThatMayHaveLandedIsAmbiguous` and `make outcomes`
    hold them.
11. **Extend `internal/gdrive` rather than importing
    `google.golang.org/api` or another project's code**, because the
    generated module drags in gRPC, OpenTelemetry and the cloud auth
    stack (D-09). depguard refuses it. Give a method or a field a
    verdict in `testdata/api-coverage.tsv` or `testdata/api-fields.tsv`
    before calling or modeling it (D-69).
12. **Keep every released tool, argument, output field and resource
    template rather than renaming or dropping one**, because a model, a
    saved prompt or a script builds against the surface.
    `make schema-diff` fails on a break no `**Breaking:**` entry names;
    a break ships in a new major (D-64).
13. **Emit every error as `[class] message`**, because the class tells
    the model what to do next. `make classes` holds the vocabulary
    closed (D-51).
14. **Let the live driver and the evals touch only what they made, and
    print only through the transcript** (D-71, D-72). `make transcript`
    holds the second.
15. **Commit on a branch, not `main`, and leave pushing to the owner**,
    who pushes, tags and merges (D-81).
16. **Back every belief about Google with a primary source**: the
    discovery document or a live probe (D-68). Record a verified Google
    behavior in the comment at the line that depends on it, with the
    source link and the live step that checks it.

## Definition of done

- `make check` passes. On a pull request CI also runs `schema-ack`,
  which needs a commit footer naming each changed item of the tool
  surface (`CONTRIBUTING.md`).
- New behavior has tests, and the schema diff has been read.
- `/simplify` and `/code-review medium` have run. A fixed finding is one
  line in the commit body. A declined one goes in the PR body, or
  becomes a present-tense constraint comment at the line it protects.
  Findings go there, not into `docs/`.
- Anything touching the write path or an API response shape has had a
  `make live` run whose transcript was read (D-70).

## Traps

- **Green gates are not done.** `internal/gapi/drivetest` is written
  from belief about Drive; only `make live` and `make integration` test
  it (D-67, D-70).
- **`dry_run` is taken by the writes that move, share, copy, trash,
  grant or destroy, not every write** (D-31). Each returns before
  sending, with no guard in the API client, so a write that takes it
  needs its own "sends nothing" test.
- **A short or empty page is not the end of a listing** (D-16).
- **Drive's reads lag its writes.** Assert only what the response
  carried, and say the answer may be stale rather than sleeping and
  retrying in a tool (D-30).
- **`empty_trash` takes the whole account's trash.** The live driver
  names it in one place, inside its scratch shared drive;
  `TestEmptyTrashIsNamedInOnePlace` holds it (D-71).
- **A linter settings block that fails to load leaves its defaults on**,
  still firing. Check the message text of forbidigo and depguard.
- **Supersede or deprecate a decision rather than deleting it.** Take
  the next ID from the file's `Next ID:` line rather than reusing one.

## Writing

Plain and short: lead with the outcome, one idea per sentence, no
narration, no count a gate does not check. History goes in commit
messages. `release.yml` lifts the CHANGELOG section verbatim into the
release notes, so follow the rules in `CONTRIBUTING.md` exactly.
