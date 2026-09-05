# google-drive-mcp

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

**Status: v0.1.0, phase 1 of the plan in
[docs/architecture.md](docs/architecture.md).** The sixteen tools below
work, and every one of them is verified against a real Google Workspace
account as well as against the in-memory Drive the tests use. Sharing
and history arrive in v0.2.0, collaboration in v0.3.0.

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
| `copy_file` | Copy a file, optionally asking Google to import it as a Doc, which reads the text out of a PDF or a scan |
| `create_shortcut` | A pointer to one item from another folder |
| `trash_file` | Move an item to the trash, which is reversible |
| `restore_file` | Take an item out of the trash, and say where it went |

Four things it does differently from the alternatives:

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

Or download a release archive from the releases page and put the binary
on your `PATH`. Every archive carries the binary, `LICENSE` and this
README; `checksums.txt` is signed with a keyless Sigstore certificate and
each archive has a build provenance attestation you can check with
`gh attestation verify`.

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
| `GDRIVE_ENABLE_DESTRUCTIVE` | `false` | Register permanent delete, empty trash and the other tools with no way back. |

## What it will not do

- Edit the content of a Google Doc, Sheet or Slides deck. It reads them
  through Google's export and says so.
- Widen access without being asked. Sharing tools check
  `capabilities.canShare` first, show who can see a file before and
  after, need `allow_anyone: true` for a public link, and send no
  notification mail unless you ask for it. What may actually be shared is
  decided by your organisation's own policy, which Google enforces on
  every call.
- Destroy anything without a way back, by default. Trash and restore are
  the default surface; permanent deletion is gated behind
  `GDRIVE_ENABLE_DESTRUCTIVE=true`. There is no bulk delete and no bulk
  share: one item per call, so every removal is a visible approval.
- Talk to anything but Google. Every URL is checked against an allowlist
  of Google's own hosts before credentials are attached. No telemetry, no
  update checks.
- Write anything private into its logs. They carry truncated ids, counts,
  byte counts and latencies; never file names, paths, addresses, queries
  or content.

## Documentation

- [docs/architecture.md](docs/architecture.md) — the design, the evidence
  behind it, and the phase plan.
- [docs/configuration.md](docs/configuration.md) — every setting.
- [docs/security.md](docs/security.md) — what it touches and what limits it.
- [docs/development.md](docs/development.md) — building, testing, releasing.
- [CONTRIBUTING.md](CONTRIBUTING.md) — ground rules and the pull request flow.

## Licence

Apache-2.0.
