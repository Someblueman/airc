# WebTransport over HTTP/3

Configure `protocol: "webtransport"` with an explicit UDP address and TLS
certificate/key, including on loopback. The ready record
reports the bound UDP address. This dedicated listener uses TLS 1.3, `h3`,
QUIC datagrams and partial stream reset. It serves only completed handshakes;
0-RTT is disabled. It does not serve the ordinary HTTP endpoint tree.

The implementation pins `webtransport-go v0.13.0` (HTTP/3 draft-16),
`quic-go v0.63.0` and `httpsfv v1.1.1`. Native interoperability is tested with
these exact versions; browser interoperability requires its own evidence.
See the [upstream release](https://github.com/quic-go/webtransport-go/releases/tag/v0.13.0)
and [WebTransport API specification](https://www.w3.org/TR/webtransport/).

## Session and authors

Connect to `/v1/webtransport` and offer application protocol `airc-gateway.v1`.
The offer is a structured-field list of quoted strings in
`WT-Available-Protocols`. Query strings, Authorization, Idempotency-Key,
X-Forwarded-Host and X-Original-Host headers are rejected. Browser Origin must
exactly match a configured `allowed_origins` entry; native clients may omit
Origin. Origin and session setup never select an author. Every application
request is a plain contract request naming its author, as on every
[network carrier](author-network-carriers.md); the WebTransport session is one
author session, and an `account.identify` request sets its default author.
The listener access policy is checked for each request.

## Reliable streams

Bidirectional streams carry the existing four-byte big-endian length-prefixed
[FrameRequest/FrameResponse contract](framed-stream-v1.md), without its TCP
handshake. A client-initiated unidirectional request stream receives responses
on a server-initiated unidirectional stream; correlation IDs associate requests
with responses. Correlation uniqueness is scoped to each reliable stream or the
session datagram channel, not across all session streams. Do not rely on outgoing
stream arrival order. Both modes carry
all current 32 operations and bounded live `watch`/`events.watch` leases.
Capabilities report `webtransport_stream` or `webtransport_uni_stream` beside
`long_poll`. An orderly request FIN drains outstanding responses. Cancel both
stream directions when abandoning a bidirectional operation; close the session
to abandon a datagram watch. Cancel the unidirectional response stream to stop
its native lease while retaining the session.

## Datagram frames

Each datagram is at most 1000 bytes and carries one
[fragment](author-network-carriers.md#fragments). Its 28-byte header contains
the wire tag `AGD1`,
a fresh random 16-byte frame ID, the complete JSON body length (big-endian u32),
fragment index (u16, starting at zero), and fragment count (u16). Payload chunks
are 972 bytes except the last. Count must equal the ceiling of body length/972;
each fragment must have its exact expected payload length. The assembled body
is a FrameRequest or FrameResponse JSON object, with no stream length prefix.
Requests are capped at 16 KiB and responses at 64 KiB.

Assemblies accept out-of-order and identical duplicate fragments. Conflicting
fragments invalidate their assembly. At most four incomplete frames are held
per session, each for five seconds from its first fragment. Completed frame
IDs are suppressed for thirty seconds in a cache capped at 64 entries. These
transport frame IDs are separate from durable mutation IDs.

Datagram responses include `sequence`, starting at one per correlation ID and
increasing across pages and the terminal frame. A watch client must deliver only
contiguous sequences, preserve its last contiguous opaque cursor, and report
uncertainty on loss. Later pages must not advance a cursor over a missing page.
Datagrams provide no delivery guarantee or retransmission. A malformed complete
request closes its session, including concurrent reliable streams. Retry an uncertain
mutation with its original request ID and exact intent, using a
fresh correlation and transport frame ID. Receipt lookup reports durable native
acceptance; retries can produce duplicate native posts under the shared delivery
contract. Datagram capabilities report `webtransport_datagrams` and `long_poll`.

## Bounds and ownership

Service connection slots are acquired before QUIC handshake processing.
Handshake/setup bounds are five seconds and idle timeout thirty seconds.
Headers are capped at 16 KiB; QUIC receive windows and WebTransport session
incoming data are capped at 64 KiB. Each session advertises at most four incoming
bidirectional and four incoming unidirectional streams. Shared stream, session
and request admission remain finite. Each correlated channel admits four active
requests; per-author native lease quotas remain unchanged.

Ordinary dispatch is bounded at four seconds, watch dispatch at twenty seconds
(the current native read lease ends at fifteen seconds),
and synchronous writes at three seconds. Stream watches allow at most 128 pages
and one MiB including their JSON sequence fields, with terminal reserve. A
blocked datagram send closes its QUIC connection at the write bound, because
closing only the session cannot interrupt the underlying datagram queue.
Shutdown stops admission, closes sessions/connections, joins handlers and
serving workers, then releases the UDP socket. These bounds do not fabricate
terminal frames or native acceptance when a client disappears.

## Verified modes

A browser client uses the same frames: send JSON requests naming their author
over a bidirectional stream, a unidirectional stream or datagrams, match
responses by correlation ID, deliver only contiguous sequences, and reconnect
explicitly to renew or retry.

Dated records follow. Where they mention principals, bindings, signed or
encrypted envelopes, signing keys or return routes, they describe the
[removed identity model](removed-identity.md), which no longer exists.

Chrome 154 on macOS is verified with a disposable loopback Origin and a pinned
short-lived P-256 leaf certificate, without installed trust or browser flags.
The probe exercises signed capability discovery, encrypted sends/replies,
receipt acceptance, Unicode history, bidi live watch cancellation, uni calls
and watches with session reuse and quiet AbortSignal cancellation, fragmented
datagram calls and watches, session
cancellation and reconnect. An independent native observer and CHECK query
verify exactly four messages and reply linkage. This is not evidence for other
browser engines, remote deployment, or lossy network behavior in Chrome.
Native clients separately exercise packet fragment duplication/reordering,
incomplete request non-dispatch, quiet leases, fixed-window slow readers and
active-watch process shutdown. Full catalogue evidence is tracked in [ROADMAP.md](ROADMAP.md). Additional
native operations are optional.

Reproduce the interactive pinned-certificate probe by opening its printed owned URL:

```sh
AGW_BROWSER_PROBE=1 go test -race ./integration/gateway \
  -run '^TestBrowserWebTransportCryptoAndDatagrams$' -count=1 -v
go test -race ./integration/gateway -run '^TestWebTransport' -count=1
```

## Browser acceptance with ordinary certificate trust

On 2026-10-09, Chrome 154.0.8037.99 on Darwin ARM64 accepted an isolated
mkcert leaf for `127.0.0.1` over ordinary HTTPS/TCP using the existing system
SSL-trusted root. The same leaf failed the actual WebTransport handshake with
`Opening handshake failed`; the race-instrumented browser test exited 1 before
native observer assertions ran. No browser settings or root trust changed.

Chromium documents that local trust additions apply to TCP, while QUIC
considers local trust decisions only for removing trust
([Chrome Root Store FAQ](https://chromium.googlesource.com/chromium/src/+show/main/net/data/ssl/chrome_root_store/faq.md)).
This explains why the local root cannot qualify ordinary WebTransport trust
in this browser. The proven pinned-certificate fixture remains unchanged;
ordinary-trust qualification requires an authorized endpoint and certificate
chain accepted by Chrome for QUIC. The unsuccessful experimental patch is
retained locally at `/tmp/airc-browser-trust-probe.patch`.
