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
