# CoAP over TLS/TCP gateway profile

Configure an enabled `coaps_tcp` listener with a TCP bind address, a
certificate/key and the shared operation allowlist. Clients must verify the
server certificate and hostname. TLS 1.3 and exact `coap` ALPN are required,
including on loopback. TLS authenticates the server; each signed/encrypted
detached envelope supplies the operation principal and binding.

The resources, JSON request/response bodies, stable logical IDs and two-step
Observe registration follow [coap.md](coap.md). An observation token belongs
to one TLS connection. Reconnection requires a fresh registration and envelope;
use the last completely delivered opaque cursor to resume a watch. The carrier
uses [RFC 8323 framing](https://www.rfc-editor.org/rfc/rfc8323.html), without
UDP message IDs, confirmable/non-confirmable modes or Reset messages.

The initial capability exchange precedes application requests. The server
advertises a 1152-byte receive frame and standard blockwise transfer. Whole
request and response bodies retain their 16 KiB and 64 KiB limits. Large
responses select the largest standard block size (16–1024 bytes) whose
fully encoded frame fits the current peer receive limit. Repeated CSM updates
can lower subsequent block sizes while preserving the requested byte offset.
A peer without blockwise support needs room for the complete encoded response;
an output that exceeds its limit closes the carrier before application bytes.

Observe setup uses `GET /v1/coap/observe?observation=<token-hex>` with the
registered eight-byte token as the request token and Observe=0. The query must
match that token. It is required when a complete AGD1 notification cannot fit
the advertised peer frame; queryless setup remains available when whole notifications fit.
The fixed AGD1 notification format is preserved. Standard Block2 continuation
requests use fresh request tokens, the same observation query and no Observe
option. The association-local query selects the immutable current notification,
so a copied token cannot retrieve a snapshot on another TLS connection.
Observe=1 cancellation uses the original observation token; its query is
optional. Pending notifications are bounded to four snapshots of at most
1000 bytes each, plus bounded options, with an absolute three-second expiry.
Continuations advance contiguous byte offsets only after successful writes;
each snapshot permits at most eight duplicate block responses. A stalled or
cancelled partial transfer closes its association.

Observe carries bounded AGD1 fragments with per-fragment freshness and a
separate sequence for each complete FrameResponse. Observe=1 or connection
close cancels a subscription. An interrupted or timed-out write closes the
connection, including when cancellation interrupts a partially written stream.

The initial peer CSM has a five-second deadline. Unknown critical CSM options,
invalid lengths and repeated non-repeatable capability options close the
connection before native work. Ordinary and Observe operations share the
existing four-operation admission cap, 16-entry receive queue, 256-token
lifetime budget and three-second receiving-blockwise expiry. Only successful
whole authenticated Content writes renew the thirty-second idle timer; signals and
partial Block1 acknowledgements do not. Connections also expire after two
minutes. Physical writes and time spent waiting for the dependency writer are
bounded to three seconds. The service joins accepted connections and all
adapter-owned handlers, expiry and watch workers. The dependency queue stops
through Session.Done with a closed handler gate preventing late native work.
Ordinary sending blockwise caches may retain their bodies until the association
deadline; the lifetime token budget bounds that state.

This profile is an additional part of catalogue row 25. Plain UDP CoAP has a separate [protected mailbox profile](coap-udp.md). Plain TCP
uses a [protected connection profile](coap-plain-tcp.md). OSCORE and remote
constrained-device acceptance remain pending. The full
72-entry catalogue remains the implementation objective.

## Acceptance

Current signed and encrypted qualification uses certificate-verified TLS 1.3,
exact `coap` ALPN and the initial CSM exchange. The client advertises and enforces
a 1152-byte maximum wire frame with blockwise support. Two multiline Unicode
sends reach durable accepted receipts; exact-intent replay keeps the native IDs
and one send attempt. Independent CHECK records verify IDs, authors, targets
and bodies. Consecutive one-message history pages match complete native metadata,
report `more` then `ok`, and retain nonempty cursors without gaps; the caught-up
resume returns an empty page.

The new qualification, whole large Block1/Block2 transfer and too-small peer
frame rejection pass twice under `go test -race` on Darwin;
`go vet ./integration/gateway` passes. These local Go-client checks do not
establish remote constrained-device acceptance.

The timings and broad operation counts below describe earlier source snapshots.
Current carrier matrices exercise send, scoped receipts and reads; optional
mutation semantics have separate shared backend proofs. Fresh final module
acceptance remains outstanding in [ROADMAP.md](ROADMAP.md).


The initial TLS/TCP combined pinned-native race group passed in 124.790 seconds,
covering all 33 shared operations in four DTLS and two TCP envelope modes,
whole large blockwise and Observe pages, heartbeat/cursor cancellation,
actual response loss with stable-ID recovery, TLS/envelope rejection,
pre-CSM intake fencing, token isolation, output capacity and joined shutdown.
Native history and receipts use the pinned fixture and independent native
records. That increment's focused carrier race group passed in 6.591 seconds,
including a cancelled contended writer and failed-initial-write private
reader cleanup. Its full stable-source normal and race module suites passed
(gateway integration 1079.520s/1098.830s). Vet, staticcheck, module
tidiness and vulnerability checks also pass; no affected vulnerability calls
were found.

This evidence uses local native Go clients. It does not establish remote
constrained-device acceptance or support for the pending profiles above.

Adaptive native checks pass in 26.023 seconds and cover 64-, 256-, 512- and
queryless 1100-byte peer frames, whole history and unchanged AGD1 Observe
pages, new live messages, quiet heartbeat and cancellation, CSM lowering during
history transfer, interrupted and unfetched notifications, foreign-association
query denial, and route admission with the same-intent positive control.
The final combined DTLS/TCP native race group passes in 135.845 seconds against
gateway SHA-256
`14b8e868618c56a115d5590f262bbcf372ea6e256949081738334af2ea270b2b`.
Vet, staticcheck, formatting and module tidiness pass. The first full normal
and race runs failed in existing QUIC replay and RPC receipt assertions
(integration 1084.900s/1091.991s). The replay now retains its signed validity
timestamps, and the RPC assertion waits for the journal receipt after native
broadcast. Ten focused race repetitions pass in 35.846s. Full normal and race
module checks then pass against the corrected frozen source (gateway
integration 1096.226s/1107.119s). The source manifest remains unchanged across
those runs; vet also passes after the test corrections.
