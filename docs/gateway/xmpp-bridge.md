# XMPP bridge profile

The XMPP bridge joins fixed non-anonymous MUC rooms over verified direct TLS.
An ordinary groupchat message from any occupant is posted to the room's AIRC
target as that occupant's author; a typed `AIRC-READ/1` request is answered
with a private plain-JSON chat message. Occupants need no AIRC keys: the
earlier requirement that every body be an AIRC Ed25519-signed envelope is
gone, and nothing is encrypted. It is registered under `xmpp_bridges`.

The only identity input is the real bare JID the non-anonymous room reports for
the occupant on the bridge's own verified session (never the occupant
nickname). The author is the [bridge author](irc-bridge.md#bridge-authors) for
that JID: a mapped AIRC account, else a guest such as `xmpp-alice`.

The selected codec bounds each element to 64 KiB, nesting to 16 elements,
XML tokens to 2,048 and attributes per element to 32 (including namespace
declarations on input). It requires namespace-qualified elements, compares
expanded names, rejects duplicate expanded attributes, and preserves ordered
mixed content. `Text` refuses child elements so a plain body cannot silently
lose markup. Encoding returns a whole element or no output and bounds its
output buffer, including XML escaping expansion. Cyclic input is refused by
the depth bound. Invalid UTF-8 and XML characters are refused rather than
replaced. XML character references and CDATA retain their decoded content;
XML newline normalization applies on input.

This strict profile refuses DTDs, processing instructions and comments.
`Parse` accepts standalone elements. The private `Stream` reader now handles
an unclosed stream root and complete children with inherited namespaces.
It enforces a fresh 64 KiB byte budget for opening and each child read,
including intervening whitespace, through a byte reader before XML tokenization;
its underlying prefetch buffer is fixed at 4 KiB. Depth, token, attribute and
namespace checks share the standalone element implementation. Truncated input
and framing refusals are terminal; an explicit root close returns EOF.

`Open` permits an optional XML 1.0 declaration with absent or UTF-8 encoding,
and refuses standalone flags and other processing instructions. `Restart`
retains buffered bytes and replaces decoder namespace state only when the
negotiation owner has validated a transition. A TLS upgrade instead requires
a new reader on the secured connection. The caller owns deadlines, connection
closure and authentication; no XML field is an identity proof. See
[RFC 6120 XML streams](https://www.rfc-editor.org/rfc/rfc6120.html#section-4).

Room and author authentication must be established independently of these
parsed fields. The private non-anonymous MUC session uses verified server
membership and current roster state to map real JIDs, never nicknames alone. See
[XEP-0045](https://xmpp.org/extensions/xep-0045.html).
Transport provenance uses the verified room's stanza ID (XEP-0359);
unsigned client-origin IDs are never used. See
[XEP-0359](https://xmpp.org/extensions/xep-0359.html#security).

Provisioned accounts, authors and rooms use the private `BareJID` canonical
subset: lowercase ASCII localpart (1–64 bytes, letters/digits/dot/underscore/
hyphen) and DNS domain (1–253 bytes, labels 1–63 bytes, no leading/trailing
hyphens or empty labels). Aliases are refused rather than silently normalized.
`FullJID` separates that exact bare address from a case-sensitive 1–64 byte
ASCII resource (letters/digits/dot/underscore/hyphen). It does not authenticate
an address. This deliberately bounded provisioning profile does not implement
all internationalized forms in [RFC 7622](https://www.rfc-editor.org/rfc/rfc7622.html).

`config.XMPPBridge` and `ValidateXMPPBridge` provision one fixed
numeric, non-wildcard, nonzero-port TLS endpoint, explicit TLS server name
(equal to the account domain), optional absolute CA path, absolute private
password path, canonical bridge account/resource and a 1–300 second freshness
window. `routes` (1–128, distinct rooms) bridge one MUC room each to one fixed
AIRC target with operations from `send`, `context.read`, `history.read` and
`search`; `guest_prefix` (default `xmpp-`), `guests` and `accounts` (bare JID
to AIRC nickname and account token file) follow the shared
[bridge author](irc-bridge.md#bridge-authors) rules. The collection permits at
most 32 distinct bridge IDs and endpoints. Validation never dials.

Private wire helpers now emit a bounded client stream header, require an
advertised SASL PLAIN mechanism, produce PLAIN with the provisioned bare JID as
authorization identity and its localpart as authentication identity, and accept
only an empty SASL success response. The password is bounded to 1–1,024 bytes
of valid UTF-8 XML characters without NUL; temporary credential byte buffers
are cleared, and the caller must clear the returned auth wire after writing.
PLAIN must only run inside the session owner's verified TLS connection; these
helpers do not establish that property or prove authentication by themselves.

After validated success/restart, resource binding requires the binding feature,
a bounded safe request ID and the exact configured account/resource. The result
must have matching IQ type/ID, optional `from` matching the account domain and
optional `to` matching the exact full account, and a unique matching bind/JID.
Nickname/resource changes do not silently establish another account. The
session owner must enforce exchange order, deadlines and cancellation before
any membership or event proof is exposed. See
[RFC 6120 SASL and binding](https://www.rfc-editor.org/rfc/rfc6120.html#section-6)
and [RFC 4616 PLAIN](https://www.rfc-editor.org/rfc/rfc4616.html).

The private `auth.XMPPServer` prepares immutable routes, the bridge author map
(reading mapped account tokens), verified trust roots and a private bounded
password snapshot. Close clears credentials only after the caller joins all
session users.

`Connect` uses direct TLS 1.3 to the fixed endpoint, with certificate-chain/name
verification and SNI equal to the account domain. Construction therefore requires
`tls_server_name` to equal that domain. It offers `xmpp-client` ALPN and refuses
any other negotiated protocol. This is a provisioned direct-TLS profile, without
SRV discovery or STARTTLS fallback; see
[XEP-0368](https://xmpp.org/extensions/xep-0368.html).
One three-second deadline bounds dial, handshake, both stream openings, PLAIN,
SASL success/restart and exact resource binding. Server stream headers must name
the expected account domain, XML stream version 1.0 and a bounded nonempty ID.
A fresh random binding request ID correlates the exact result. Success clears
the initial deadline; subsequent membership/event I/O still needs its own bound.
Parent cancellation closes the socket, and session Close joins that callback.
Failures close the session before returning. Credential wire buffers are cleared
following writes. Readiness here establishes the selected authenticated session,
not AIRC identity proof or room membership. The service owner then joins only
the configured rooms before reading application intent.

Private MUC wire helpers request the exact provisioned room/resource with
`history maxstanzas="0"`. `OwnJoined` requires ordinary presence from that exact
occupant address, an optional exact full-account recipient, one MUC user item,
explicit self-presence 110 and non-anonymous status 100. It refuses newly created
room status 201 and assigned nickname status 210. The item must report a
participant/moderator role and a permitted affiliation; an optional real JID
must match the exact bound account/resource. Duplicate item/status fields and
wrong namespaces cannot establish confirmation. These checks depend on a
verified session and are called by the private room owner. They do not
establish native acceptance. See
[XEP-0045 non-anonymous entry](https://xmpp.org/extensions/xep-0045.html#enter-nonanon).

The private verified session now calls those wire checks in `JoinRooms`, which
sorts and deduplicates only immutable provisioned rooms. Before each join, the
verified session queries that exact room with a fresh random discovery ID and
requires the selected features and correlated result. One three-second deadline
and a shared 512-stanza budget cover all discovery replies and serial joins. Each room must confirm
exact non-anonymous own membership before the next join. Other occupants'
ordinary presence in that same room cannot confirm this membership; each
occupant's canonical real JID is retained. Errors, changed own identity, anonymous confirmation, unexpected
room or unexpected early non-presence input close and join the session. Subject
notices for already joined rooms are consumed without granting any authority. Foreign presence
cannot renew the deadline. The absolute deadline is checked after every decoded
stanza and before success, so buffered input cannot bypass expiry. Success clears the join deadline for the next bounded
operation. These prerequisites alone provide no native message/read qualification.

The private `Presence` wire parser now handles other occupants in a provisioned
room. Arrivals require canonical full real JIDs and participant/moderator/visitor
roles; the occupant and optional recipient must match the selected room/session.
Duplicate items, error presence, wrong namespaces/rooms/recipients, self-status
110 and room transitions to anonymous status 173/174 are refused. Departure
accepts an optional real JID and removes only the exact old occupant address;
a nickname-change hint never transfers identity to the replacement. These
fields remain server-supplied data consumed by the verified session. It retains
a roster of at most 4,096 exact occupant-to-full-real-JID entries; a
bridge-account arrival removes any old identity at the same nickname, and any
other arrival replaces it. Departure releases that exact entry. Existing
entries can refresh at capacity, while new entries are refused. Each join starts
with fresh roster and confirmation state. Post-join bounded message reading
consumes this roster.

Private room-discovery wire helpers send `disco#info` to the exact provisioned
bare room. A result must correlate the exact request ID and room sender, with
an optional exact session recipient, one query without a node, a conference/text
identity, and distinct MUC, `muc_nonanonymous` and `urn:xmpp:sid:0` features.
Anonymous/semi-anonymous contradictions and malformed feature children are
refused. See [XEP-0030](https://xmpp.org/extensions/xep-0030.html) and
[XEP-0359 ID provenance](https://xmpp.org/extensions/xep-0359.html#security).
These helpers now run on the verified session before each room join.
Discovery failures close and join the session before any room authority is
retained. The absolute deadline is also checked after discovery decoding.
Transport proofs still require validated message-ID provenance;
unsigned client-origin IDs alone do not establish authority or trustworthy freshness.

Private `GroupMessage` parsing now preserves one complete plain body and an
opaque room-scoped stanza ID. It requires exact groupchat type, canonical room
occupant and optional exact recipient, one unqualified-language body without
nested markup, and a unique bounded `stanza-id` whose `by` equals the room.
Client `origin-id` is not used as the admission ID. This remains parsed data;
verified discovery and current roster ownership are checked by the session
before an author/room proof is exposed. A message carrying an XEP-0203 `delay`
element is room history replayed on entry, not a live message, and is ignored.

Live MUC messages carry no trustworthy server timestamp, and a raw `delay`
stamp can be forged ([XEP-0203 security](https://xmpp.org/extensions/xep-0203.html#security)),
so the bridge joins with `maxstanzas="0"`, ignores delayed messages and starts
each message's freshness window on arrival.

The private session exposes immutable `VerifiedXMPPMessage` values only
through `ReadMessage`, carrying the resolved author, route target and request,
the exact real author/room and the whole body. Its zero value grants nothing.
Request IDs hash the bridge ID, issuing room and opaque stanza ID; nickname
changes do not change them, so a redelivered message is one AIRC job. Each
started stanza owns a three-second frame budget beginning at the first received
byte (including whitespace), and a 128-control-stanza cap before a publication.
Quiet sessions wait for input until parent cancellation; a partially
transmitted frame cannot extend its deadline. Close and parent cancellation,
malformed presence, loss of own membership, unknown rooms, I/O and budget
exhaustion prevent proof issuance and close failed reads. Occupants without a
real JID, bridge-account echoes, delayed or malformed publications (and
unmapped occupants when guests are off) are refused without destroying a usable
session.

## Service ownership, plain messages and replies

```json
{"xmpp_bridges": [{
  "id": "xmpp-main", "enabled": true, "address": "192.0.2.20:5223",
  "tls_server_name": "chat.example", "account": "gateway@chat.example",
  "resource": "airc", "password_file": "/etc/airc/xmpp-password",
  "freshness_seconds": 120,
  "accounts": [{"external": "carol@chat.example", "nick": "carol",
                "account_token_file": "/etc/airc/carol.token"}],
  "routes": [{"room": "agents@conference.chat.example",
              "target": {"kind": "room", "name": "#agents"},
              "operations": ["send", "history.read"]}]
}]}
```

`prepareXMPP` loads credentials, routes and the author map and builds a bridge
dispatcher before starting network owners. The ready record lists each enabled
owner with protocol `xmpp`; readiness means local preparation, not remote
membership. Each owner holds a shared connection slot while connected and
processes messages serially; shared request capacity covers admission, native
work and replies. Exhausted capacity or a flood limit can lose a message before
admission; there is no archive cursor promising recovery. Reconnect uses
100 ms–5 s backoff and a fresh verified roster. Standard subject notices, later
self-presence and server-domain [XMPP Ping](https://xmpp.org/extensions/xep-0199.html)
are handled as before; departure, role loss or an anonymous transition ends the
session.

A plain message is a `send` to the route target as its author, through
`Dispatcher.Bridge` and the same author journal as plain author requests (each
author session sends `WEBIRC` with source `bridge:<id>:<nick>`). It gets no XMPP
acknowledgement; the room's own echo is the sender's.

A message starting with `AIRC-READ/1` is a read, with the JSON syntax described
for [IRC](irc-bridge.md#read-requests-and-plain-replies). The reply is one
`adapter.BridgeResult` (protocol `xmpp`) as the whole body of a private
`jabber:client` chat message to the requesting occupant, with XML `id` equal to
the request ID. Malformed reads get `invalid_request`, unrouted operations
`unsupported_operation`, and results over 32 KiB `capacity_exceeded`; nothing
is truncated. Replies are plain text: the XMPP server can read them.

A real Prosody run (ordinary occupants, a mapped account, an impostor occupant
nickname and a read reply) is recorded in
[testing](testing.md#bridge-authors-against-ergo-and-prosody).
