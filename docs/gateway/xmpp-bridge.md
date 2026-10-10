# XMPP bridge profile

The private XMPP prerequisites provide bounded XML framing, a verified TLS
session and provisioned room/occupant tracking. Signed original-intent admission
is implemented below; service ownership and replies remain open. No real XMPP
client/server mode is qualified, and testing of the new admission path is deferred.

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
Transport provenance uses the verified room/server's stanza ID;
unsigned client-origin IDs alone are spoofable. Signed AIRC request identity,
described below, owns native replay admission. See
[XEP-0359](https://xmpp.org/extensions/xep-0359.html#security).
Original-event freshness and durable admission are implemented with verification
deferred. Service ownership, bounded replies and real-client qualification remain
in the roadmap.

Provisioned accounts, authors and rooms use the private `BareJID` canonical
subset: lowercase ASCII localpart (1–64 bytes, letters/digits/dot/underscore/
hyphen) and DNS domain (1–253 bytes, labels 1–63 bytes, no leading/trailing
hyphens or empty labels). Aliases are refused rather than silently normalized.
`FullJID` separates that exact bare address from a case-sensitive 1–64 byte
ASCII resource (letters/digits/dot/underscore/hyphen). It does not authenticate
an address. This deliberately bounded provisioning profile does not implement
all internationalized forms in [RFC 7622](https://www.rfc-editor.org/rfc/rfc7622.html).

The private `config.XMPPBridge` and `ValidateXMPPBridge` provision one fixed
numeric, non-wildcard, nonzero-port TLS endpoint, explicit TLS server name,
optional absolute CA path and absolute private password path. The canonical
bridge account/resource is explicit. Freshness is 1–300 seconds, with 1–128
unique exact author/room grants; the bridge account cannot be an author grant.
Each grant names an active existing AIRC principal and its exact authorized
target, with distinct explicit send/context/history/search operations bounded
by that principal's existing authority. History/search require the cursor key.
These types are not registered in service JSON or file checks, do not read
credentials or dial, and do not replace verifier/current journal authorization.
Verified session preparation and room ownership are described below.

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

The private `auth.XMPPServer` now prepares immutable verifier-owned grants,
verified trust roots and a private bounded password snapshot. Constructor
permission checks use the actual verifier, so supplying a different configuration
cannot enlarge its identities or permissions. Close clears credentials only
after the caller joins all session users.

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
not AIRC identity proof or room membership. The service does not call it yet.

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
ordinary presence in that same room cannot confirm this membership; canonical
real JIDs are now retained only for explicitly granted authors. Errors, changed own identity, anonymous confirmation, unexpected
room or early non-presence input close and join the session. Foreign presence
cannot renew the deadline. The absolute deadline is checked after every decoded
stanza and before success, so buffered input cannot bypass expiry. Success clears the join deadline for the next bounded
operation. These owners remain unused by the service and provide no native
message/read qualification.

The private `Presence` wire parser now handles other occupants in a provisioned
room. Arrivals require canonical full real JIDs and participant/moderator/visitor
roles; the occupant and optional recipient must match the selected room/session.
Duplicate items, error presence, wrong namespaces/rooms/recipients, self-status
110 and room transitions to anonymous status 173/174 are refused. Departure
accepts an optional real JID and removes only the exact old occupant address;
a nickname-change hint never transfers identity to the replacement. These
fields remain server-supplied data consumed by the verified session. It retains
a roster of at most 4,096 exact occupant-to-full-real-JID entries for explicitly
granted author/room pairs; bridge-account and unmapped arrivals remove any old
identity at the same nickname. Departure releases that exact entry. Existing
entries can refresh at capacity, while new entries are refused. Each join starts
with fresh roster and confirmation state. Post-join bounded message reading
consumes this roster; the separate signed admission boundary below establishes
original-event freshness before native work.

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
before an author/room proof is exposed. That proof must then pass the signed
application admission boundary below.

Freshness must not be inferred from a raw `delay` timestamp. The
[XEP-0203 security section](https://xmpp.org/extensions/xep-0203.html#security)
explicitly describes forged delayed-delivery metadata, including stamps
putatively from a room. The selected policy uses signed AIRC envelopes in chat
bodies; delay fields and arrival time grant no original-event freshness.

The private session now exposes immutable `VerifiedXMPPMessage` values only
through `ReadMessage`, with mapped native identity, target and cloned operation
grants, exact real author/room and whole body. Its zero value grants nothing.
Stable request IDs include configured network, issuing room and opaque stanza
ID; nickname changes do not change that ID. Each read owns one absolute
three-second budget and 128-stanza cap including roster updates. Close and
parent cancellation, malformed presence, own membership changes, unknown rooms,
I/O and budget exhaustion prevent proof issuance and close failed reads.
Unmapped authors, bridge-account echoes and malformed publications are refused
without destroying a usable session. These proofs establish current session
provenance only, not historical authorship, original-event freshness or durable
replay admission. `Dispatcher.XMPP` now combines them with signed application
intent; they have no service owner yet.

## Signed original intent and durable admission

The concrete freshness choice is the existing compact Ed25519 AIRC JWS profile
as the entire groupchat body, bounded to 16 KiB. Provision the sender's signing
public key under the principal mapped to its real bare JID and room. The private
transport prerequisite can still inspect provenance without a signing key; such
a proof cannot cross the native admission boundary. Plain chat text, unsigned
timestamps, `origin-id` and room delay stamps never substitute for the envelope.
JWS authenticates content but does not encrypt it: room members and the server
can read requests and Send text according to the room's access policy.

`Verifier.AuthenticateXMPP` requires the source proof to belong to that exact
verifier's XMPP server. The envelope must verify under the mapped principal's
key, name the gateway realm, match its exact target and one of the route's Send,
ContextRead, HistoryRead or Search operations, and pass ordinary current key
and principal authorization. The signed `request_id` supplies correlation and
durable identity; it is not replaced by the room stanza ID. A sender can choose
it before publication and repeat the same signed intent without needing to know
the server's later-assigned ID.

Original signed `issued_at` may not be in the future. Admission must occur before
both `expires_at` and `issued_at + freshness_seconds`, with the configured
freshness restricted to 1–300 seconds. This profile grants no timestamp skew
extension. Arrival, reconnection, a new stanza ID or another room delay stamp
cannot renew the signed deadline. The existing request's mutation times still
have to match its signed envelope exactly.

`Dispatcher.XMPP` caps its context at that original freshness deadline and calls
the existing authenticated dispatcher. Sends therefore use the ordinary durable
principal/binding/request-ID journal key, immutable intent fingerprint, native
delivery identity, retry policy and uncertain outcomes. Identical authorized
intent coalesces across redelivery; changed intent with the same ID conflicts.
The full original signed `ReplayUntil` is retained for durable replay markers,
even though XMPP execution has a shorter deadline. Shortening retention here
could let another ingress re-admit a pruned Send and is deliberately avoided.
Already-admitted native delivery follows the existing journal lifetime/retry
contract; the shorter freshness window is not a new delivery deadline.
No new schema, second journal or XMPP-specific Send implementation is introduced.

Whole bounded reads use the existing durable read grants and cursor machinery,
under the same original deadline. The dispatcher returns the signed request ID
alongside a whole `DetachedResult`; an empty ID indicates that no authenticated
result scope was established. It does not publish responses or own connections.
Those service/reply steps remain the next separate implementation item.

This change was reviewed and compiled for Darwin ARM64 and Linux AMD64/ARM64.
Original-time rejection, principal/room mismatch, durable duplicate/conflict
handling, read deadlines and native delivery have not yet been tested through
this new boundary; runtime acceptance remains in the roadmap.
