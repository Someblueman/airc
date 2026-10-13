# Gateway cleanup: fixes, consolidation and benchmarks

Measured 13 October 2026 on the `gateway-cleanup` branch against `main`
(`4b6f1f7`). The work followed a review of every gateway carrier family. The
review found the carrier framework already well consolidated: one request loop
serves about ten stream carriers, one packet engine serves eleven, and
fragmentation and replay protection exist once. So this branch removes copies
rather than restructuring. Gateway source is 354 lines smaller net
(496 added, 845 removed, outside docs).

## Changes

| Change | Kind |
| --- | --- |
| A Redis stream read whose result is too large for the result stream fails permanently and is acknowledged. Before, it stayed pending and was re-dispatched and reported about once a second, forever | Bug fix |
| Framed responses: one buffer and one write, instead of `io.Copy` over an `io.MultiReader`, which allocated 32 KiB per response and sent two TLS records | Hot path |
| A streamed page frame is JSON-encoded once: the shared loop's budget check keeps its encoding for the transport. gRPC replies and watch frames also encode once | Hot path |
| DTLS reuses one read buffer per listener; a unixpacket connection reuses one 16 KiB record buffer | Hot path |
| `adapter.ServeAccepted` replaces nine copies of the accept loop: framed, SCTP, unixpacket, ZeroMQ/NNG, MQTT, AMQP, STOMP, TLS syslog, CoAP over TCP | Consolidation |
| One polled owner for the directory mailbox and append log | Consolidation |
| `soleListener`, `openListenerDispatch` and `waitDelivered` for the process carriers: UART, FIFO, stdio-style streams, file mailbox, offline imports | Consolidation |
| `quicFramedStream` is generic over the stream error-code type and replaces the two WebTransport copies | Consolidation |
| HTTP read routes come from one table (`readPaths`, `targetReads`) | Consolidation |
| `watchEndReason` (four copies), one AMQP/STOMP operation filter, and removal of unreachable branches in JSON-RPC and gRPC and of the unused `dispatch.Import` | Consolidation |
| Integration fixtures keep each reserved port's probe open until all are chosen. The benchmark runs hit the old race once ("duplicate enabled listener address") | Test fix |

Equivalence checks beyond the test suite:
- The HTTP read-route table was compared with the previous `readRequest` over 2.6 million path and query combinations, with identical requests, start values and errors.
- `encodeFrame` reuses an encoding only for the sequence number it was made with (`TestEncodeFrameReusesOnlyMatchingSequence`).

## Not done, and why

- **The four IPC queue carriers (POSIX, System V, shared memory, mapped file) share one owner and constructor skeleton.** Merging them is low risk, but this container has no `/dev/mqueue`, and shared memory and mapped file build only on arm64. Three of the four could not be tested here.
- **Smaller shared helpers** (origin allow-lists, the AMQP/STOMP/Redis admission step, a joined `AfterFunc`) were left for a follow-up. They are worth about 70 lines together.
- **Deliberately not merged**, because behaviour differs:
  - the broker routing engines (acknowledgement and flow control differ per protocol);
  - the HTTP, MCP, WebSocket and WebTransport origin/CORS handlers (status codes and headers differ);
  - the two mailbox publishers (crash recovery differs);
  - WebRTC's request loop (its rejections close the peer and no test covers them);
  - the per-carrier stream budgets.

## Tests

`go test -race -count=1 ./...` passed except tests this container cannot run, which fail the same way on `main`: IPv6 sockets, POSIX message queues and UDP-Lite over IPv6. No data races. `go vet` passes for linux/amd64, linux/arm64 and darwin/arm64. `staticcheck` is clean. For windows/amd64, `internal/gateway/auth`'s tests fail to build on `main` too.

## Benchmarks

Same host as the earlier studies (4-vCPU Xeon, Go 1.27.0); another agent was running tests at the same time, and load averages reached about 3 ([progress.txt](progress.txt)). [suite-latency-harness.go.txt](suite-latency-harness.go.txt) runs in each tree's `integration/gateway` package. It makes sequential calls over the carrier suite's own clients for HTTP, WebSocket, framed, gRPC, MCP and JSON-RPC: 300 capabilities calls, 30 guest sends and 100 history reads of 50 messages. The earlier gateway send harness ran alongside it ([round.sh.txt](round.sh.txt)). There were 15 interleaved rounds, then 10 more ([rerun-*](rerun-suite-benchstat.txt)).

- **Framed capabilities calls:** 386 → 351 µs, −9.0% (p=0.041), p90 −12.1% (p=0.005). The rerun reproduced it: −9.8% (p=0.029), p90 −15.4% (p=0.000). This is the single-write response.
- **JSON-RPC send:** p50 −7.6% (p=0.010), which did not reproduce (p=0.089).
- **Everything else unchanged.** Every other carrier and operation shows no significant difference ([suite-benchstat.txt](suite-benchstat.txt)).
- **Gateway HTTP send** ([send-benchstat.txt](send-benchstat.txt)): throughput, admission, observation and gap times unchanged at c8 and c1. The c1 admit p90 was −10.0% (p=0.016) in the first pass, then unchanged in the rerun. No send failed.

`aircd`, `airc` and the load generator were not rerun: this branch changes no code they link.
