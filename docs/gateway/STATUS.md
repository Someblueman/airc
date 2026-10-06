# Connection gateway inventory

Remaining work lives only in [ROADMAP.md](ROADMAP.md). This file is a concise implementation inventory, not a second task list or a running acceptance log.

## Recorded mode evidence

This inventory maps source-specific evidence in the linked specifications; it
is not fresh certification of every mode. Full normal acceptance passed at executable/test source `9b217be`;
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
| SSE | [HTTP-submitted native records, bounded reconnect pages and lease expiry](http-sse-testing.md) | Local stream fixtures; submission uses HTTP |
| WebSocket | [Encrypted send, disconnect retry, native observer and bounded cursor read](websocket-testing.md) | Local Darwin Go client; browser probe in [specification](websocket-v1.md) |
| WSS | [Certificate/origin rejection, signed/encrypted send, retry and cursor read](wss-testing.md) | Local Go clients; trusted remote browser certificate remains unverified |
| Darwin Unix stream | [Owner-only socket, encrypted sends, native observer and bounded cursor pages](darwin-unix-stream-testing.md) | Foreign-UID denial probe was privilege-gated and skipped |
| Linux Unix stream | [Compiled platform credential implementation](unix-stream.md#verification-boundaries) | Native Linux runtime acceptance missing |
| Darwin Unix datagram | [Loss/reconnect replay, native observer and bounded mailbox cursor read](darwin-unixgram-testing.md) | Owner-provisioned private paths |
| Linux Unix datagram | [Cross-build](unix-datagram.md) | Native Linux runtime acceptance missing |
| Framed stdio | [Authenticated sends, native observer, bounded pages and EOF joining](stdio-testing.md) | Darwin runtime; dedicated parent/child pipes |
| File mailbox | [Signed/encrypted native acceptance, clean command replay and complete cursor pages](file-mailbox.md) | Darwin one-shot CLI with refusal/size, pending SIGKILL replay and joined signals; Linux runtime qualification remains open |
| MCP Streamable HTTP | [Four-tool SDK network acceptance, native sends and bounded pages](mcp-http-testing.md) | Loopback standalone gateway; separate local `airc mcp` surface choice unresolved |
| JSON-RPC | [Durable retry identity, native observer and bounded correlated pages](json-rpc-testing.md) | Local fixtures; RPC correlation is separate from durable identity |
| gRPC | [Generated client, native send/replay and exact bounded cursor pages](grpc-testing.md) | Local Darwin native gRPC; no gRPC-Web |
| WebTransport bidirectional | [Authenticated native send/replay and bounded cursor pages](webtransport-bidi-testing.md) | Local Go client; trusted-certificate browser acceptance remains open |
| WebTransport paired unidirectional | [Paired send/receipt/replay and bounded cursor reads](webtransport-uni-testing.md) | Local Go client; trusted-certificate browser acceptance remains open |
| WebTransport datagrams | [Discarded-admission retry, native send and bounded cursor reads](webtransport-datagram-testing.md) | Local Go client; browser lossy-network acceptance unverified |
| Raw QUIC bidirectional | [Authenticated native send/replay and exact bounded cursor pages](quic-bidi-testing.md) | Local Darwin QUIC endpoint |
| Raw QUIC paired unidirectional | [Paired authenticated sends, native observer and bounded cursor reads](quic-uni-testing.md) | Local Darwin QUIC endpoint |
| Raw QUIC datagrams | [Native send/replay, bounded cursor pages and incomplete-fragment denial](quic-datagram-testing.md) | Local packet fixtures; no reliable-delivery promise |
| UDP unicast | [Actual loss/reconnect replay and exact bounded encrypted mailbox pages](udp-testing.md) | Provisioned return route; no multicast/broadcast qualification |
| Plain CoAP TCP | [Encrypted send, protected results, native observer, reconnect and cursor pages](coap-plain-tcp.md) | Local native client; no OSCORE or remote constrained-device claim |
| Plain CoAP UDP | [Encrypted NON send, native observer, replay and protected cursor mailbox](coap-udp.md) | Local native client; no OSCORE or remote constrained-device claim |
| DTLS datagrams | [Certificate-verified authenticated send, replay and bounded cursor reads](dtls.md#acceptance) | RSA interoperability and remote PMTU unverified |
| WebRTC reliable ordered | [Authenticated send and bounded cursor reads](webrtc.md#native-mode-qualification) | Loopback; remote NAT/relay path missing |
| WebRTC reliable unordered | [Authenticated send and bounded cursor reads](webrtc.md#native-mode-qualification) | Loopback; remote NAT/relay path missing |
| WebRTC unreliable ordered | [Authenticated send, bounded reads and uncertain-result recovery](webrtc.md#native-mode-qualification) | Loopback; remote NAT/relay path missing |
| WebRTC unreliable unordered | [Authenticated send, bounded reads and uncertain-result recovery](webrtc.md#native-mode-qualification) | Loopback; remote NAT/relay path missing |
| CoAP over DTLS confirmable | [Signed/encrypted accepted send, replay and bounded cursor reads](coap.md#acceptance) | Local Go client; remote constrained-device evidence missing |
| CoAP over DTLS non-confirmable | [Signed/encrypted NON native acceptance and replay](coap.md#acceptance) | Transport acknowledgement cannot establish AIRC acceptance |
| CoAP over TLS/TCP | [Negotiated bounds, accepted send and cursor reads](coap-tcp.md#acceptance) | Local Go client; remote constrained-device evidence missing |

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
configuration; and owner-selected targets for open-ended APIs/products. OSCORE remains an unfinished gateway profile; the OSCORE prototype is not
a supported adapter. New mailbox, broker, IPC, packet, physical and overlay
entries are unfinished unless separately qualified. No full catalogue or final
acceptance claim follows from this table.

The unfinished WHO/WHOIS increment has been removed from this branch and preserved in a verified private recovery archive under the common Git directory. Test-speed work is part of the roadmap. The branch has no configured upstream; previous milestone notes report no installation or deployment. Keep the live service outside test fixtures.

The previous chronological acceptance record remains in Git at `28d255e:docs/gateway/STATUS.md`. Earlier passes apply to their recorded source versions; they do not certify a subsequently modified tree. Future verification belongs with the relevant transport specification and commit, not an expanding master log.
