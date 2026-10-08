# XMPP bridge profile

The private `xmppwire` prerequisite parses and encodes standalone XML elements;
it is not connected to authentication, journal admission or the service.
No XMPP mode is qualified by this codec.

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
parsed fields. The planned non-anonymous MUC profile will use verified server
membership to map real JIDs, never nicknames alone. See
[XEP-0045](https://xmpp.org/extensions/xep-0045.html).
Replay authority must use the verified room/server's stanza ID and provenance;
client-origin IDs alone are spoofable. See
[XEP-0359](https://xmpp.org/extensions/xep-0359.html#security).
TLS/authentication, membership, loop prevention, durable admission, service
ownership, bounded read replies and real client/server/native qualification
remain in the roadmap.

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
TLS/session preparation and actual membership proofs remain separate work.

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
verified session; they are not called by the session owner yet and do not map
other occupants or establish native acceptance. See
[XEP-0045 non-anonymous entry](https://xmpp.org/extensions/xep-0045.html#enter-nonanon).

The private verified session now calls those wire checks in `JoinRooms`, which
sorts and deduplicates only immutable provisioned rooms. One three-second deadline
and a shared 512-stanza budget cover all serial joins. Each room must confirm
exact non-anonymous own membership before the next join. Other occupants'
ordinary presence in that same room cannot confirm this membership and is
currently discarded; initial roster tracking is still required before exposing
any event proof. Errors, changed own identity, anonymous confirmation, unexpected
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
fields remain server-supplied data. The verified session has not yet retained
them or exposed an event identity proof; author mapping and loops remain open.
