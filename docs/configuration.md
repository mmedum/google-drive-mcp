# Configuration

Every setting is an environment variable named `GDRIVE_*`, and every one
of them also has a command-line flag of the same name in lower case with
dashes. A flag passed explicitly wins over the environment; the
environment wins over the built-in default. Configuration is
environment-first because Claude Code, Claude Desktop and Cursor all pass
only `command`, `args` and `env` to a stdio server.

Everything is validated once at start. A misconfigured server fails
before it announces itself, with every problem reported at once rather
than one per run.

## Settings

| Variable | Flag | Default | Meaning |
|---|---|---|---|
| `GDRIVE_PROFILE` | `--profile` | `default` | Named profile. Each profile keeps its own client secret, refresh token and account, so one machine can hold several Google accounts. Lower case, digits, `-` and `_`. |
| `GDRIVE_CLIENT_SECRET` | `--client-secret` | the profile's `client_secret.json` | Path to the Desktop-app OAuth client JSON downloaded from the Google Cloud console. |
| `GDRIVE_LOG_LEVEL` | `--log-level` | `info` | `debug`, `info`, `warn` or `error`. Logs go to stderr; stdout carries only JSON-RPC. |
| `GDRIVE_LOG_FORMAT` | `--log-format` | `text` | `text` or `json`. |
| `GDRIVE_READ_ONLY` | `--read-only` | `false` | Register only the read tools, and ask for read-only scopes at login. |
| `GDRIVE_ENABLE_DESTRUCTIVE` | `--enable-destructive` | `false` | Register the tools that destroy without a way back: permanent delete, empty trash, delete revision, delete shared drive, delete comment. Without it, trash and restore are the only removal. |
| `GDRIVE_SHARING` | `--sharing` | `all` | `all` registers the sharing tools — `share_file`, `unshare_file` and `resolve_access_request`, which grants a permission when it accepts one; `off` leaves them unregistered while `list_permissions` and `list_access_requests` still work, since seeing who can reach a file is not widening it. An `anyone`-with-the-link grant needs `allow_anyone: true` on the individual call in either case. |
| `GDRIVE_LOCAL_DIR` | `--local-dir` | unset | The one directory downloads are written to and uploads are read from, as an absolute path. **Unset means no file transfer at all**; inline text still works both ways. |
| `GDRIVE_MAX_DOWNLOAD` | `--max-download` | `1GiB` | Largest single download. Accepts `1GiB`, `500MB`, `2G` or a plain byte count; IEC units are powers of 1024 and decimal units powers of 1000. |
| `GDRIVE_HTTP_TIMEOUT` | `--http-timeout` | `60s` | Deadline for one attempt at a Google API call, and for one chunk of a transfer. Between `1s` and `10m`. |
| `GDRIVE_LABELS` | `--labels` | `false` | Enable Google Workspace labels. Adds the Drive Labels API scopes at login, so turning it on means logging in again. |

## Settings with no flag

These two are read from the environment only, because they are consulted
before the flags are parsed.

| Variable | Default | Meaning |
|---|---|---|
| `GDRIVE_CONFIG_DIR` | `os.UserConfigDir()/google-drive-mcp` | Where profiles, the client secret and the fallback token file live. |
| `GDRIVE_REFRESH_TOKEN` | unset | A refresh token supplied directly, for CI and automation. It overrides both the keyring and the token file. `logout` cannot revoke or remove it. |

## Where things are stored

```
$GDRIVE_CONFIG_DIR/                     (default: ~/.config/google-drive-mcp)
  client_secret.json                    the OAuth client you downloaded
  config.json                           account, token location, scopes at login (0600)
  token.json                            refresh token, only if no keyring was available (0600)
  profiles/<name>/                      the same three files, per non-default profile
```

The refresh token goes into the OS keyring first: Secret Service on
Linux, Keychain on macOS, Credential Manager on Windows. If the keyring
is unavailable — a headless machine with no session bus, say — it falls
back to the `0600` file and warns on stderr every time it uses it.

## File transfer

`GDRIVE_LOCAL_DIR` is the only place this server reads local files from
and writes them to. Unset, `download_file` and `upload_file` are still
registered — a missing tool tells a model nothing — and both refuse with
a message naming this setting. `get_account` says which of the two states
you are in.

- **Uploads** take a bare name in that directory or an absolute path
  inside it. The path is resolved through its symlinks before it is
  checked, so a link inside the directory pointing at something outside
  is refused like any other outside path.
- **Downloads** land on `<name>-<six characters of the id>.<extension>`,
  so two files of one name sit beside each other rather than on each
  other. Nothing is ever overwritten: a name already taken gains a
  counter. A blob is checked against Drive's own md5 and the result says
  whether it matched; an export has no checksum to compare against, and
  the result says that instead.
- `GDRIVE_MAX_DOWNLOAD` caps one download. It applies to a file's own
  bytes; an export has no size until it arrives, and Google caps exports
  at 10 MB of its own accord.

Inline text needs none of this: `read_file` returns a file's text and
`create_file` and `update_content` take text directly, so a server with
no local directory can still read and write file contents.

## Scopes

| Configuration | Scopes requested at login |
|---|---|
| default | `https://www.googleapis.com/auth/drive` |
| `GDRIVE_READ_ONLY=true` | `https://www.googleapis.com/auth/drive.readonly` |
| `GDRIVE_LABELS=true` | the above, plus `.../auth/drive.labels` (or `.../auth/drive.labels.readonly` in read-only mode) |

`drive` and `drive.readonly` are *restricted* scopes. That is fine for an
app you own and never publish; it is why the setup asks you to keep the
consent screen Internal, or External and in Testing.

`drive.file` is deliberately never requested. It reaches only files the
app itself created or the person opened with it through Google's Picker,
which a stdio server has no way to show.

## Changing a setting that affects scopes

`GDRIVE_READ_ONLY` and `GDRIVE_LABELS` change which scopes the login asks
for. Changing either means running `google-drive-mcp login` again;
`doctor` compares the scopes granted with the ones the current
configuration wants and names any that are missing.
