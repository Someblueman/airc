# HTTP/3 over QUIC

Plain author requests need no principal, bearer or key on this carrier; see
[author network carriers](author-network-carriers.md). The rest of this page
describes the legacy signed/encrypted profile where it differs.

The `http3` listener uses a dedicated UDP socket, TLS 1.3 and `h3` ALPN.
Certificates are mandatory, including on loopback. The HTTP handler tree and
its operation, target and credential checks are shared with the HTTP listener:
classic gateway endpoints, JSON-RPC, bearer authentication, signed and encrypted
profiles, and bounded SSE streams use their existing contracts.

The pinned QUIC implementation is `quic-go v0.63.0` (QPACK v0.6.0).
Shared service connection slots are acquired before handshake processing and
released on failed handshakes or closed connections. Only handshake-completed
connections are served, and QUIC 0-RTT is disabled.
Requests have explicit five-second read and ordinary write deadlines; streaming
handlers set their existing three-second synchronous write deadlines. An outer
23-second request context bounds handler lifetime. QUIC handshake and idle
bounds are five and thirty seconds. Request headers are capped at 16 KiB;
existing gateway envelope, response, page and aggregate stream budgets apply.
Multiplexed streams do not increase principal permissions or native lease quotas.

Mutations retain durable request IDs and bounded retry semantics. An uncertain
retry must reuse the original ID and intent; receipt lookup reports subsequent
native acceptance and possible duplicates. Watch renewal uses the last delivered
opaque cursor and authenticates again. Cancellation can prevent a terminal
frame from arriving.

Configure `protocol: "http3"` with an explicit address, TLS certificate/key and
operation allowlist. The ready record reports the bound UDP address. This
listener does not advertise TCP fallback, WebTransport, or WebSockets over
HTTP/3. Those remain separate catalogue entries. Current verified capabilities
and remaining work are recorded in [STATUS.md](STATUS.md).
