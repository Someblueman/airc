# Raw QUIC streams and datagrams

Plain author requests need no principal, bearer or key on this carrier; see
[author network carriers](author-network-carriers.md). The rest of this page
describes the legacy signed/encrypted profile where it differs.

Configure a dedicated UDP listener with `protocol: "rawquic"` for reliable
streams, or `protocol: "rawquic_datagrams"` for streams plus the negotiated
datagram extension. Both require a TLS certificate and key, including on
loopback. The ready record reports the actual UDP address. These listeners use
TLS 1.3 and ALPN `airc-gateway-quic/1`, with pinned quic-go v0.63.0. They do not
accept HTTP/3 or WebTransport sessions. Only completed handshakes are served;
0-RTT is disabled.

The datagram listener requires peer support for the QUIC datagram extension.
Datagrams remain unreliable: successful transport setup or local enqueue does
not prove AIRC acceptance. See the [quic-go datagram contract](https://quic-go.net/docs/quic/datagrams/).

## Application requests

Every request carries the shared signed or encrypted envelope. Session setup
does not grant AIRC authority; current principal, operation and target
permissions are checked independently for each request. There is no connection
bearer credential or raw AIRC command escape hatch.

Bidirectional streams carry the existing four-byte big-endian length-prefixed
[FrameRequest/FrameResponse contract](framed-stream-v1.md) without its TCP
handshake. An incoming unidirectional request stream receives responses on a
server-initiated unidirectional stream. Associate responses by correlation ID,
not stream arrival order. Correlation uniqueness applies to each reliable
stream or the connection's datagram channel. Ordinary request FIN drains active
responses. Cancel the response direction to abandon a quiet watch; its native
lease must end while the connection remains usable.

The application contract carries the current 32 shared operations, including
receipt lookup, replies, history, search and live `watch`/`events.watch` leases.
Capabilities identify `quic_stream`, `quic_uni_stream` or `quic_datagrams`
alongside `long_poll`. This does not claim the remaining native AIRC operations
are implemented.

Datagrams use the same AGD1 fragment layout and per-correlation response
sequence as [WebTransport datagrams](webtransport.md#datagram-frames).
The raw QUIC carrier adds no HTTP datagram or WebTransport session prefix.
Requests are at most 16 KiB, responses 64 KiB, fragments 1000 bytes. Four
incomplete assemblies per connection expire after five seconds; completed
frame IDs are suppressed for thirty seconds in a cache of 64 entries.
Contiguous sequences are required before delivering pages or advancing opaque
cursors. Loss leaves an uncertain result and the last contiguous cursor.
Malformed complete requests close the connection. To retry an uncertain
mutation, retain its original request ID and exact authenticated intent, using
a new correlation and transport frame ID. Receipt lookup reports durable native
acceptance; the shared bounded retry contract accepts possible duplicate posts.

## Bounds and lifecycle

Connection admission uses shared service slots before handshake processing.
Handshake timeout is five seconds; idle timeout is thirty seconds. Receive
windows are capped at 64 KiB for each stream and connection, and each connection
permits four incoming bidi and four incoming uni streams. Stream and request
admission have finite shared limits. Each correlated channel admits four active
requests; native per-principal lease quotas still apply.

Ordinary dispatch is bounded at four seconds, watch dispatch at twenty seconds
(the current native read lease ends at fifteen seconds), and synchronous writes
at three seconds. Watches allow at most 128 pages and one MiB including framing,
with a terminal reserve. A blocked datagram enqueue closes the QUIC connection
at the write bound. Shutdown stops admission, closes accepted connections,
joins all stream/datagram workers and admission callbacks, then releases the UDP
socket. The [connection lifecycle documentation](https://quic-go.net/docs/quic/connection/)
describes how connection close interrupts blocked QUIC operations.

## Acceptance

Real native clients verify all current 32 operations over each carrier, whole
native records and independent mutation facts, stable replay and receipts,
live/quiet watches, FIN/STOP_SENDING cancellation, fixed-window slow readers,
TLS/ALPN/datagram negotiation failures and active-watch process shutdown. The
WebTransport native and real Chrome probes also pass after the shared AGD1
extraction and stream-close safeguard. Full normal and race module gates pass;
exact results are recorded in [STATUS.md](STATUS.md). Other platforms, remaining
native AIRC operations and the full catalogue remain unfinished.

```sh
go test -race ./integration/gateway -run '^TestRawQUIC' -count=1
go test -race ./internal/gateway/service -run 'RawQUIC' -count=1
```
