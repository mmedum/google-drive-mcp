# Troubleshooting

What to do when something does not work, then the maintenance tasks.
Setting up in the first place is the README's
[Set up Google](../README.md#set-up-google). Start with these two
commands:

```bash
google-drive-mcp status
google-drive-mcp doctor
```

`status` prints the profile, the config directory, the account masked to
its domain, the token store, the scopes and the settings, without
contacting Google. `doctor` prints the same, then checks the local directory, the
refresh token, the granted scopes and the Drive API, and the Labels and
Activity APIs when their settings are on. `doctor <id, URL or path>`
also reads that file. A failed check prints `✘`, the check's name and
the reason.

Run both with the profile and environment your MCP client gives the
server, since each checks what it is given.

## `OAuth client JSON not found at …`

`login` stops with this before opening a browser.

**Cause:** no OAuth client JSON sits at the profile's default path, and
none was named.

**Fix (resolves it):** download the Desktop app client's JSON from the
Cloud console. Put it at the path printed, or pass
`--client-secret <path>`.

## `auth: client secret JSON is not a Desktop app client (expected an "installed" section)`

`login` stops with this. A Web application client adds
`this is a Web application client; create a Desktop app client instead`.

**Cause:** the file is not a Desktop app client. Sign-in returns to a
loopback port chosen at run time, which only a Desktop app client
accepts (D-59).

**Fix (resolves it):** create an **OAuth client ID** of type **Desktop
app** in the same project, download its JSON, and run
`google-drive-mcp login --client-secret <path>`.

## `login failed: auth: timed out waiting for the browser`

**Cause:** the browser never reached the callback. Over SSH, the
callback goes to the remote host's loopback address while the browser
runs on your machine. Otherwise Google showed an error page rather than
returning to the callback.

**Fix (resolves it):** over SSH, forward the port. It is drawn at random
and printed only once `login` is waiting, so read it from the printed
URL, where it appears as `127.0.0.1%3A<port>`. In a second local
terminal, run:

```bash
ssh -N -L <port>:127.0.0.1:<port> user@remote-host
```

Then open the URL in your local browser. If `ssh` says
`bind: Address already in use`, stop `login` with Ctrl-C and start it
again to draw another port. `login --no-browser` prints the URL without
trying to open one, and `--timeout` sets how long it waits.

For an error page, read it. An External app in **Testing** signs in
only the accounts listed under **Test users**.

## `login failed: auth: Google returned no refresh token; …`

**Cause:** Google answered the sign-in without a refresh token, and the
server cannot call Google later without one.

**Fix (resolves it):** remove the app at
[Google account permissions](https://myaccount.google.com/permissions),
then run `google-drive-mcp login` again.

## `` [auth] no Google account is signed in. Run `google-drive-mcp login`, then try again. ``

Every tool answers this, and the server keeps running (D-62). `doctor`
fails `credentials found`.

**Cause:** this profile has no stored token. Either nobody logged in
under it, or the client starts the server with another profile or
config directory than your shell uses.

**Fix (resolves it):** run `google-drive-mcp login` with the same
`GDRIVE_PROFILE` and `GDRIVE_CONFIG_DIR` as the client's entry.

## `[auth] Google refused this login: …`

Every tool fails with this, and `doctor` fails `refresh token exchange`.
Google's reason is usually `invalid_grant`.

**Cause:** Google refuses the refresh token. An **External** app left in
**Testing** gets refresh tokens that expire after 7 days, so a personal
account signs in again about once a week. The token also dies when the
app's access is removed from the Google account or the OAuth client is
deleted.

**Fix (resolves it):** run `google-drive-mcp login`. A Workspace account
with an **Internal** consent screen gets refresh tokens that do not
expire.

## `[forbidden] this login does not carry the scope that … needs`

`doctor` fails `granted scopes` with `missing …`, and `login` warns
`Google granted fewer scopes than requested`.

**Cause:** a scope was never granted. A box was left unticked at
sign-in, an External app's consent screen lacks the scope, or a setting
changed after the login: `GDRIVE_READ_ONLY`, `GDRIVE_LABELS` and
`GDRIVE_ACTIVITY` each change the set (D-56). A token never gains a
scope.

**Fix (resolves it):** on an External app, add the scope to the consent
screen, as the README's setup step lists. Then run `login` again under
the configuration the client uses, and tick every box.

## `[forbidden] … failed because the Drive Labels API refused the token`

The same refusal names the **Drive Activity API** for `list_activity`.
`doctor` fails `Drive Labels API (labels.list)` or
`Drive Activity API (activity.query)`.

**Cause:** the API is not enabled in the Cloud project, or its scope was
not granted. Both fail as one 403 that says only that the token had
insufficient scopes, so the refusal names both steps (D-58).

**Fix (resolves it):** enable the **Drive Labels API** or the **Drive
Activity API** under **APIs & Services → Library**. On an External app,
add its scope to the consent screen. Then run
`google-drive-mcp login --labels=true`, or `--activity=true`, with the
same setting in the client's entry.

## `doctor` fails `Drive API (about.get)`

**Cause:** the Google Drive API is not enabled in the project that owns
the OAuth client.

**Fix (resolves it):** enable the **Google Drive API** under **APIs &
Services → Library**, then run `doctor` again.

## `[unsupported] this server cannot read or write local files: it was started without GDRIVE_LOCAL_DIR. …`

`download_file` and `upload_file` answer this, and `doctor` prints
`no local directory set`.

**Cause:** `GDRIVE_LOCAL_DIR` is unset, which turns file transfer off
(D-23).

**Fix (resolves it):** set `GDRIVE_LOCAL_DIR` to an absolute path in the
client's entry for this server, and restart the client.

**Workaround:** `read_file` returns a file's text, and `create_file` and
`update_content` take text, with no local directory.

## `[invalid] the local directory this server was given does not exist or cannot be read: …`

`doctor` fails `local directory writable`.

**Cause:** `GDRIVE_LOCAL_DIR` names a path that is missing, unreadable
or not a directory.

**Fix (resolves it):** create the directory, or point the setting at
one that exists, and restart the client.

## `warning: keyring unavailable (…); refresh token saved in plaintext at … (mode 0600)`

Each later use warns `using plaintext token file …` while the keyring
stays out of reach.

**Cause:** the OS keyring could not be reached at login, such as on a
headless Linux machine with no Secret Service running (D-60).

**Fix (resolves it):** start a keyring, or log in from a desktop
session, and run `login` again. The token moves to the keyring and the
file is deleted.

**Impact:** the file is a live credential. Keep it out of backups that
leave the machine, and remove it with `logout` rather than by deleting
the file, so the token is revoked.

## `config: invalid: GDRIVE_REQUIRE_PROMPT was removed: …`

The server and its subcommands refuse to start.

**Cause:** `GDRIVE_REQUIRE_PROMPT` is set to anything but `false`. The server asks the person nothing, and the client approves
each call (D-44).

**Fix (resolves it):** unset it. For a client that runs with nobody
watching, leave `GDRIVE_ENABLE_DESTRUCTIVE` off, and list the writes you
want to approve under the client's ask rules, as the README shows.

## A tool you expect is missing

**Cause:** registration follows the settings the server started with,
not the call (D-48, D-57, D-58). The permanent deletes and the shares
past the organization register only with `GDRIVE_ENABLE_DESTRUCTIVE=true`,
and the shares also need `GDRIVE_SHARING=all`. The label tools need
`GDRIVE_LABELS=true`, `list_activity` needs `GDRIVE_ACTIVITY=true`, and
`GDRIVE_READ_ONLY=true` leaves the reads and `download_file`.
`get_account` lists the tools that registered.

**Fix (resolves it):** set the variable in the client's entry for this
server, not in your shell, and restart the client.

## `[blocked] your organization's sharing policy does not allow this. Google said: …`

**Cause:** the organization's sharing policy refuses the share, and
Google enforces it on every call (D-35).

**Fix:** none from this server. A Workspace administrator decides the
policy.

## `[rate_limited] Drive is rate-limiting this account; …`

**Cause:** the account spent its Drive quota for the moment, and the
server's retries were refused too (D-52). Another program on the same
account is the usual cause.

**Fix:** wait a minute and retry. A refusal that says the project's
**daily** quota is spent cannot succeed until the quota resets, so
waiting a minute does not help.

## `[ambiguous_outcome] Google did not confirm …, so it may or may not have happened. …`

**Cause:** Google failed or timed out on a write that cannot be repeated
safely, so the server cannot know whether it landed (D-28).

**Fix (resolves it):** read the item with `get_file`, or a share with
`list_permissions`, and see whether the write is there before sending it
again.

**Impact:** repeating a create or a share that landed can do it twice.

## The live run says `!! A SCRATCH SHARED DRIVE IS STILL THERE, named …`

**Cause:** `-destructive` deletes every file it made, then the shared
drive it made. Drive refuses to delete a drive that still holds anything
untrashed, or whose trash it does not yet see as empty, and a second
attempt after a pause failed too (D-71).

**Fix (resolves it):** in Drive, open the shared drive the line names.
Trash what is left inside it, empty its trash, and delete the drive.

**Impact:** until then the drive stays in the Workspace, visible to its
members.

## The live run says `!! A GRANT IS STILL THERE: …`

**Cause:** `-share` takes back its grant on a scratch file and reads the
permissions back, and the address was still there after a retry.

**Fix (resolves it):** remove the grant in Drive's share dialog, or with
`unshare_file`. The file's id is in the results above the line.

**Impact:** the second person keeps access to that file, and trashing
the scratch folder does not take it away.

## The live run says `!! A FILE IS NOT BACK IN THE SCRATCH FOLDER …`

**Cause:** `-drive` moves a file into the named shared drive and back,
and both moves back failed.

**Fix (resolves it):** find the file by the id in the results above the
line, and move or trash it.

**Impact:** the file stays in that shared drive, visible to its members.

## Tasks

### Rotating the token

```bash
google-drive-mcp login
```

The new token replaces the old one in the same store.

### Revoking access

```bash
google-drive-mcp logout
```

This revokes the token at Google and deletes the local copy. If Google
cannot be reached, `logout` warns and deletes the local copy anyway;
remove the app at
[Google account permissions](https://myaccount.google.com/permissions)
to finish. `logout` cannot revoke or remove `GDRIVE_REFRESH_TOKEN`, and
says so when it is set.

### Suspected exposure

If a refresh token may have leaked, such as in a copied home directory,
a shared machine or a `token.json` in a backup:

1. Run `google-drive-mcp logout` on any machine that still has it.
2. If you cannot, remove the app's access at
   [Google account permissions](https://myaccount.google.com/permissions).
3. Run `google-drive-mcp login` on the machines that should keep
   working.

To replace the OAuth client as well, create a new Desktop app client,
run `login --client-secret <new path>`, and delete the old client.

### Moving to another machine

Do not copy the token. Install the binary, copy the OAuth client JSON,
and run `login`. A keyring entry does not move, and a copied
`token.json` is a second live copy of a credential.

### Two accounts on one machine

Profiles keep them apart (D-61):

```bash
GDRIVE_PROFILE=work google-drive-mcp login --client-secret ./work_client.json
GDRIVE_PROFILE=personal google-drive-mcp login --client-secret ./personal_client.json
```

Each profile has its own client secret, token, account and granted
scopes. Point each client entry at its profile with the same variable.

### Reporting a problem

Open an [issue](https://github.com/mmedum/google-drive-mcp/issues)
with:

- the output of `google-drive-mcp status` and `google-drive-mcp --version`;
- the lines of `google-drive-mcp doctor` that fail;
- how you installed it and which client runs it;
- a debug log, from `GDRIVE_LOG_LEVEL=debug`, which never carries file
  names, paths, addresses, queries or content (D-06).

`doctor` prints the signed-in address in full, and with a reference the
file's name, so take those out before pasting. Never paste a file id or
URL, file contents, a client JSON, a token or a tool result; describe
the shape instead. A suspected vulnerability goes through
[`SECURITY.md`](../SECURITY.md), privately.
