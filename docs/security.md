# Security model

The threat model and the invariants the code holds.
[`SECURITY.md`](../SECURITY.md) says how to report a vulnerability.

It applies to one binary, running as a subprocess of your MCP client, on
your machine, with your own OAuth client and your own token, against
Google's APIs only (D-01, D-04). It has no listener except while `login`
waits, no telemetry and no update check.

## Trust boundaries

| Boundary | Crosses | Assumption |
|---|---|---|
| MCP client ↔ this server | An OS pipe | Both run as the same OS user, unauthenticated. |
| This server ↔ Google | TLS | Credentials go only to `googleapis.com` and its subdomains, `*.googleusercontent.com`, `accounts.google.com` and `drive.google.com`, over HTTPS, checked with the port before a credential is attached (D-04). |
| This server ↔ disk | The file system | The config directory holds the profile and, without a keyring, the token file (D-60). Files move only through `GDRIVE_LOCAL_DIR` (D-23). |
| Browser ↔ the login listener | The loopback interface, on a random port | Alive only while `login` waits. PKCE and `state` are checked on the callback (D-59). |
| File authors ↔ the model | Every name, file's text and comment a tool returns | Written by other people, sometimes to steer an assistant (D-11). |

## What is worth stealing

- **The refresh token.** The `drive` scope reads, changes, shares and
  deletes every file the account can reach (D-56). It lives in the OS
  keyring where there is one, and in an owner-only file where there is
  not (D-60).
- **File content.** It passes through tool results to your client, and
  to disk only under `GDRIVE_LOCAL_DIR`.
- **Who can reach a file.** Taking a share back does not take back what
  was read.
- **The files themselves.** A permanent delete skips the trash and has
  no undo.

## Adversaries

**A collaborator writing to steer your assistant.** The server cannot
stop a model being persuaded by a file's name, text or comments, so it
limits what persuasion reaches. It never acts on what a file says, and
quotes comment text as somebody else's words (D-11). `share_file` reaches only named people and groups inside
the organization (D-38), and nothing widens by a move, a copy or a
folder's limited access turned off (D-42). The permanent deletes and the
wider shares are absent unless the deployer turns them on (D-40, D-48),
and no tool changes the server's own settings (D-47).

**A malicious MCP client, or a persuaded model driving an honest one.**
It can call any registered tool as you, so registration is the
control. `confirm: true` and
the name a wide share repeats are witnesses the model writes, never your
approval (D-41, D-48). Past that, Google's sharing policy and the guards
below are the bound.

**A co-resident process.** It cannot finish a login without the PKCE
verifier, and the token file is readable by your account alone.

## Invariants the code holds

A test or a gate holds each of these. A way to break one is a
vulnerability.

- **Stdout carries only JSON-RPC.** forbidigo, the smoke test and
  `TestStatusJSONFlagIsTheWholeOfStdout` hold it (D-05).
- **Credentials go only to Google's hosts.**
  `TestRefusesToSendCredentialsElsewhere` and `TestAllowsGoogleHosts`
  hold it (D-04).
- **Logs never carry names, paths, addresses, queries or content**, and
  a transport error loses its query. `TestLogsCarryNoTraceOfWhatWasTouched`
  and `TestATransportErrorDoesNotCarryTheSearchTerms` hold it (D-06).
- **A name or path that matches several items is never resolved by
  picking one.** `TestResolveAmbiguousPathListsCandidates` holds it
  (D-12).
- **No ordinary tool reaches past the organization.**
  `TestShareFileRefusesWhatReachesPastTheOrganization`,
  `TestAMoveThatReachesAnyoneIsRefusedNamingThem` and
  `TestACopyThatReachesAnyoneIsRefused` hold it (D-38, D-42).
- **A wide share is a tool of its own, behind the destructive flag, and
  holds the item to the name it repeats.**
  `TestTheWideSharesNeedTheDestructiveFlag` and
  `TestAWideShareHoldsItsFileToTheName` hold it (D-40, D-41).
- **A share shows who could reach the item before and after, and sends
  no mail unless asked.**
  `TestShareFileGrantsAndShowsExposureBeforeAndAfter` holds it (D-36,
  D-37).
- **The permanent deletes are absent without `GDRIVE_ENABLE_DESTRUCTIVE`,
  need `confirm`, and never register read-only.**
  `TestDestructiveToolsExistOnlyWhenAskedFor`,
  `TestDestructiveToolsAlsoNeedConfirmOnTheCall` and
  `TestDestructiveToolsAreNeverAvailableReadOnly` hold it (D-48).
- **A bounded delete names one resource.**
  `TestAFileIDThatNamesASiblingEndpointIsRefused` and
  `TestDeleteFileWillNotDestroyTheTopOfADrive` hold it (D-50).
- **`extract_text` deletes only the copy it made.**
  `TestExtractTextDeletesOnlyTheGoogleDocItsCopyMade` holds it (D-22).
- **Local files stay in `GDRIVE_LOCAL_DIR`**, symlinks resolved, and a
  download never overwrites. `TestUploadsRefuseAPathOutsideTheLocalDirectory`
  and `TestDownloadFileNeverOverwrites` hold it (D-23).
- **A login checks `state` and uses PKCE.**
  `TestLoginRejectsAStateMismatch` holds it (D-59).
- **The token file is owner-only, with a warning when the keyring fails**, and
  the environment's token overrides both stores.
  `TestReadingTheTokenFileWarnsOnlyWhenTheKeyringIsBroken` and
  `TestEnvironmentOverridesEverything` hold it (D-60).
- **The annotations are exact, the server asks the person nothing, and
  no tool writes its settings.**
  `TestOnlyToolsThatAddSayTheyAreNotDestructive`,
  `TestTheMarkSitsOnlyWhereItsPromptIsThePoint`, `TestNoToolAsksThePerson`
  and `TestNoToolWritesTheServersOwnSettings` hold it (D-44 to D-47).
- **Read-only mode asks for read-only scopes and registers only the
  reads and `download_file`.** `TestReadOnlyModeRegistersNoWriteTools`
  and `TestScopes` hold it (D-56, D-57).
- **Nothing in the repository identifies a real file, account or
  organization.** `make leaks` and gitleaks hold it (D-07), and fixtures
  are synthetic (D-08). The live driver and the evals print only through
  the redacting transcript, which `make transcript` holds (D-72).

## Not a vulnerability

Do not report these as vulnerabilities. Open an ordinary issue if one
surprises you.

- **The client did not prompt.** Annotations and the
  `requiresUserInteraction` mark are hints, and every widely used client
  can run a tool with nobody approving. A gated tool registered while
  its switch is off is a vulnerability.
- **What runs as you can act as you.** Any process running as you can
  read the token and use it.
- **The scope reaches every file the account can.** `drive.file`
  reaches only files the app created or the person opened through
  Google's Picker, which a stdio server cannot show.
- **Google's policy allowed a share.** The organization's policy is the
  authority (D-35). A grant to a group reaches everyone in it, members
  added later included, and the server cannot see who they are; the
  result says so.
- **A tool result is not masked.** It is your data, returned to you.
- **A model following instructions in content.** That is the client's
  concern. Content that makes the server itself break an invariant is a
  vulnerability.
- **Someone else's code.** A flaw in Google's APIs goes to Google, and
  one in a dependency to that project. We will help route it.

## Dependency vulnerabilities

`govulncheck` runs in `make check` and in CI on every pull request and
push to `main`. It fails when this server's code can reach a vulnerable
symbol.

A CVE in a dependency is a vulnerability here only when `govulncheck`
reports it reachable. It is then fixed in a release whose CHANGELOG
entry names the CVE or GHSA; otherwise the dependency is updated in the
normal course.

## What you are responsible for

- **Your own OAuth client.** Nothing is shipped in the binary, so
  revoking yours affects no one else.
  [`troubleshooting.md`](troubleshooting.md#tasks) says how to rotate
  the token, revoke access and handle a suspected exposure.
- **The switches you turn on.** `GDRIVE_ENABLE_DESTRUCTIVE` registers
  deletes that skip the trash and shares that reach past the
  organization. `GDRIVE_LOCAL_DIR` is a directory a model can write
  into and upload from, so point it at nothing you would not share.
  Leave the first off for a client that runs with nobody watching, and
  use read-only mode, logged in again, where reading is enough.
- **Your client's approvals.** Give the sharing and deleting tools an
  `ask` rule, as the README shows.
- **Where the token lives.** Keep a keyring available, and
  `GDRIVE_REFRESH_TOKEN` out of shared shells and logs.
- **What you paste.** `status` and a debug log are built to be safe.
  `doctor` prints the account in full. A token, `token.json`, a client
  JSON and a tool result are not safe.
