# google-drive-mcp

[![CI](https://github.com/mmedum/google-drive-mcp/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/mmedum/google-drive-mcp/actions/workflows/ci.yml)
[![Latest release](https://img.shields.io/github/v/release/mmedum/google-drive-mcp?sort=semver)](https://github.com/mmedum/google-drive-mcp/releases/latest)
[![Go Reference](https://pkg.go.dev/badge/github.com/mmedum/google-drive-mcp.svg)](https://pkg.go.dev/github.com/mmedum/google-drive-mcp)
[![License: Apache 2.0](https://img.shields.io/github/license/mmedum/google-drive-mcp)](./LICENSE)

A [Model Context Protocol](https://modelcontextprotocol.io) server for
Google Drive, written in Go: find files and know where they live and who
can see them, organise folders, move and copy, get content in and out,
share without widening access by accident, follow what changed, and
manage shared drives. It stops at the file boundary; what happens inside
a Google Doc, Sheet or Slides deck is out of scope and belongs to servers
built on the Docs, Sheets and Slides APIs.

Single binary, stdio, one Google account per profile. You run it against
a Google Cloud project you own, so nothing about this repository is tied
to any particular organisation or account.

**Status: v1.0.0, phase 5 of the plan in
[docs/architecture.md](docs/architecture.md).** The tools below work, and
every one of them is verified against a real Google Workspace account as
well as against the in-memory Drive the tests use — the five that remove
something for good included, which run inside a shared drive the live
driver creates and destroys again. Four paths are not: handing over
ownership of a file, opening a link-shared file that needs its resource
key, a share an organisation's policy refuses, and applying a label. Each
needs a second Google account or an administrator to exercise, and
[docs/architecture.md](docs/architecture.md) §17a says what stands in for
each of them.

## What it does today

| Tool | What it does |
|---|---|
| `get_account` | Who is signed in, storage used, whether this account has shared drives, and which of this server's tools are registered |
| `get_file` | Everything about one file: kind, location, link, size, owner, who can see it, and what you may do with it |
| `list_folder` | One page of a folder's contents, or a budgeted tree of everything below it |
| `search_files` | Find files across My Drive, files shared with you, and every shared drive |
| `read_file` | The text of a file: a Doc as markdown, a Sheet as csv, a log or source file as itself, windowed with a continuation |
| `download_file` | Write a file to the local directory, converting a Google document on the way out, checksum-verified |
| `create_file` | A new empty Google file, or one written from text you have here, with optional conversion |
| `upload_file` | Send a local file, in one request or in chunks that survive a dropped connection |
| `update_content` | Replace what is inside a file, keeping its id, its place and everything that points at it |
| `create_folder` | A new folder, refusing a duplicate name unless you allow it |
| `update_file` | Rename, describe, star, colour, set properties, or turn off copying and re-sharing |
| `move_file` | Move an item to another folder or shared drive, with a dry run |
| `copy_file` | Copy a file, optionally asking Google to import it as a Doc, which reads the text out of a PDF or a scan; with `recursive`, a whole folder |
| `create_shortcut` | A pointer to one item from another folder |
| `trash_file` | Move an item to the trash, which is reversible |
| `restore_file` | Take an item out of the trash, and say where it went |
| `list_permissions` | Who can see an item, with the role, the expiry, and where each grant came from |
| `share_file` | Grant or change access, with who can see it before and after |
| `unshare_file` | Take access away, or kill the link that let anybody open it |
| `list_drives` | The shared drives this account can see, with what it may do in each |
| `manage_drive` | Create, rename, hide, unhide or restrict a shared drive |
| `list_revisions` | A file's version history, with Google's own caveat about what it leaves out |
| `manage_revision` | Pin a version so Drive keeps it, or unpin it again |
| `list_changes` | What has changed since a point in time, with the token for next time |
| `list_comments` | The threads on a file, with their replies and whether each is still open |
| `add_comment` | Start a thread on any file, Google document or not |
| `reply_comment` | Answer a thread, resolve it, reopen it, or edit wording already in it |
| `list_access_requests` | Who has asked to be let into a file, and what they asked for |
| `resolve_access_request` | Accept or deny one, with who could see the file before and after |
| `list_approvals` | The reviews on a file: who asked, who has to answer, and whether it is waiting on you |
| `manage_approval` | Ask people to review a file, answer one, withdraw it, comment on it, or change who is asked |

Three more are registered only when the deployer turns their feature on,
because each needs a scope the consent screen would otherwise not carry:
`list_labels` and `manage_labels` with `GDRIVE_LABELS=true`, and
`list_activity` with `GDRIVE_ACTIVITY=true`.

| Tool | What it does |
|---|---|
| `list_labels` | The Workspace labels this account can use, with each field and the values it takes |
| `manage_labels` | Put a label on a file, set or clear one of its fields, or take it off |
| `list_activity` | What happened to a file, or to everything in a folder, and when |

Five more are registered only with `GDRIVE_ENABLE_DESTRUCTIVE=true`, and
each of those also needs `confirm: true` on the call itself:
`delete_file`, `empty_trash`, `delete_drive`, `delete_revision` and
`delete_comment`.

Three of the reads are also **resources**, for a client that attaches
them rather than calling a tool: `gdrive://<id>` is the file's text,
`gdrive://<id>/meta` is the description, and `gdrive://<id>/children` is
a folder's first page. A reference with a slash in it — a path, a URL —
has to be percent-encoded there, so pass an id.

Five things it does differently from the alternatives:

- **Shared drives work from the first call.** Every request carries
  `supportsAllDrives`, listings include items from all drives, and an
  incomplete search says so instead of quietly returning less.
- **Ids are the contract.** A name or path matching more than one item
  comes back as `[ambiguous]` with every candidate listed. Nothing takes
  the first match.
- **Location is always shown.** Names are not unique in Drive, so every
  result says which folder, and which drive, a file sits in.
- **Files go one place only.** Downloads land in `GDRIVE_LOCAL_DIR` and
  uploads are read from it; unset, there is no file transfer at all. A
  path outside it is refused, symlinks included.
- **Sharing shows its work.** Every grant or revocation reports who could
  see the file before and who can see it after. A public link needs
  `allow_anyone: true` and an ownership transfer needs
  `transfer_ownership: true`, on the call itself. No notification mail
  goes out unless you ask for it, which is the opposite of the API's own
  default.

## Install

```
go install github.com/mmedum/google-drive-mcp/cmd/google-drive-mcp@latest
```

That puts the binary in Go's bin directory, which is often not on your
`PATH`. If the next command says `command not found`, either use the full
path or add the directory once:

```
"$(go env GOPATH)/bin/google-drive-mcp" --version    # check it landed
export PATH="$(go env GOPATH)/bin:$PATH"             # or add it to your shell profile
```

Or take a signed archive from the
[latest release](https://github.com/mmedum/google-drive-mcp/releases/latest)
— Linux, macOS and Windows, on amd64 and arm64 — and put the binary on
your `PATH`. Every archive carries the binary, `LICENSE` and this README.
Nothing about a release has to be taken on trust:

```bash
# --ignore-missing, because checksums.txt covers every archive and you
# will have downloaded one of them.
sha256sum -c checksums.txt --ignore-missing

# checksums.txt is signed with a keyless Sigstore certificate tied to the
# release workflow's identity; the bundle carries the signature and the
# certificate together.
cosign verify-blob checksums.txt --bundle checksums.txt.bundle \
  --certificate-identity-regexp '^https://github.com/mmedum/google-drive-mcp' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com

# And each archive carries build provenance naming the workflow and tag
# that produced it.
gh attestation verify google-drive-mcp_*.tar.gz --repo mmedum/google-drive-mcp
```

Every archive also ships an SBOM, so you can see what is inside a binary
you did not build.

## Set up Google, once per person

You need your own Google Cloud project and your own OAuth client. This
takes about five minutes, and `google-drive-mcp doctor` tells you which
step you missed.

1. **Create a project** at [console.cloud.google.com](https://console.cloud.google.com/projectcreate).
2. **Enable the Google Drive API** for it, under *APIs & Services →
   Library*.
3. **Configure the OAuth consent screen.**
   - On a Google Workspace account, choose **Internal**. Refresh tokens
     do not expire.
   - On a personal account, choose **External**, leave it in **Testing**,
     and add your own address as a test user. Google expires refresh
     tokens for a Testing app after **7 days**, so you will re-run
     `login` about once a week.
4. **Add the scope** `https://www.googleapis.com/auth/drive`
   (or `.../auth/drive.readonly` if you will run with
   `GDRIVE_READ_ONLY=true`). It is a restricted scope, which is fine for
   an app only you use and never publish.
5. **Create credentials → OAuth client ID → Desktop app**, and download
   the JSON. A *Web application* client will not work: this server uses
   the loopback flow Google documents for desktop apps.
6. **Put the JSON where the server looks**, or point at it:

   ```
   mkdir -p ~/.config/google-drive-mcp
   cp ~/Downloads/client_secret_*.json ~/.config/google-drive-mcp/client_secret.json
   ```

7. **Log in and check:**

   ```
   google-drive-mcp login
   google-drive-mcp doctor
   ```

`login` opens a browser, receives the callback on `127.0.0.1` with PKCE,
and stores the refresh token in your OS keyring (Secret Service, Keychain
or Credential Manager). If no keyring is available it falls back to a
`0600` file and says so. `logout` revokes the token at Google and deletes
it locally.

### Logging in over SSH

The callback goes to the *remote* host's loopback address and your browser
is local, so the port has to be forwarded. It is drawn at random and
printed only once login is already waiting, so read it out of the printed
URL — it appears percent-encoded there, as `127.0.0.1%3A<port>` — and in a
second local terminal:

```bash
ssh -N -L <port>:127.0.0.1:<port> user@remote-host
```

Then open the URL in your local browser. If `ssh` says `bind: Address
already in use`, cancel the login with Ctrl-C and start it again to draw
a different port.

## Connect a client

**Claude Code**

```
claude mcp add google-drive -- google-drive-mcp
```

**Claude Desktop** (`claude_desktop_config.json`) and **Cursor**
(`.cursor/mcp.json`) take the same shape:

```json
{
  "mcpServers": {
    "google-drive": {
      "command": "google-drive-mcp",
      "env": {
        "GDRIVE_LOCAL_DIR": "/absolute/path/for/downloads"
      }
    }
  }
}
```

All three clients pass only `command`, `args` and `env`, which is why
every setting is an environment variable.

## Configuration

Every setting is a `GDRIVE_*` environment variable with a matching flag.
The full list, with defaults, is in
[docs/configuration.md](docs/configuration.md). The ones that change what
the server will do at all:

| Variable | Default | Effect |
|---|---|---|
| `GDRIVE_LOCAL_DIR` | unset | The one directory downloads are written to and uploads are read from. **Unset means no file transfer at all.** |
| `GDRIVE_READ_ONLY` | `false` | Register only read tools, and ask for read-only scopes at login. |
| `GDRIVE_SHARING` | `all` | `off` leaves the sharing tools unregistered. |
| `GDRIVE_ENABLE_DESTRUCTIVE` | `false` | Register permanent delete, empty trash and the other tools with no way back. Each still needs `confirm: true` per call. |

## What it will not do

- Edit the content of a Google Doc, Sheet or Slides deck. It reads them
  through Google's export and says so. A comment made here sits on the
  file rather than on a passage of the document: pinning one to a place
  in a Doc is a Docs API feature, and this server does not use that API.
- Widen access without being asked. Sharing tools check
  `capabilities.canShare` first, show who can see a file before and
  after, need `allow_anyone: true` for a public link, and send no
  notification mail unless you ask for it. What may actually be shared is
  decided by your organisation's own policy, which Google enforces on
  every call.
- Destroy anything without a way back, by default. Trash and restore are
  the default surface; permanent deletion is gated behind
  `GDRIVE_ENABLE_DESTRUCTIVE=true` **and** needs `confirm: true` on the
  call, because a registered tool is one a model will reach for
  eventually. There is no bulk delete and no bulk share: one item per
  call, so every removal is a visible approval. Deleting the top of a
  drive is refused outright.
- Talk to anything but Google. Every URL is checked against an allowlist
  of Google's own hosts before credentials are attached. No telemetry, no
  update checks.
- Write anything private into its logs. They carry truncated ids, counts,
  byte counts and latencies; never file names, paths, addresses, queries
  or content.

## How it works

```
MCP client ──stdio──► google-drive-mcp
                       ├── tools      one handler per tool; shapes the reply
                       ├── service    the rules: addressing, sharing policy, confinement
                       ├── model      the server's view of a file, and what you may do to it
                       ├── render     the text a result is made of
                       ├── gapi       raw REST client for Drive, Drive Labels and Drive Activity
                       ├── gdrive     hand-written wire types, no generated client
                       ├── ref        ids, URLs and paths; resolves nothing over the network
                       └── auth       refresh token → access token
```

[docs/architecture.md](docs/architecture.md) has the design, the package
layout, the phase plan, and an evidence log recording which conventions
were checked against Google's own reference and which turned out to be
wrong. [docs/security.md](docs/security.md) is what it touches and what
limits it.

## Development

```bash
make build     # the binary
make test      # race detector, per-package coverage floor
make check     # everything CI runs
make live      # drive the binary against the signed-in account, redacted
```

`make check` is the definition of done: gofmt, `go vet`, golangci-lint,
race tests with a per-package coverage floor, `govulncheck`, a licence
check, a leak check over the working tree, pinned-version and error-class
gates, an API-coverage gate holding every method of all three APIs to a
recorded decision, a stdio smoke test, a schema diff against the released
tool surface, a staleness gate that fails when this README, the docs or
the changelog drift from the code, and a parity gate asserting that
`make check` and CI run the same set of gates.

Conventions are in [CONTRIBUTING.md](CONTRIBUTING.md); building, testing
and releasing are in [docs/development.md](docs/development.md).

## Versioning

Tool names, their arguments and the shape of their output are stable
within a major version. A change that needs you to do something — a new
scope, another `login`, a different command in your client config — is
marked **Breaking:** in [CHANGELOG.md](CHANGELOG.md), which is what the
release notes are made from. A tool moving from the default surface to
behind a feature flag, or the other way, counts as breaking.

## Security

[SECURITY.md](SECURITY.md) says how to report a vulnerability. Please do
not open a public issue for one.

## Code of conduct

[CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) — Contributor Covenant 3.0.

## Documentation

- [docs/architecture.md](docs/architecture.md) — the design, the evidence
  behind it, and the phase plan.
- [docs/configuration.md](docs/configuration.md) — every setting.
- [docs/security.md](docs/security.md) — what it touches and what limits it.
- [docs/development.md](docs/development.md) — building, testing, releasing.
- [CONTRIBUTING.md](CONTRIBUTING.md) — ground rules and the pull request flow.
- [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) — Contributor Covenant 3.0.
- [SECURITY.md](SECURITY.md) — reporting a vulnerability.

## Licence

Apache-2.0.
