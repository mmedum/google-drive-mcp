# google-drive-mcp

[![CI](https://github.com/mmedum/google-drive-mcp/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/mmedum/google-drive-mcp/actions/workflows/ci.yml)
[![Latest release](https://img.shields.io/github/v/release/mmedum/google-drive-mcp?sort=semver)](https://github.com/mmedum/google-drive-mcp/releases/latest)
[![Go Reference](https://pkg.go.dev/badge/github.com/mmedum/google-drive-mcp.svg)](https://pkg.go.dev/github.com/mmedum/google-drive-mcp)
[![License: Apache 2.0](https://img.shields.io/github/license/mmedum/google-drive-mcp)](./LICENSE)

Google Drive as MCP tools. Find, organize, share and move files, and follow what changed.

A single Go binary that speaks [Model Context Protocol](https://modelcontextprotocol.io)
over stdio. It runs as a subprocess of your client, on your own machine,
one Google account per profile: find files and know where they live and
who can see them, organize folders, move and copy, get content in and
out, share without widening access by accident, follow what changed, and
manage shared drives.

## Why google-drive-mcp

Drive is where the file lives, and most of what goes wrong with a file is
about the boundary rather than the contents: where it sits, who can
already see it, what widening a share would actually widen, and what
changed since you last looked. This server works at that boundary and
stops there — what is inside a Doc, Sheet or Slides deck belongs to
servers built on those APIs, and this one says so rather than guessing
through an export.

It runs as you, against a Google Cloud project you own, so nothing here
is tied to any particular organization or account. Every tool below is
verified against a real Google Workspace account as well as against the
in-memory Drive the tests use — the five that remove something for good
included, which run inside a shared drive the live driver creates and
destroys again. Four paths are not, because each needs a second account
or an administrator to exercise: handing over ownership, opening a
link-shared file that needs its resource key, a share an organization's
policy refuses, and applying a label. §17a of
[docs/architecture.md](docs/architecture.md) says what stands in for each.

## Install

```
go install github.com/mmedum/google-drive-mcp/v3/cmd/google-drive-mcp@latest
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

### Claude Desktop

Every release also carries a `.mcpb` bundle. Open it and Claude Desktop
installs the server and asks for the settings a client's configuration
would carry: your OAuth client JSON, the profile, read-only, the
destructive switch, the sharing tools, the download and upload folder,
the largest download, Workspace labels and activity — no config file to
edit. Deleting for good and sharing past your organization
(`enable_destructive`), labels (`labels`) and activity (`activity`) stay
off unless you turn them on, and read-only leaves the destructive switch
with nothing to register. This server asks nobody before a call. Claude
Desktop approves each tool, with Allow once or Always allow, and a tool
you always allow then runs unasked, so leave a switch off unless you
need its tools.

It covers macOS, Windows and Linux on both architectures each: macOS
through a universal binary, Windows through amd64, and Linux through a
small launcher that picks the right binary at start, because a bundle
manifest names a command per platform and has no key for the
architecture. Its SHA-256 is in the same signed `checksums.txt`.

The bundle does not log you in. Install the binary as well, run
`google-drive-mcp login` once, and the bundle picks up the same
credentials. Labels and activity also need a login that asks for their
scopes, such as `google-drive-mcp login --labels=true`. Claude Code does
not install `.mcpb` files, so it uses the command below.

## Set up Google

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
4. **On an External app, add the scopes this server asks for.** Google
   asks for that list only from apps used by people outside your
   Workspace organization, so an **Internal** app can skip this step;
   listing them anyway does no harm. Which ones depends on how you
   run it, and `login` requests exactly the set your configuration needs
   — so add the ones you will use, and re-run `login` if you turn a
   feature on later. All of them are restricted scopes, which is fine for
   an app only you use and never publish.

   | Scope | Requested when |
   |---|---|
   | `https://www.googleapis.com/auth/drive` | always, unless read-only |
   | `https://www.googleapis.com/auth/drive.readonly` | instead of it, with `GDRIVE_READ_ONLY=true` |
   | `https://www.googleapis.com/auth/drive.labels` | with `GDRIVE_LABELS=true` |
   | `https://www.googleapis.com/auth/drive.labels.readonly` | with `GDRIVE_LABELS=true` and read-only |
   | `https://www.googleapis.com/auth/drive.activity.readonly` | with `GDRIVE_ACTIVITY=true` |

   `google-drive-mcp doctor` names any scope your token is missing, which
   is what to run if a call comes back refused after you change a flag.

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
| `GDRIVE_SHARING` | `all` | `off` leaves the sharing tools unregistered, keeps a shared drive's restrictions, a file's download and reshare switches and a folder's limited access from being loosened, and refuses a move that would let more people reach an item, or a copy that more people could reach than the original. |
| `GDRIVE_ENABLE_DESTRUCTIVE` | `false` | Register permanent delete, empty trash and the other tools with no way back, and the shares that reach past your organization: a link for anyone, a whole domain, someone outside it, a new owner, and lifting a shared drive's domain restriction. Each still needs `confirm: true` per call. |

`GDRIVE_REQUIRE_PROMPT` was removed in 3.0.0: the server no longer asks
you anything. Set to anything but `false`, the server refuses to start,
rather than quietly allow what you set it to stop.

### Who approves each call

Your MCP client approves each call; the server does not ask you
itself. claude.ai and Codex prompt for the tools marked destructive,
which is every write but the eight that only add something new:
`create_file`, `create_folder`, `create_shortcut`, `upload_file`,
`copy_file`, `add_comment`, `download_file` and `extract_text`. VS Code prompts for every tool not marked read-only,
which is every tool that writes, `download_file` included. In Claude
Code, `delete_file`, `empty_trash`, `delete_drive` and `share_by_link`
ask on every call whatever your rules say, and are refused where no
prompt can be drawn: none can be undone, and each reaches past one
item, skipping the trash, taking a whole trash or shared drive, or
opening a file to everyone with the link or a whole domain. For the
rest, list the calls you want to approve one by one under
`permissions.ask` in your settings, with the name you gave the server
in place of `google-drive`. For example, the writes that share, publish,
sign off or delete:

```json
{
  "permissions": {
    "ask": [
      "mcp__google-drive__share_file", "mcp__google-drive__share_outside_organization",
      "mcp__google-drive__share_by_link", "mcp__google-drive__transfer_ownership",
      "mcp__google-drive__resolve_access_request", "mcp__google-drive__lift_drive_restriction",
      "mcp__google-drive__approve_file",
      "mcp__google-drive__delete_file", "mcp__google-drive__empty_trash",
      "mcp__google-drive__delete_drive", "mcp__google-drive__delete_revision",
      "mcp__google-drive__delete_comment"
    ]
  }
}
```

What holds whatever the client does:

- **Wide shares are their own tools.** `share_file` grants named people
  and groups inside your organization. A link for anyone, a whole
  domain, someone outside the organization and a new owner each have
  their own tool, registered only with `GDRIVE_ENABLE_DESTRUCTIVE=true`.
  Outside means an address whose domain is not your account's; on a
  personal Google account, which has no organization, that is every
  address.
- **Nothing widens by omission.** A move, a copy, or a folder's limited
  access turned off that would let anyone with the link, a domain or
  someone outside your organization reach a file is refused, naming
  who. Accepting a request for access from outside your organization is
  refused too.
- **Deletes need the flag and `confirm: true`.** Trash and restore are
  the default way to remove something; `confirm: true` is the caller's
  witness that it meant this delete, not your approval.

## Tools

| Tool | What it does |
|---|---|
| `get_account` | Who is signed in, storage used, whether this account has shared drives, and which of this server's tools are registered |
| `get_file` | Everything about one file: kind, location, link, size, owner, who can see it, who cannot download it, and what you may do with it |
| `list_folder` | One page of a folder's contents, or a budgeted tree of everything below it |
| `search_files` | Find files across My Drive, files shared with you, and every shared drive, or under one folder at any depth |
| `read_file` | The text of a file: a Doc as markdown, a Sheet as csv, a Word, Excel or PowerPoint file (or its OpenDocument counterpart) read here as text, a log or source file as itself, windowed with a continuation |
| `extract_text` | The text in a PDF or an image, read by Google's OCR through a temporary Google Doc copy that is deleted again |
| `download_file` | Write a file to the local directory, converting a Google document on the way out, checksum-verified |
| `create_file` | A new empty Google file, or one written from text you have here, with optional conversion |
| `upload_file` | Send a local file, in one request or in chunks that survive a dropped connection |
| `update_content` | Replace what is inside a file, keeping its id, its place and everything that points at it, and refusing to lose the comments Google Docs keeps in a Markdown file |
| `create_folder` | A new folder, refusing a duplicate name unless you allow it |
| `update_file` | Rename, describe, star, color, set properties, restrict downloads and re-sharing, or give a folder limited access |
| `move_file` | Move an item, or up to 50, to another folder or shared drive, with who can reach each before and after, and a dry run |
| `copy_file` | Copy a file, optionally asking Google to import it as a Doc, which reads the text out of a PDF or a scan; with `recursive`, a whole folder; with who can reach the original and the copy |
| `create_shortcut` | A pointer to one item from another folder |
| `trash_file` | Move an item to the trash, which is reversible |
| `restore_file` | Take an item out of the trash, and say where it went |
| `list_permissions` | Who can see an item, with the role, the expiry, and where each grant came from |
| `share_file` | Grant or change access for people and groups inside your organization, with who can see it before and after |
| `unshare_file` | Take access away, or kill the link that let anybody open it |
| `list_drives` | The shared drives this account can see, with what it may do in each |
| `manage_drive` | Create, rename, hide, unhide or restrict a shared drive; never lift its domain restriction |
| `list_revisions` | A file's version history, with Google's own caveat about what it leaves out |
| `manage_revision` | Pin a version so Drive keeps it, or unpin the current one; a pin on an older version is for good |
| `list_changes` | What has changed since a point in time, with the token for next time |
| `list_comments` | The threads on a file, with their replies and whether each is still open |
| `add_comment` | Start a thread on any file, Google document or not |
| `reply_comment` | Answer a thread, resolve it, reopen it, or edit wording already in it |
| `list_access_requests` | Who has asked to be let into a file, and what they asked for |
| `resolve_access_request` | Accept a request from inside your organization, or deny any, with who could see the file before and after |
| `list_approvals` | The reviews on a file: who asked, who has to answer, and whether it is waiting on you |
| `manage_approval` | Ask people to review a file, decline one, withdraw it, comment on it, or change who is asked, and choose whether a content change resets it |
| `approve_file` | Approve a review you were asked for, which can lock the file for good |

Three more are registered only when the deployer turns their feature on,
because each needs another Google API and a scope login would otherwise not ask for:
`list_labels` and `manage_labels` with `GDRIVE_LABELS=true`, and
`list_activity` with `GDRIVE_ACTIVITY=true`.

| Tool | What it does |
|---|---|
| `list_labels` | The Workspace labels this account can use, with each field and the values it takes |
| `manage_labels` | Put a label on a file, set or clear one of its fields, or take it off |
| `list_activity` | What happened to a file, or to everything in a folder, and when |

Nine more are registered only with `GDRIVE_ENABLE_DESTRUCTIVE=true`, and
each of those also needs `confirm: true` on the call itself. Five remove
something for good: `delete_file`, `empty_trash`, `delete_drive`,
`delete_revision` and `delete_comment`. Four reach past your
organization: `share_by_link` opens a file to anyone with the link or a
whole domain, `share_outside_organization` grants someone outside it,
`transfer_ownership` hands a file to a new owner, and
`lift_drive_restriction` lets a shared drive's content be shared outside
the organization. These four also take the name of what they open up,
`file_name` or `drive_name`, so the call your client shows you for
approval says which file or drive it is; a name that is not the item's
is refused.

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
  see the file before and who can see it after. A public link, a share
  outside your organization and an ownership transfer are each their own
  tool, off unless the deployer turns them on. No notification mail goes
  out unless you ask for it, which is the opposite of the API's own
  default.

## Safety

- Edit the content of a Google Doc, Sheet or Slides deck. It reads them
  through Google's export and says so. A comment made here sits on the
  file rather than on a place in it: pinning one in a Doc, Sheet or
  Slides deck is a feature of that kind's own API, which this server
  does not use.
- Delete the comments Google Docs keeps in a Markdown file. Docs edits a
  `.md` stored in Drive in place and writes the comments made there into
  the file's own text, where Drive's comments do not list them.
  `update_content` refuses new content that would lose one unless
  `allow_comment_changes` is set.
- Widen access without being asked. Sharing tools check
  `capabilities.canShare` first and show who can see a file before and
  after. Without `GDRIVE_ENABLE_DESTRUCTIVE=true` nothing here can share
  past your organization, and nothing ever does so by a move or a copy.
  No notification mail goes out unless you ask for it. What may actually
  be shared is decided by your organization's own policy, which Google
  enforces on every call.
- Destroy anything without a way back, by default. Trash and restore are
  the default surface; permanent deletion is gated behind
  `GDRIVE_ENABLE_DESTRUCTIVE=true` **and** needs `confirm: true` on the
  call, because a registered tool is one a model will reach for
  eventually. There is no bulk delete and no bulk share: one item per
  call, so every removal is a visible approval. Deleting the top of a
  drive is refused outright. The one thing deleted for good without
  either is the temporary copy `extract_text` makes and removes within
  the same call.
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

## Getting help

If something does not work, run `google-drive-mcp doctor`. It checks the
credentials, the granted scopes and what Google actually answers, and
names what is missing — most first-run trouble is an API that was never
enabled or a consent screen without you on it.

`google-drive-mcp status` shows the same setup without contacting Google,
and `status --json` prints it as one JSON object for a script that has to
decide whether this server is authorized before starting it —
`credentials.resolved` is the field to branch on. The shape is in
[docs/configuration.md](docs/configuration.md#reading-the-setup-from-a-script).

If that does not explain it,
[open an issue](https://github.com/mmedum/google-drive-mcp/issues). Never
paste a file id or URL, file contents, a `client_secret.json` or a token
into one; describe the shape instead. Security problems go through
[SECURITY.md](SECURITY.md), privately.

## Versioning

Tool names, their arguments and the shape of their output are stable
within a major version. A change that needs you to do something — a new
scope, another `login`, a different command in your client config — is
marked **Breaking:** in [CHANGELOG.md](CHANGELOG.md), which is what the
release notes are made from. A tool moving from the default surface to
behind a feature flag, or the other way, counts as breaking.

## Development

```bash
make build     # the binary
make test      # race detector, per-package coverage floor
make check     # everything CI runs
make live      # drive the binary against the signed-in account, redacted
```

`make live` changes nothing unless asked: `-write` and the flags in
[docs/development.md](docs/development.md) add writes. `-transfer`
hands a scratch file to another account for good, which cannot be undone
and makes Google email that account.

`make check` is the definition of done: gofmt, `go vet`, golangci-lint,
race tests with a per-package coverage floor, `govulncheck`, a license
check, a leak check over the working tree, pinned-version and error-class
gates, an API-coverage gate holding every method of all three APIs to a
recorded decision, a bundle-manifest gate holding the committed manifest
to the files the packer stages, a registry gate holding the published
entry to the rules the MCP registry enforces in code, a transcript gate refusing any way for a
program that drives a real account to reach a terminal except through the
redactor, a live-coverage gate holding every tool option to a decision
about whether the live driver drives it, an outcome gate refusing a
result sentence written from the request rather than from the response, a
stdio smoke test, a schema diff against the released tool surface, a
staleness gate that fails when this README, the docs or the changelog
drift from the code, and a parity gate asserting that `make check` and CI
run the same set of gates.

Conventions are in [CONTRIBUTING.md](CONTRIBUTING.md); building, testing
and releasing are in [docs/development.md](docs/development.md).

## Documentation

- [docs/architecture.md](docs/architecture.md) — the design, the evidence
  behind it, and the phase plan.
- [docs/configuration.md](docs/configuration.md) — every setting.
- [docs/security.md](docs/security.md) — what it touches and what limits it.
- [docs/development.md](docs/development.md) — building, testing, releasing.

## Contributing

Questions and bugs go in
[issues](https://github.com/mmedum/google-drive-mcp/issues); pull
requests are welcome. [CONTRIBUTING.md](CONTRIBUTING.md) covers the
ground rules and the pull request flow, and `make check` is what has to
pass.

## Security

[SECURITY.md](SECURITY.md) says how to report a vulnerability. Please do
not open a public issue for one.

## Code of conduct

[CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) — Contributor Covenant 3.0.

## License

Apache-2.0 — see [LICENSE](LICENSE).
