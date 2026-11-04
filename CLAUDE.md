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

1. **Never commit anything internal**: file or folder ids or URLs, the
   names or content of real files, account addresses, organization
   names, Cloud project ids, OAuth client ids or secrets. Not in code,
   docs, fixtures, transcripts, commits, pull requests or logs. Naming a
   sibling server, such as google-sheets-mcp, is fine. A fixture id says
   it is synthetic (D-07, D-08). `make leaks` and gitleaks hold it.
2. **Never write to stdout outside `main`.** Stdout carries only MCP
   JSON-RPC frames; logs go to stderr through `slog` (D-05). forbidigo
   and `make smoke` hold it, with `scripts/` excluded.
3. **Never log a name, path, address, query or content** (D-06).
   `TestLogsCarryNoTraceOfWhatWasTouched` holds it.
4. **Never take the first match or guess an id.** A name or path that
   matches several items is `[ambiguous]` with every candidate (D-12).
   `TestResolveAmbiguousPathListsCandidates` holds it.
5. **Never widen access silently.** Check `canShare` first, show who
   could reach the item before and after, and send no mail unless asked
   (D-35, D-36, D-37). `share_file` reaches named people and groups
   inside the organization (D-38), and a move, a copy or a folder's
   limited access turned off never reaches past it (D-42).
   `TestShareFileRefusesWhatReachesPastTheOrganization` holds it.
6. **Never make a risky variant an argument.** A link, a domain, an
   outside address, a new owner and lifting a drive's restriction are
   tools of their own behind `GDRIVE_ENABLE_DESTRUCTIVE`, repeating the
   name of what they open (D-40, D-41).
   `TestTheWideSharesNeedTheDestructiveFlag` holds it.
7. **Never destroy without a way back by default.** Trash is the removal;
   a permanent delete registers only with `GDRIVE_ENABLE_DESTRUCTIVE`
   and needs `confirm: true` (D-48). No bulk delete (D-49), and a bounded
   delete names one resource (D-50). `extract_text` deletes only its own
   copy (D-22). `TestDestructiveToolsExistOnlyWhenAskedFor` holds it.
8. **Never touch a local file outside `GDRIVE_LOCAL_DIR`**, or hold a
   whole file in memory (D-23, D-24).
   `TestUploadsRefuseAPathOutsideTheLocalDirectory` holds it.
9. **Never ask the person anything, or let a tool write the server's
   settings** (D-44, D-47). Annotations are exact (D-45), and the
   `requiresUserInteraction` mark stays on the tools D-46 names.
   `TestNoToolAsksThePerson` holds it.
10. **Never repeat a write that may have landed**: it is
    `[ambiguous_outcome]` (D-28). Never build an outcome from the
    request (D-29). `TestOnlyAWriteThatMayHaveLandedIsAmbiguous` and
    `make outcomes` hold them.
11. **Never import `google.golang.org/api`** or another project's code;
    extend `internal/gdrive` (D-09). depguard refuses it. Never call a
    method or model a field without a verdict in
    `testdata/api-coverage.tsv` or `testdata/api-fields.tsv` (D-69).
12. **Never rename or drop a released tool, argument, output field or
    resource template.** `make schema-diff` fails on a break no
    `**Breaking:**` entry names; a break ships in a new major (D-64).
13. **Never emit an error outside `[class] message`.** `make classes`
    holds the vocabulary closed (D-51).
14. **Never let the live driver or the evals touch what they did not
    make, or print except through the transcript** (D-71, D-72).
    `make transcript` holds the second.
15. **Never commit to `main` or push**; the owner pushes, tags and
    merges (D-81).
16. **Never adopt a belief about Google without a primary source**: the
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
  Never write findings into `docs/`.
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
- **Drive's reads lag its writes.** Never assert what the response did
  not carry, and never sleep and retry in a tool (D-30).
- **`empty_trash` takes the whole account's trash.** The live driver
  names it in one place, inside its scratch shared drive;
  `TestEmptyTrashIsNamedInOnePlace` holds it (D-71).
- **A linter settings block that fails to load leaves its defaults on**,
  still firing. Check the message text of forbidigo and depguard.
- **Never delete a decision or reuse an ID.** Supersede or deprecate it,
  and take the next ID from the file's `Next ID:` line.

## Writing

Plain and short: lead with the outcome, one idea per sentence, no
narration, no count a gate does not check. History goes in commit
messages. `release.yml` lifts the CHANGELOG section verbatim into the
release notes, so the rules in `CONTRIBUTING.md` are not optional.
