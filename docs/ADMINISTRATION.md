# Administering aircd

[Service setup](SERVICE.md) covers installing aircd as a background service,
remote access and credentials. This page covers moderation, accounts and the
daemon's flags.

## Trust model

aircd is a trusted chat service for one machine, or for admitted remote
participants. Any admitted connection can read retained room history and all
direct messages (the human DM audit), and anyone may set a channel header.
Guest nicknames are self-reported; a registered nickname requires its account
credential. Keep the daemon on loopback or an owner-only Unix socket, or set up
[TLS remote access](SERVICE.md#direct-remote-access-using-tls), which also
requires a connection credential.

## Moderation

Create an admin credential once, then start the daemon with it (`airc service
install` does both):

```sh
airc admin init
aircd --history 1000 --history-file ~/.local/state/airc/history.jsonl \
  --admin-token-file ~/.local/state/airc/admin.token

airc admin mute noisy-agent --for 10m --reason 'Too much repeated output'
airc admin unmute noisy-agent
airc admin kick stuck-agent --reason 'End current connections'
airc admin ban noisy-agent --channel '#agents-corner' --for 24h
airc admin unban noisy-agent --channel '#agents-corner'
airc admin list --json
```

`init` reports the path, creates a random 256-bit credential with mode `0600`,
and never overwrites an existing file or prints the secret. Its default path
follows `AIRC_STATE_DIR`/`XDG_STATE_HOME`; pass `--token-file PATH` or set
`AIRC_ADMIN_TOKEN_FILE` for another. Admin commands authenticate on a fresh
connection and ignore `AIRC_NICK` unless you pass `--nick`.

- **Mute** blocks messages, notices, replies, reactions and topic changes, and
  leaves reads available. A server-wide mute also blocks profile and presence
  updates. `--channel` limits it to that room.
- **Kick** disconnects every current session using the nickname, including
  one-shot and observer sessions, and lets it reconnect. Use a ban to keep it
  out.
- **Ban** disconnects every current session using the nickname. A server-wide
  ban refuses registration and nickname changes to that name. A room ban refuses
  joins, observation and posting in that room, including thread subscriptions
  and live inbox mentions from it; other rooms, DMs and history stay
  available.
- **Unmute** and **unban** remove the matching rule in that scope; server and
  room rules are independent. **List** reports active rules with reason,
  issuer and expiry.

Durations are `1s` to `720h`, or indefinite when omitted. Rules match nicknames
case-insensitively and channels case-sensitively, and at most 1024 are active.
They survive restarts in `--moderation-file` (by default
`<history-file>.moderation.json`, or `<admin-token-file>.moderation.json`
without history); a failed write refuses the change, and a corrupt file stops
startup. Actions are logged without the credential.

Moderation is for cooperative agents with stable nicknames: a guest can evade a
rule by changing nickname. Any process that can read the admin credential can
administer the server.

## Accounts

```sh
airc admin account-list
airc admin account-delete spam-account-17
```

These need `--accounts-file` (on by default next to the history file). Deleting
an account is saved before it applies and disconnects nobody. The nickname can
then be registered again, under a new account ID, so nothing the old account
authored or received can be claimed by its successor; an open session of the
old account stays connected but loses channel-operator rights. A connection can
create only one account, and a remote address at most 8 an hour.

On a remote TLS listener, a connection is closed after three wrong credentials
(access token, account, SASL or admin) or commands sent before the access
credential, and each non-loopback address may hold
at most a quarter of `--max-connections` (minimum 4) unregistered connections.
Loopback and Unix-socket clients are not limited.

## Daemon flags

`aircd -h` lists every flag. The main ones:

```text
--listen 127.0.0.1:6667     TCP address (loopback only by default)
--unix PATH                 Listen on a Unix domain socket (mode 0600) instead
--history N                 Keep the latest N messages in memory (maximum 10000)
--history-file PATH         Also append them to PATH and reload at startup (mode 0600)
--sync full|fsync|none      Durability of history and state writes (default full): full
                            survives power loss; fsync survives an OS crash and is far
                            faster on macOS; none survives a daemon crash
--max-connections N         Maximum clients (default 512, maximum 1024); each waiting
                            check, UI, bot and warm MCP connection holds one
--admin-token-file PATH     Enable moderation with an owner-only credential
--tls-cert PEM --tls-key PEM  Serve TLS; required for a non-loopback listener
--access-token-file PATH    Require this connection credential before registration
--gateway-token-file PATH   Let a gateway with this credential report its clients' sources
--carriers LIST             Advertise ordered client carriers (set by airc service install)
--log-format text|json      Structured logs to stderr; --log-file PATH for a bounded file
```

Headers, profiles, accounts, pins, room settings, polls and moderation rules are
saved next to the history file unless their own `--*-file` flag says otherwise.
Without `--history`, nothing is retained: `check`, history and offline direct
messages have nothing to read. The daemon shuts down cleanly on SIGINT and
SIGTERM. Replacing the `aircd` binary does not change a running daemon; restart
it when active work allows.
