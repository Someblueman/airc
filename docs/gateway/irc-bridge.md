# IRC bridge profile

The selected bridge connects to an owner-configured, TLS-verified IRC server.
Services-account/room grants map IRC authors to existing AIRC principals and
bindings. A nickname, hostmask or client-only tag cannot establish authority.
Server `account`, `msgid` and `time` tags are required for admitted events;
missing or invalid identity, stable event identity or freshness is refused.
The bridge services account is excluded from ingress to prevent its own echoes
from becoming new AIRC sends. No server or external account is configured by
this specification, and no live service is contacted.

The service uses bounded client-side IRCv3 framing and atomic journal admission
for configured send routes, plus encrypted bounded read replies as specified
below. Runtime qualification of reads and of a real IRC server/client remains
on the [roadmap](ROADMAP.md); the new implementation has only been compiled.

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

Provisioning is defined by `config.IRCBridge`, `config.IRCRoute` and
`Config.ValidateIRCBridge`, registered as the optional `irc_bridges` JSON array.
At most 32 bridge profiles are allowed; bridge IDs and literal dial endpoints
must be unique. Each has an explicit `enabled` flag. Read routes additionally
require the encrypted reply configuration below.
A bridge requires a stable owner ID,
numeric non-wildcard dial endpoint with nonzero port, verified TLS server name,
optional absolute CA path, explicit nickname and services account, and an
absolute private SASL password path. Runtime ownership must verify the password
file and TLS connection; validation does not read credentials or authenticate.

Freshness is explicitly 1–300 seconds, with 1–128 fixed account/room grants.
Each grant resolves to an active existing principal authorized for the exact
native target and all declared operations (send, context, history or search).
History/search require the existing cursor key. No signing key is required for
this server-authenticated profile; the trusted server session must supply the
authentication proof before dispatch. Current durable authority still applies.

Services-account names are literal ASCII letters, digits, dot, underscore or
hyphen, at most 64 bytes. Ingress comparisons are exact; case aliases confer no
authority. Grants whose account and room differ only by ASCII case collide and
are refused. Channels use a fixed `#` plus ASCII letters, digits, underscore or
hyphen subset, at most 64 bytes; this subset folds identically under ASCII and
RFC1459 mappings. The bridge's own account cannot be granted, including case
aliases. Nicknames use the bounded standard ASCII subset at most 30 bytes.

The private registration implementation sends a blind `CAP REQ` for
`sasl account-tag message-tags server-time`, followed by NICK and USER.
It requires one complete acknowledgement of the exact requested set before
starting SASL PLAIN. A single empty challenge triggers complete base64
credentials in 400-byte chunks; an exact final 400-byte chunk is followed by
`AUTHENTICATE +`. Constructor inputs are bounded; it owns a password copy and
clears it after emission or close. Returned command buffers belong to the caller,
which must clear credential-bearing buffers after writing them.

Registration requires both account confirmation (`900`) naming the configured
bridge account and SASL success (`903`), in either order, before CAP END.
Only a subsequent matching welcome (`001`) establishes protocol readiness.
Missing capabilities, SASL failure/logout, registration errors, early user
events, wrong account/nickname, or more than 128 registration messages refuse
the session. Removal of a required capability closes readiness. PING is answered
with a whole bounded PONG; it does not reset any future owner deadline.

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

`NewIRCServer` also binds fixed grants to active identities owned by the supplied
verifier; configuration claims cannot expand its target or operation permissions.
`ReadEvent` alone exposes an opaque immutable `VerifiedIRCEvent`, reading directly
from that verified session and processing control messages with a three-second,
128-line budget. Missing server account/ID/time tags, client-only account tags,
unmapped accounts/rooms, bridge-account echoes, malformed prefixes, blank bodies
and CTCP are refused. Whole Unicode bodies and whitespace are preserved.

The selected timestamp profile requires exact UTC millisecond formatting, refuses
future timestamps and leap seconds, and expires at the original server time plus
the configured freshness interval. Replay cannot renew that deadline. A stable
64-character request ID hashes the bridge/network ID and opaque server message
ID. A separate fingerprint binds account, room, exact timestamp, whole body and
native identity/target; mutable nickname/host metadata does not change it.

These proofs are not journal intake. Service dispatch checks current durable
authority and atomically binds global event fingerprints. Scripted IRC peers
verify this profile; they do not qualify a real IRC server/client.

`Store.AdmitIRC` accepts only opaque event proofs with
send permission, checks original freshness and current durable authorization,
and atomically binds the network/event request ID to its event fingerprint,
principal, binding and exact replay deadline while admitting the native send.
Schema v16 adds the global `irc_events` table without rewriting existing jobs,
replay, authorization, OSCORE or broker state. Concurrent identical delivery
coalesces; changed content, timestamp or native authority conflicts. A result
that has been pruned cannot be recreated while its event mark remains.

The table is capped at 16,384 events. At capacity identical replay still works;
new events are refused. Expired marks are reclaimed only when no native job with
that request ID remains, including during normal maintenance. Exact second and
nanosecond deadline fields preserve the original timestamp boundary. No new mark or
job is committed by a failed admission transaction. Native delivery still uses the
existing journal lease, retry lifetime and uncertain-outcome policy; event
freshness governs admission, rather than resetting the native delivery lifetime.
Read replies use the separate proof/dispatcher path below; they never enter
this Send journal. Real IRC server/client qualification remains separate work.

`Dispatcher.IRC` requires the event identity to belong
to its exact verifier and Send to be configured before calling `Store.AdmitIRC`.
It returns the existing durable job result without contacting AIRC directly.
Foreign or zero proofs, a missing journal/verifier, unconfigured sends, expired
events and durable revocation cannot admit work through this entry point.
The service owner applies shared request admission to sends and reads.

`IRCSession.JoinRooms` snapshots rooms from its immutable
grants, deduplicates and sorts them, and sends one JOIN per room after verified
registration. It requires the bridge nickname's own JOIN confirmation from the
verified server session for every room. Received nicknames and rooms must first
pass the selected ASCII profile, so Unicode case-fold lookalikes cannot establish
membership. One three-second deadline and at most
512 received lines bound the complete joining step; PING does not renew either
budget. Refused joins, premature PRIVMSG, incomplete confirmation, control
revocation and I/O failure close the session. Another user's JOIN is not the
bridge's membership. Room keys and invite provisioning are not supported by
this selected profile; the owner must arrange membership at the IRC server.
The service must call joining before event ingestion. Protocol reference:
[JOIN](https://dd.ircdocs.horse/refs/commands/join).

The `adapter.ServeIRC` owner reserves one shared session slot while
connecting, registering, joining and reading. Valid proofs use shared request
capacity and bounded dispatcher admission. A refused publication is classified
as `auth.ErrIRCEvent` (also an authentication refusal) so it does not disconnect
other authors; I/O and control failures end the connection. Each event read
retains the existing three-second/128-line bound. Reconnection backs off from
100 milliseconds to five seconds, resetting after durable admission or a whole
successful read reply. Idle read
expiry also reconnects; this bounded profile does not promise a persistent idle
connection or recover publications lost between connections. Request capacity
exhaustion can lose an event before intake. Send events receive no acknowledgement;
read results use encrypted replies. Parent cancellation closes and joins the verified session
and releases only owned capacity; the caller clears server credentials after
the owner joins.

Service preparation validates all credential references and constructs immutable
server/dispatcher owners before READY without dialing IRC. READY lists each
prepared bridge ID with protocol `irc`; it reports local preparation, not IRC
membership or AIRC acceptance. Owners then dial and join, share global capacity
with other ingress, and join before credentials are cleared and the journal
closes. Preparation failure emits no READY and releases owned resources.
The service supports IRC-only configuration with `listeners: []`.

Selected integration fixtures use a scripted verified IRC TCP/TLS server and the
real isolated gateway CLI and AIRC server, with an independent native observer.
They verify exact mapped author, room and Unicode/trailing-space body, stable
replay across nickname changes and gateway restart, a later native fence,
single-send retained results, and joined shutdown with journal release. This is
real native AIRC delivery evidence through the selected scripted IRC profile;
it does not qualify a real IRC server/client or any reply path. Full unchanged-source acceptance passed; see
[the exact evidence](testing.md#irc-service-integration-acceptance).

## Encrypted bounded read replies

The chosen reply policy uses a fixed P-256 recipient key for each account/room
grant. Add `response_recipient_key_id` and absolute
`response_recipient_public_key_file` to a route with `context.read`,
`history.read` or `search`, and configure the global Ed25519 response signing
key. Read-only and mixed send/read routes are supported. The existing trusted
server account proof authenticates read requests; requester signing keys are
not required. Reply keys never extend the principal's target or operation grants.

Clients negotiate `message-tags` and send one tagged message in the configured
IRC room. `ircwire.ReadRequestLine` constructs the complete wire form:

```text
@+airc/read=<base64url-json> PRIVMSG #external :AIRC-READ/1
```

The tag contains canonical unpadded base64url of at most 2,048 bytes of strict
JSON, for example:

```json
{"correlation_id":"history-1","operation":"history.read","limit":10}
```

The only accepted fields are `correlation_id`, `operation`, `cursor`, `limit`,
`query`, `from`, `parent_id` and `thread_mode`. Normal AIRC request validation
determines which fields each of the three read operations can use. A correlation
ID is 1–64 ASCII letters, digits, underscores or hyphens; clients choose a fresh
one for each outstanding request. Identity and target come exclusively from the
trusted account/room grant. `request_id` remains the existing digest of network
ID and server `msgid`; the correlation handle is not a durable identity.

**Request parameters are visible to the IRC server and room members**, including
search text and opaque cursor tokens. Do not put credentials in the request.
Only returned native content is encrypted. The exact marker/tag pair is reserved:
missing, malformed, over-limit or unauthorized read requests are refused whole,
and never become ordinary AIRC sends. Read proofs cannot enter `Dispatcher.IRC`
or `Store.AdmitIRC`. Freshness remains the original server event's deadline.

`Dispatcher.IRCRead` accepts only an immutable proof belonging to its verifier,
checks the route operation and original freshness, and uses the existing durable
read permissions and backend. Requests have a three-second native-work budget,
capped by event expiry. Whole history/search pages retain the existing cursor;
use that cursor in a fresh correlated request to continue. There are no watches.
Reads do not create jobs, extend replay markers or reschedule sends.

The whole response payload is `IRCReadResult`: `version:1`, `protocol:"irc/read"`,
`correlation_id`, server-derived `request_id`, literal account, lowercase IRC room,
native target, and exactly one `result` or `error`. A successful `result` is the
existing complete `DetachedResult`. Its scope includes realm, principal, binding
and a route from `auth.IRCReadResponseRoute`: SHA-256 of these length-prefixed
UTF-8 strings in order (each length is an unsigned eight-byte big-endian integer):

```text
airc-irc-read-response-v1, network-id, account, lowercase-room, target-kind, target-name
```

The standard response profile signs with Ed25519 and encrypts with P-256
ECDH-ES/A256GCM. Its limits remain 32 KiB clear payload, 64 KiB protected token
and 120-second validity. Oversized results produce a protected capacity error
before any result bytes are written; native content is never truncated.

The token is split into at most 22 pieces of 3,000 characters. Each piece goes
as a private NOTICE to the requesting nickname, using zero-based indexes:

```text
@+airc/result=<64-hex-request-id>/<index>/<count>/<chunk> NOTICE nick :AIRC-RESULT/1
```

Client tag data fits IRCv3's 4,094-byte allowance independently of the 512-byte
message limit. Server forwarding may add its own tags. Chunks are paced at
100 ms intervals, within one five-second write budget also capped by original
event expiry. The server must permit this bounded burst; its flood policy can
still drop replies. Cancellation closes the session and joins the serial owner.
Shared request capacity remains held through the whole reply.

Clients collect at most 22 fragments for a bounded time and call
`ircwire.JoinResponseFragments`; duplicate, mixed, missing or oversized sets
are refused. They then use `auth.OpenResponse` with the configured signing key,
recipient key and expected scope, verify all payload routing fields and match
the protected correlation ID to their outstanding request. Do not expose a
prefix or treat unsigned chunk metadata as authenticated content. Nickname
changes or reassignment may lose or misroute encrypted tokens but cannot grant
access to native plaintext. IRC write success is not recipient delivery; retry
a lost read with a fresh correlation ID and the same opaque cursor.

Implementation review and Darwin ARM64/Linux AMD64/Linux ARM64 production builds
cover this change. Tests, real IRC-client/server interoperability, encrypted
cursor reads, refusal, replay, flood behavior and shutdown qualification remain
deferred. Earlier send evidence above does not qualify this new reply path.
