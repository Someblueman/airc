# Connection gateway inventory

Remaining work lives only in [ROADMAP.md](ROADMAP.md). This file is a concise implementation inventory, not a second task list or a running acceptance log.

## Recorded mode evidence

This inventory is an evidence map at `e774f7d`, not fresh certification of every
mode. Full normal acceptance passed at executable/test source `9b217be`;
full race acceptance at that source timed out before completion. Subsequent
gRPC capacity, active-read rebinding and scratch-example checks passed their
affected race runs. Exact results and limitations are in [testing.md](testing.md)
and the linked specifications. Mode qualification remains individually open in
the roadmap until its complete acceptance boundary is audited and verified.

The rows below distinguish modes even when they share an implementation. Local
evidence primarily uses real Go clients and private Darwin native fixtures with
independent AIRC observers. A compiled adapter or skipped opt-in probe is not
runtime evidence.

| Mode | Recorded local exercise / specification | Evidence limit |
|---|---|---|
| Framed TCP IPv4 | [Authenticated durable sends, independent observer, stable replay and bounded cursor pages](framed-ipv4-testing.md) | Loopback Darwin |
| Framed TCP IPv6 | [Authenticated sends, native observer and bounded cursor resume](framed-ipv6-testing.md) | Loopback Darwin |
| Framed TLS | [Verified CA/name, signed/encrypted sends, native observer and cursor pages](framed-tls-testing.md) | Local certificates and clients |
| HTTP/1.1 | [Authenticated sends, whole bounded pages and caught-up cursor resume](http1-testing.md) | Local TLS fixture |
| HTTP/2 | [Authenticated sends, exact native pages, cursor resume and revocation](http2-testing.md) | Local TLS fixture |
| HTTP/3 | [Authenticated sends, exact native pages, replay and cursor resume](http3-testing.md) | Go client, loopback QUIC |
| HTTP GET polling | [New native records after caught-up cursor, bounded pages and repeat polling](http-get-polling-testing.md) | Local HTTP/1.1 and HTTP/2 history |
| HTTP long polling | [Native wakeup, noise-bounded quiet deadline and joined cancellation](http-long-poll-testing.md) | Local HTTP/1.1 and HTTP/2 fixtures |
| HTTP POST | [Durable admission, exact accepted stable-ID retry and native observer](http-post-testing.md) | Local HTTP/1.1 and HTTP/2 TLS fixtures |
| HTTP PUT | [Stable accepted resource, cross-method replay and conflict preservation](http-put-testing.md) | Local HTTP/1.1 and HTTP/2 fixtures |
| SSE | [Events and cursor reconnection contract](events.md) | Local stream fixtures; submission uses HTTP |
| WebSocket | [Real client and macOS browser probe](websocket-v1.md) | Disposable local origin |
| WSS | [Native TLS/origin checks](websocket-v1.md) | Trusted remote browser certificate remains unverified |
| Darwin Unix stream | [Same-UID real client, permissions and lifecycle](unix-stream.md#verification-boundaries) | Foreign-UID denial probe was privilege-gated and skipped |
| Linux Unix stream | [Compiled platform credential implementation](unix-stream.md#verification-boundaries) | Native Linux runtime acceptance missing |
| Darwin Unix datagram | [Authenticated mailbox, replay and race fixtures](unix-datagram.md) | Owner-provisioned private paths |
| Linux Unix datagram | [Cross-build](unix-datagram.md) | Native Linux runtime acceptance missing |
| Framed stdio | [Owned anonymous-pipe conformance](stdio.md) | Darwin runtime; dedicated parent/child pipes |
| MCP Streamable HTTP | [Four-tool gateway and SDK fixtures](mcp-http.md) | Separate local `airc mcp` still has 19 tools; deployed callers/surface choice unresolved |
| JSON-RPC | [Real HTTP calls, durable IDs and streamed reads](jsonrpc.md) | Local fixtures; RPC correlation is separate from durable identity |
| gRPC | [Generated client; authentication, replay, reads and held-read send](capacity-reservation-testing.md) | Eleven affected real gRPC tests passed race; native gRPC, no gRPC-Web |
| WebTransport bidirectional | [Native streams and pinned-leaf Chrome probe](webtransport.md) | Trusted-certificate browser acceptance remains open |
| WebTransport paired unidirectional | [Separate native and Chrome uni paths](webtransport.md) | Trusted-certificate browser acceptance remains open |
| WebTransport datagrams | [Fragment/reorder/loss fixtures and Chrome datagrams](webtransport.md) | Browser lossy-network acceptance unverified |
| Raw QUIC bidirectional | [Real native stream client](quic.md) | Local QUIC endpoint |
| Raw QUIC paired unidirectional | [Separate native uni client](quic.md) | Local QUIC endpoint |
| Raw QUIC datagrams | [Fragmentation, replay and loss boundaries](quic.md) | Local packet fixtures; no reliable-delivery promise |
| UDP unicast | [Encrypted mailbox and actual packet-loss/replay checks](udp.md) | Provisioned return route; no multicast/broadcast qualification |
| DTLS datagrams | [Certificate-verified ECDSA native client](dtls.md) | RSA interoperability and remote PMTU unverified |
| WebRTC reliable ordered | [Distinct native DataChannel mode](webrtc.md) | Loopback; remote NAT/relay path missing |
| WebRTC reliable unordered | [Distinct native DataChannel mode](webrtc.md) | Loopback; remote NAT/relay path missing |
| WebRTC unreliable ordered | [Distinct native DataChannel mode and loss semantics](webrtc.md) | Loopback; remote NAT/relay path missing |
| WebRTC unreliable unordered | [Distinct native DataChannel mode and loss semantics](webrtc.md) | Loopback; remote NAT/relay path missing |
| CoAP over DTLS confirmable | [Signed/encrypted real CoAPS client](coap.md) | Local Go client; remote constrained-device evidence missing |
| CoAP over DTLS non-confirmable | [Separate signed/encrypted non-confirmable mode](coap.md) | Transport acknowledgement cannot establish AIRC acceptance |
| CoAP over TLS/TCP | [Negotiated bounds, signed/encrypted native client](coap-tcp.md) | Local Go client; remote constrained-device evidence missing |

Shared authentication/isolation, durable restart, retry uncertainty, receipt
recovery and bounded reads are recorded in
[baseline authentication](baseline-authentication-testing.md),
[restart](durable-restart-testing.md), [retry](retry-boundary-testing.md),
[receipts](receipt-recovery-testing.md) and [history](bounded-history-testing.md).
[Active-read rebinding](active-read-rebinding-testing.md) and the runnable
[scratch example](scratch-example.md) add actual process/client evidence.
Optional operations beyond message submission and reading do not require other
connectors to implement full native-operation parity.

The remaining blockers are explicit in the roadmap: native Linux/Windows and
other platform runs where required; actual trusted-certificate browser checks;
a real remote WebRTC NAT/relay path; physical devices, buses, removable media
and carrier networks; existing transfer/broker/chat services and their authority
configuration; and owner-selected targets for open-ended APIs/products. Plain
CoAP and OSCORE remain unfinished gateway profiles; the OSCORE prototype is not
a supported adapter. New mailbox, broker, IPC, packet, physical and overlay
entries are unfinished unless separately qualified. No full catalogue or final
acceptance claim follows from this table.

The unfinished WHO/WHOIS increment has been removed from this branch and preserved in a verified private recovery archive under the common Git directory. Test-speed work is part of the roadmap. The branch has no configured upstream; previous milestone notes report no installation or deployment. Keep the live service outside test fixtures.

The previous chronological acceptance record remains in Git at `28d255e:docs/gateway/STATUS.md`. Earlier passes apply to their recorded source versions; they do not certify a subsequently modified tree. Future verification belongs with the relevant transport specification and commit, not an expanding master log.
