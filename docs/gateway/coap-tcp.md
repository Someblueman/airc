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
responses use 1024-byte blocks for peers advertising blockwise support and
a receive frame of at least 1152 bytes. A peer without blockwise support needs
room for the complete encoded response. Smaller peer limits are accepted, but
an output that exceeds them closes the carrier before writing application
bytes. Adaptive smaller output blocks remain pending.
Observe carries bounded AGD1 fragments with per-fragment freshness and a
separate sequence for each complete FrameResponse. Observe=1 or connection
close cancels a subscription. An interrupted or timed-out write closes the
connection, including when cancellation interrupts a partially written stream.

The initial peer CSM has a five-second deadline. Unknown critical CSM options,
invalid lengths and repeated non-repeatable capability options close the
connection before native work. Ordinary and Observe operations share the
existing four-operation admission cap, 16-entry receive queue, 256-token
lifetime budget and three-second blockwise expiry. Only successful whole
authenticated Content writes renew the thirty-second idle timer; signals and
partial Block1 acknowledgements do not. Connections also expire after two
minutes. Physical writes and time spent waiting for the dependency writer are
bounded to three seconds. The service joins accepted connections and all
adapter-owned handlers, expiry and watch workers. The dependency queue stops
through Session.Done with a closed handler gate preventing late native work.
Sending blockwise caches may retain their bodies until the association
deadline; the lifetime token budget bounds that state.

This profile is an additional part of catalogue row 25. Plain UDP/TCP CoAP,
OSCORE and remote constrained-device acceptance remain pending. The full
72-entry catalogue remains the implementation objective.

## Acceptance

The combined DTLS/TCP pinned-native race group passes in 124.790 seconds,
covering all 33 shared operations in four DTLS and two TCP envelope modes,
whole large blockwise and Observe pages, heartbeat/cursor cancellation,
actual response loss with stable-ID recovery, TLS/envelope rejection,
pre-CSM intake fencing, token isolation, output capacity and joined shutdown.
Native history and receipts use the pinned fixture and independent native
records. The final focused carrier race group passes in 6.591 seconds,
including a cancelled contended writer and failed-initial-write private
reader cleanup. Full stable-source normal and race module suites pass
(gateway integration 1079.520s/1098.830s). Vet, staticcheck, module
tidiness and vulnerability checks also pass; no affected vulnerability calls
were found.

This evidence uses local native Go clients. It does not establish remote
constrained-device acceptance or support for the pending profiles above.
