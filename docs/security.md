# Security

This server acts as one signed-in person, on their own Google account,
from their own machine. It is worth being precise about what that lets it
touch and what stops it going further.

## What it talks to

Only Google. Before any credential is attached, the request URL is
checked against an allowlist: `www.googleapis.com`, `*.googleapis.com`,
`*.googleusercontent.com` (the download hosts Drive hands back),
`oauth2.googleapis.com`, `accounts.google.com` and `drive.google.com`,
over HTTPS. Anything else is refused before the request is built.

There is no telemetry and no update check.

## Credentials

- The OAuth client is one you created in a Cloud project you own, so its
  access can be revoked independently of anyone else's.
- `login` uses the loopback flow Google documents for desktop
  applications: a listener on `127.0.0.1` with a random port, PKCE, and a
  `state` value checked on the callback. The out-of-band flow Google
  blocked in 2023 is not used.
- The refresh token goes to the OS keyring. If none is available it goes
  to a `0600` file under your config directory, and every use of that
  file warns on stderr.
- `logout` revokes the token at Google and deletes it locally. A token
  supplied through `GDRIVE_REFRESH_TOKEN` is outside its reach, and it
  says so.
- The client secret file is read, never written and never logged.

## What is in the logs

Logs go to stderr, never stdout, because stdout carries the JSON-RPC
frames the client parses. They carry truncated file ids, counts, byte
counts, latencies and error classes. They do not carry file or folder
names, paths, email addresses, search queries, or any file content.
Query strings are stripped from logged URLs for that reason.

`status` and `doctor` print the signed-in account to the terminal, where
you asked for it; it never reaches the log.

## Limits on what a model can do through this server

| Risk | What limits it |
|---|---|
| Acting on the wrong file | Ids are the contract. A name or path matching more than one item is refused with the candidates listed, and every result shows the file's folder and drive. |
| Exposing a file to the world or the wrong domain | Your organisation's own sharing policy, enforced by Google on every call; a `capabilities.canShare` check before the server offers the action at all; `allow_anyone: true` required per call for a link grant; who can see the file shown before and after; `dry_run`; `GDRIVE_SHARING=off`. Publishing a revision to the web is not offered at all, because it is exposure with no audience control. |
| Unwanted email to people | Notification mail is off unless asked for, the opposite of the API's default. Where Google forces it on — an ownership transfer — the result says so. |
| Mass deletion | Trash and restore are the default surface and are reversible for 30 days. Permanent delete, empty trash, delete revision and delete shared drive exist only with `GDRIVE_ENABLE_DESTRUCTIVE=true`. There is no bulk delete and no bulk share: one item per call, so every removal is a separate approval in the client. |
| Copying private files onto disk | Files are written only under `GDRIVE_LOCAL_DIR`, which is unset by default and disables transfers entirely. Paths are cleaned and symlinks resolved, and a path that escapes the directory is refused. Downloads are capped by `GDRIVE_MAX_DOWNLOAD` and every file written is named in the result. |
| Uploading files the person did not mean to share | Uploads read only from `GDRIVE_LOCAL_DIR`. Inline content is whatever the model wrote, which the person saw it write. |
| Instructions hidden inside a file | Read tools are read-only and return content as data. The server never acts on what a file says; the client's per-call approval is what stands between a suggestion in a document and a write. |
| Runaway listings and quota exhaustion | One page per call, budgeted tree walks with depth and item limits that report where they stopped, per-process rate limiters, and backoff that honours Google's own retry reasons. |
| A stalled request hanging the server | Every attempt, token refresh and transfer chunk runs under a deadline, and response bodies are bounded. |
| Secrets reaching the repository | gitleaks runs in pre-commit and in CI, with rules for Google client ids, client secrets and refresh tokens on top of its defaults. Every fixture is synthetic. |

## Annotations are not a security boundary

The MCP specification says a client may not trust a tool's annotations.
So every gate here is in the server: a tool the configuration excludes is
never registered, rather than registered with a warning attached.

## Reporting a vulnerability

See [SECURITY.md](../SECURITY.md). Report privately through GitHub's
security advisory form, not as a public issue.
