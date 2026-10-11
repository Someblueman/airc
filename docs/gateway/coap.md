# CoAPS gateway profile

This increment implements catalogue row 25's certificate-authenticated DTLS
profile. CoAP over TLS/TCP is implemented separately in [coap-tcp.md](coap-tcp.md).
Plain UDP is described in [coap-udp.md](coap-udp.md) and plain TCP in [coap-plain-tcp.md](coap-plain-tcp.md). OSCORE
and remote constrained-device acceptance remain pending. Those are required work, not
limitations of CoAP.

An [isolated OSCORE prototype](coap-oscore-prototype.md) records native library
persistence and replay controls. It does not implement a gateway OSCORE profile.

Configure an enabled `coaps` listener with an RSA or ECDSA certificate/key,
and UDP bind address; the listener access policy applies. Certificate and hostname
verification are required at the client. The server uses DTLS 1.2, authenticated
ECDHE/AES-GCM, extended master secret and `coap` ALPN. DTLS authenticates the
server; each request names its author, as on every
[network carrier](author-network-carriers.md), and an `account.identify`
request sets the association's default author. A peer address never selects
an author.

## Requests and ordinary responses

`POST /v1/coap/rpc`, with Content-Format `application/json`, carries the existing
version-1 [FrameRequest](framed-stream-v1.md). Set `stream` to false or omit it.
The response is a JSON FrameResponse. This dispatches the same implemented
operations as other detached adapters, including replies, receipt/status reads,
history, search, directory, mutations and long-poll watches.

CoAP Block1 and Block2 transfer whole bodies in blocks of at most 1024 bytes.
The assembled request is limited to 16 KiB and response to 64 KiB. A capacity
failure returns no truncated result. Use smaller history limits and opaque
cursors to retrieve additional complete pages. A transport acknowledgement
confirms a CoAP exchange; AIRC acceptance requires the durable receipt and its
native message ID.

## Live Observe reads

A GET payload cannot carry portable CoAP authentication: RFC 7252 requires it
to be ignored. This profile uses two steps instead:

1. POST a watch FrameRequest, with `stream: true`, to
   `/v1/coap/observe/register`. The response contains `version: 1`, the
   `correlation_id`, a random `observation_token` represented as 16 lowercase
   hexadecimal characters, and `expires_in_ms: 10000`.
2. On the same DTLS association, send a bodyless GET to `/v1/coap/observe`
   with Observe=0 and the decoded eight-byte token as the CoAP Token.

The registration is single-use, expires after ten seconds and preserves the
exact watch intent. It creates no native watch until activated.
The token is scoped to that association and is never placed in a URI. A fresh
association needs a fresh registration. The initial JSON response marks
carrier activation, not native authorization; subsequent Content notifications
use `application/octet-stream` and carry
[fragments](author-network-carriers.md#fragments). Reassemble a complete bounded FrameResponse before exposing
a page or advancing its cursor. CoAP Observe sequence numbers order individual
notifications; FrameResponse sequence numbers order complete logical responses.

Notifications are confirmable. Each fragment is at most 1000 bytes, so it fits
below the 1024-byte blockwise threshold. This preserves whole 64 KiB pages
without relying on the library's GET-based retrieval of large Observe bodies.
GET Observe=1 with the active token cancels the subscription. Reset and peer
close also cancel it. Native read leases, opaque cursor resume and finite stream
output use the shared dispatcher. Shutdown
joins the adapter-owned expiry, handler and watch workers. The dependency reader
closes through its session and cannot create native work after the handler gate
closes.

## Bounds and retries

The service retains its shared pre-handshake association limit and 30-second
idle renewal. Associations also expire after two minutes. Each peer
admits at most four active operations and four pending registrations. Lifetime
token/message-ID admission bounds library blockwise and duplicate-response
state; hostile partial requests cannot grow assembled input beyond the request
ceiling. Ordinary operation and stream deadlines remain four and twenty seconds.

Retry an uncertain mutation with the original stable logical request ID and
intent timestamps. A fresh CoAP exchange token or message
ID is not a new logical operation. The durable journal coalesces matching intent;
changed intent conflicts. Delivery uncertainty can still imply possible duplicates
at the native acceptance boundary. Resume a watch from the last completely
delivered opaque cursor; never advance it from an incomplete fragment set.

## Acceptance

Dated records follow. Where they mention principals, bindings, signed or
encrypted envelopes, signing keys or return routes, they describe the
[removed identity model](removed-identity.md), which no longer exists.

The current native carrier matrix exercises authenticated send, scoped receipts
and reads in CON/NON and signed/encrypted modes. Optional mutation semantics
have separate shared backend proofs. Additional fixtures cover whole
Block1/Block2 bodies, Observe reads, response-discard retry, Reset,
certificate/ALPN/envelope rejection, token isolation, durable revocation and
joined shutdown with UDP rebind.

Confirmable and non-confirmable qualifications use signed and encrypted requests over a
certificate-verified ECDSA DTLS association. Two multiline Unicode sends reach
durable accepted receipts, and exact-intent replay retains each native ID and
one send attempt. Independent CHECK records verify exact IDs, authors, targets
and bodies. Two one-message history pages match the complete native metadata,
report `more` then `ok`, and retain nonempty cursors without gaps; a caught-up
resume is empty. This proves local native acceptance beyond a CoAP transport
acknowledgement.

Both CON/NON qualifications pass twice under the race detector. The confirmable
qualification and existing four-profile carrier matrix also pass twice under `go test -race` on Darwin; `go vet ./integration/gateway` passes.

Non-confirmable submission uses explicit NON requests for send and receipt
lookup. Native acceptance is established by the durable receipt and independent
CHECK records; local send success and transport delivery alone are insufficient.
The local positive control does not promise delivery under packet loss.

Earlier recorded full-module and carrier timings are historical evidence;
current final acceptance and remote-device requirements remain in
[ROADMAP.md](ROADMAP.md). No complete catalogue row or remote-device acceptance
is claimed. The standard wire semantics are defined
by [CoAP](https://www.rfc-editor.org/rfc/rfc7252.html),
[Observe](https://www.rfc-editor.org/rfc/rfc7641.html) and
[blockwise transfer](https://www.rfc-editor.org/rfc/rfc7959.html).
