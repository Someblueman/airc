# IRC bridge profile

The bridge connects to an owner-configured, TLS-verified IRC server as a
services account and joins fixed channels. A plain message from any channel
member the server vouches for is posted to the channel's AIRC target as that
member's author; a typed `AIRC-READ/1` request is answered privately with plain
JSON.

The only identity input is the server's IRCv3 `account` tag (set by SASL or a
services login). A nickname, hostmask, display text or client-only `+account`
tag is never an identity: a message without a server account tag is ignored.
The author is the [bridge author](#bridge-authors) for that account. Server
`msgid` and `time` tags are also required; the bridge's own account is
excluded so its echoes never become sends.

Server lines require CRLF. The tag section is bounded at 8,191 bytes including
`@` and its separating space; the remaining message is bounded at 512 bytes
including CRLF. The reader bounds storage even without a line terminator;
any framing error is terminal for its connection. No partial command escapes.
Commands have at most 15 parameters. Prefixes and whole trailing text are
preserved; no trimming or truncation is used to make a message fit.

Tag names remain opaque and case sensitive. Repeated names use the last value.
Empty and missing values are equivalent. Escapes follow IRCv3, including dropping
a trailing backslash and the backslash of an unknown escape. Invalid UTF-8 tag
values are refused by this profile. Unknown tag names are retained without
inventing authority; `+account` is distinct from the server `account` tag.

Client command encoding emits a complete untagged command within 512 bytes or
refuses it. It does not split oversized text into independently admitted events.
Empty trailing parameters remain distinct from absent trailing parameters.

The primary sources are [RFC 2812](https://www.rfc-editor.org/rfc/rfc2812),
[IRCv3 message tags](https://ircv3.net/specs/extensions/message-tags),
[account tags](https://ircv3.net/specs/extensions/account-tag),
[server time](https://ircv3.net/specs/extensions/server-time) and
[message IDs](https://ircv3.net/specs/extensions/message-ids). A message ID is
opaque and scoped to its IRC network; repeated deliveries retain that ID.

Provisioning is defined by `config.IRCBridge` and `Config.ValidateIRCBridge`,
registered as the optional `irc_bridges` JSON array. At most 32 bridges are
allowed; bridge IDs and literal dial endpoints must be unique. Each has an
explicit `enabled` flag, a stable ID, a numeric non-wildcard dial endpoint with
nonzero port, a verified TLS server name, an optional absolute CA path, the
bridge's nickname and services account, an absolute private SASL password path,
and a freshness window of 1–300 seconds.

```json
{"irc_bridges": [{
  "enabled": true, "id": "libera", "address": "203.0.113.5:6697",
  "tls_server_name": "irc.example.net", "nickname": "airc-bridge",
  "account": "airc-bridge", "password_file": "/etc/airc/irc-password",
  "freshness_seconds": 120,
  "guest_prefix": "irc-",
  "accounts": [{"external": "carol", "nick": "carol",
                "account_token_file": "/etc/airc/carol.token"}],
  "routes": [{"room": "#external", "target": {"kind": "room", "name": "#bridged"},
              "operations": ["send", "history.read"]}]
}]}
```

`routes` (1–128) bridge one channel each to one fixed AIRC target, with
distinct operations from `send`, `context.read`, `history.read` and `search`
(history and search need the cursor key). Rooms that differ only by ASCII case
collide. `guest_prefix`, `guests` and `accounts` are described under
[bridge authors](#bridge-authors). Posting uses the gateway's author upstream
(the top-level `upstream`), exactly as plain author requests do.

Services-account names are literal ASCII letters, digits, dot, underscore or
hyphen, at most 64 bytes. Channels use a fixed `#` plus ASCII letters, digits,
underscore or hyphen subset, at most 64 bytes; this subset folds identically
under ASCII and RFC1459 mappings. Nicknames use the bounded standard ASCII
subset at most 30 bytes.

## Bridge authors

The IRC, XMPP and Matrix bridges share one author model. The gateway is not an
identity authority: it maps the identifier the *provider* verified (IRC
services account from the `account` tag; the real bare JID a non-anonymous MUC
reports; the Matrix sender as the authenticated homeserver returns it) to an
AIRC author, and AIRC decides what that author may do.

- **Mapped account.** An `accounts` entry maps one external identifier
  (`external`, compared without case) to a registered AIRC `nick` whose account
  token is read from the owner-only `account_token_file` at startup and held in
  memory only. Messages post after AIRC's own login, exactly like a request
  carrying that token. A bad token file fails startup.
- **Guest.** Anyone else posts as an AIRC guest named `guest_prefix` plus the
  identifier (defaults `irc-`, `xmpp-` and `mx-`). XMPP and Matrix users of the
  bridge account's own domain or homeserver use only their localpart
  (`alice@example.org` on an `example.org` bridge is `xmpp-alice`); others keep
  their whole identifier. The result is lowercased; when it contains bytes
  outside letters, digits, `-` and `_` (which become `-`) or exceeds 30 bytes,
  it is shortened and `|` plus ten hex digits of a SHA-256 of the whole
  identifier is appended (`xmpp-bob-elsewhere-o|3f2a9c41d0`). Plain names never
  contain `|`, so the two forms cannot collide, and different identifiers get
  different hashes. Set `"guests": false` to accept only mapped accounts.
- **No borrowed names.** Configuration refuses guest prefixes where one starts
  another (so two bridges cannot produce the same guest, and two IRC bridges
  must set distinct prefixes) and mapped nicknames that start with any guest
  prefix. An external user therefore cannot choose a mapped nickname or
  another bridge's guest: a services account literally named `carol-acct`
  posts as `irc-carol-acct`, never as the mapped account `carol-acct`.
  As everywhere in AIRC, a guest nickname is not proof of identity to other
  AIRC clients; only a mapped (registered) account is.

Each author session sends `WEBIRC` with source `bridge:<bridge-id>:<nick>`
(lowercase; a hash when the nickname has characters outside AIRC's source
grammar or the label would exceed 64 bytes), so AIRC's per-source limits apply
to each external user separately, and the gateway's own limits (120 requests a
minute per source, 60 per author) do too. After a gateway restart a pending
job falls back to the bridge-wide source `listener:<bridge-id>`.

Each message's request ID is derived from the provider's stable message ID, so
a replay of the same message (reconnect, duplicate delivery, gateway restart)
is the same AIRC job and at most one post. Jobs belong to the author's scope,
as for every other carrier.

The private registration implementationThe private registration implementation sends a blind `CAP REQ` for
`sasl account-tag message-tags server-time`, followed by NICK and USER.
It requires one complete acknowledgement of the exact requested set before
starting SASL PLAIN. A single empty challenge triggers complete base64
credentials in 400-byte chunks; an exact final 400-byte chunk is followed by
`AUTHENTICATE +`. Constructor inputs are bounded; it owns a password copy and
clears it after emission or close. Returned command buffers belong to the caller,
which must clear credential-bearing buffers after writing them.

Registration requires both account confirmation (`900`) naming the configured
bridge account and SASL success (`903`), in either order, before CAP END.
Their client parameter may be the nickname or `*`, which IRCv3 SASL permits
before registration completes (Ergo sends `900 * * <account>` and `903 *`).
Only a subsequent matching welcome (`001`) establishes protocol readiness.
Missing capabilities, SASL failure/logout (`901`–`907`), numerics refusing a
registration step (CAP/command unsupported, nickname missing/invalid/in use/
unavailable, not registered, missing parameters, password or ban), `ERROR`,
early user events, wrong account/nickname, or more than 128 registration
messages refuse the session. Other numerics, such as `422` (no MOTD), are
informational. After welcome, only `ERROR`, SASL failure/logout and removal of
a required capability end readiness; other `4xx`/`5xx` replies do not.
PING is answered with a whole bounded PONG.

This state machine performs no I/O, TLS validation, deadline management or
AIRC authentication. Readiness alone is not an identity proof. The later owner
must feed it commands read directly from its verified server connection and
join that connection's lifecycle before exposing opaque event proofs.
The registration rules follow [capability negotiation](https://ircv3.net/specs/extensions/capability-negotiation)
and [SASL](https://ircv3.net/specs/extensions/sasl-3.1).

`auth.IRCServer` snapshots the provisioned endpoint,
TLS name/roots, nickname, account and exact private password bytes. The native
credential opener verifies ownership, regular-file identity and permissions
before a bounded 1,025-byte read; passwords must fit the selected 1–1,024-byte
PLAIN profile. Whitespace is never trimmed. Registration rejects NUL, CR/LF or
invalid UTF-8 credentials. Custom trust roots use the existing verified reader;
omitting a CA path uses system trust. No caller TLS overrides are accepted.

`Connect` dials the numeric configured endpoint and uses a single three-second
budget across dial, TLS 1.3 certificate/name verification, CAP, SASL and welcome.
Only a completed verified certificate chain and successful registration return
a session. Parent cancellation closes the connection; session close joins that
callback and clears registration state. Credential-bearing command buffers are
cleared after writing. The server credential snapshot must be closed only after
its Connect/session users have joined. Each returned session is for serial use.

`ReadEvent` alone exposes an opaque immutable `VerifiedIRCEvent`, reading directly
from that verified session and processing control messages. Each call handles at
most 128 lines; reaching that bound returns `auth.ErrIRCEvent` and leaves the
session usable. An idle session stays connected: after 60 seconds without any
received line the bridge sends `PING airc-keepalive`, and only a further 30
seconds without any line (PONG or otherwise) closes it. Each write has a
three-second budget. Missing server account/ID/time tags, client-only account
tags, unbridged rooms, bridge-account echoes, malformed prefixes, blank bodies
and CTCP are refused, as are unmapped accounts when guests are off. Whole
Unicode bodies and whitespace are preserved.

The selected timestamp profile requires exact UTC millisecond formatting, refuses
future timestamps and leap seconds, and expires at the original server time plus
the configured freshness interval. Replay cannot renew that deadline. The
64-character request ID hashes the bridge ID and the opaque server message ID;
the nickname and host do not change it.

`Dispatcher.Bridge` (built by `dispatch.ForBridge`) admits the event as its
author: a send becomes an author job through the same journal path as plain
author requests; a read runs as the author. The bridge's operations and route
targets are its policy. Sends get no IRC acknowledgement.

`IRCSession.JoinRooms` snapshots rooms from its immutable
routes, deduplicates and sorts them, and sends one JOIN per room after verified
registration. It requires the bridge nickname's own JOIN confirmation from the
verified server session for every room. Received nicknames and rooms must first
pass the selected ASCII profile, so Unicode case-fold lookalikes cannot establish
membership. One three-second deadline and at most
512 received lines bound the complete joining step; PING does not renew either
budget. JOIN refusal numerics (`403`, `405`, `437`, `461`, `471`, `473`–`477`,
and the common `479`, `489` and `520` extensions), premature PRIVMSG,
incomplete confirmation, control revocation and I/O failure close the session.
Informational or unrelated numerics, such as `422` or `401`, are ignored. Another user's JOIN is not the
bridge's membership. Room keys and invite provisioning are not supported by
this selected profile; the owner must arrange membership at the IRC server.
The service must call joining before event ingestion. Protocol reference:
[JOIN](https://dd.ircdocs.horse/refs/commands/join).

The `adapter.ServeIRC` owner reserves one shared session slot while
connecting, registering, joining and reading. Valid proofs use shared request
capacity and bounded dispatcher admission. A refused publication is classified
as `auth.ErrIRCEvent` (also an authentication refusal) so it does not disconnect
other authors; I/O and control failures end the connection. Each event read
retains the 128-line bound; an idle connection is kept by the keepalive above
rather than reconnected, so a quiet room causes no JOIN/QUIT churn. Only a
keepalive timeout or another failure reconnects. Reconnection backs off from
100 milliseconds to five seconds, resetting after durable admission or a whole
successful read reply. Publications lost between connections are not recovered. Request capacity
exhaustion or a flood limit can lose an event before intake. Send events
receive no acknowledgement; read results use plain NOTICE replies. Parent cancellation closes and joins the verified session
and releases only owned capacity; the caller clears server credentials after
the owner joins.

Service preparation validates all credential references and constructs immutable
server/dispatcher owners before READY without dialing IRC. READY lists each
prepared bridge ID with protocol `irc`; it reports local preparation, not IRC
membership or AIRC acceptance. Owners then dial and join, share global capacity
with other ingress, and join before credentials are cleared and the journal
closes. Preparation failure emits no READY and releases owned resources.
The service supports IRC-only configuration with `listeners: []`.

Integration fixtures use a scripted verified IRC TCP/TLS server and the real
isolated gateway CLI and AIRC server, with an independent native observer:
the services account `alice` arrives as `irc-alice`, a replay under a new
nickname and after a gateway restart is one post, and receipts are read as
that guest over a plain Unix frame. A real Ergo run is recorded in
[testing](testing.md#bridge-authors-against-ergo-and-prosody).

## Read requests and plain replies

A member sends one ordinary channel message starting with `AIRC-READ/1`,
optionally followed by a JSON object (at most 2,048 bytes):

```text
AIRC-READ/1 {"operation":"history.read","limit":10,"correlation_id":"h1"}
```

The fields are `operation` (`history.read`, `search` or `context.read`;
default `history.read`), `cursor`, `limit` (default 20 for history and
search), `query`, `from`, `parent_id`, `thread_mode`, an optional
`correlation_id` (1–64 letters, digits, `-`, `_`) and an optional `target`,
which must equal the route's target. Unknown fields are refused. The same
syntax works on the XMPP and Matrix bridges. A message that starts with the
marker but is malformed is answered with `invalid_request` and never posted;
an operation the route does not list is answered with `unsupported_operation`.
Request text is visible to the channel like any message.

The read runs as the sender's author. The reply is plain contract JSON,
`adapter.BridgeResult`: `version`, `protocol` (`irc`), `request_id`,
`correlation_id`, `author` (the AIRC nickname), `room`, `target`, and exactly
one of `result` (the usual `DetachedResult`, with its history cursor) or
`error`. It goes only to the requesting nickname as NOTICE lines:

```text
NOTICE alice :AIRC-RESULT/1 <first 16 hex of request_id> <index>/<count> <part>
```

Parts are at most 360 bytes, split on character boundaries; joining them in
index order gives the JSON (`ircwire.ParseResponseLine` reads a line). Results
over 16 KiB are answered with `capacity_exceeded` instead; ask for a smaller
`limit`. Nothing is encrypted or signed: the IRC server and anyone who can read
the requester's NOTICEs see the result, which is the history the channel's
author may read anyway. The server's flood policy can delay or drop lines;
retry with a new message.
