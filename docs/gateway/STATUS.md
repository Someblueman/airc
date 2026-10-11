# Connection gateway inventory

Remaining work lives only in [ROADMAP.md](ROADMAP.md). The [catalogue reconciliation](catalogue-evidence.md) maps qualified submodes and exact unresolved evidence boundaries. This file is a concise implementation inventory, not a second task list or a running acceptance log.

## Recorded mode evidence

The latest [CLI and server refactor checkpoint](testing.md#cli-and-server-refactor-acceptance-2026-10-10)
records full normal and race passes on the combined main source, clean build,
vet and Darwin/Linux lint, and a repaired buffered-reply regression. It retains
the preceding gateway work and its qualification boundaries.

The [2026-10-10 main consolidation checkpoint](testing.md#main-consolidation-acceptance-2026-10-10)
records full normal and race passes on the merged source, build and vet passes,
clean Darwin ARM64 and Linux AMD64 staticcheck, parser fuzzing, and measured
history improvements. Linux lint is cross-platform analysis; this checkpoint
adds no new VPS runtime evidence. The preceding
[simplification and VPS checkpoint](testing.md#simplification-and-vps-acceptance-2026-10-10)
records historical remote acceptance and a laptop-to-VPS HTTPS
send/history/replay check through SSH. Exact source identities and limits are
recorded with each checkpoint.

Kafka ingestion has been removed; its historical journal migration and evidence
remain preserved. Connection modes that were never run against real software,
devices or a built native toolchain have also been [removed](removed-modes.md),
including NATS; the CouchDB checkpoint table remains in the journal schema.
The software checks exercise the available fixtures, including Linux multicast
and queue boundaries. They do not certify every catalogue mode.
The linked mode specifications retain their original platform, external-client,
browser, native-worker and physical-device qualification limits. Remaining mode
qualification lives in the roadmap.

The rows below distinguish modes even when they share an implementation. Local
evidence primarily uses real Go clients and private Darwin native fixtures with
independent AIRC observers. A compiled adapter or skipped opt-in probe is not
runtime evidence.

| Mode | Recorded local exercise / specification | Evidence limit |
|---|---|---|
| LoRaWAN unconfirmed uplinks | [ChirpStack 4 HTTP JSON integration](chirpstack.md) | Fixed device-mapped Send implemented; radio, provider and native AIRC testing deferred |
| OpenSSH SFTP mailbox | [Pinned SSH account mailbox and whole encrypted results](sftp.md) | Authenticated Send and bounded reads implemented; testing deferred, no OpenSSH or native AIRC qualification |
| OpenSSH SCP upload inbox | [Forced-command UID mapping and durable acknowledgement](scp-inbox.md) | Encrypted signed Send implemented; testing deferred, no OpenSSH or native AIRC qualification |
| PostgreSQL notification ingress | [Certificate-authenticated LISTEN/NOTIFY posting plain requests as the route principal](postgres-notify.md) | Ephemeral plain-JSON Send subscription; anyone who can NOTIFY the channel posts as that principal. Unit tests and one local PostgreSQL 16 to native AIRC run; no PostgreSQL 18, remote or loss qualification |
| Matrix bridge | [Visible-history recovery and encrypted whole replies](matrix-bridge.md) | Service ownership, durable Send and bounded authenticated reads implemented; testing deferred, no real-homeserver or native AIRC qualification |
| XMPP bridge | [Signed MUC requests and encrypted whole replies](xmpp-bridge.md) | Service ownership, signed admission and bounded encrypted replies implemented; testing deferred, no real-server or native AIRC qualification |
| IRC bridge | [Account-mapped sends and encrypted bounded reads](irc-bridge.md) | Prior sends have scripted native evidence; encrypted replies are compiled with testing deferred; real IRC server/client qualification remains open |
| Framed TCP IPv4 | [Authenticated durable sends, independent observer, stable replay and bounded cursor pages](framed-ipv4-testing.md) | Loopback Darwin |
| Framed TCP IPv6 | [Authenticated sends, native observer and bounded cursor resume](framed-ipv6-testing.md) | Loopback Darwin |
| Framed TLS | [Verified CA/name, signed/encrypted sends, native observer and cursor pages](framed-tls-testing.md) | Local certificates and clients |
| MQTT 3.1.1 TLS | [Bearer-authenticated publisher/topic authority over plain requests, durable PUBACK, exact native delivery, lost first acknowledgement/reconnect replay and joined shutdown](mqtt.md) | Publish-only QoS 1 with fixed literal grants; plain-request service tests and a local `mosquitto_pub` run on Linux AMD64, earlier signed-profile runs on Darwin and Linux ARM64; no general subscription-broker claim |
| MQTT-SN 1.2 connectionless ingestion | [Encrypted signed publisher/topic authority, deliberate pre-intake loss, native delivery, durable replay and joined shutdown](mqtt-sn.md) | QoS -1 predefined topics, encrypted signed sends and no success ACK; Darwin race and Linux ARM64 loopback clients, no Linux race instrumentation or physical radio claim |
| AMQP 1.0 TLS | [Plain JSON requests as the SASL principal on fixed links, durable dispositions, plain results, whole bounded reads, loss/restart replay and native observer](amqp.md) | Darwin full normal/race acceptance and Linux ARM64 selected profile checks passed for the earlier signed model; plain model passed Linux AMD64 suites and a real Qpid Proton client; no general-broker or Linux race claim |
| STOMP 1.2 TLS | [Plain JSON requests as the login principal on fixed destinations, durable receipts, individual ACK retention, plain results, whole bounded reads, loss/restart replay and native observer](stomp.md) | Darwin full normal/race acceptance and Linux ARM64 selected profile checks passed for the earlier signed model; plain model passed Linux AMD64 suites and a real stomp.py client; no general-broker or Linux race claim |
| Redis Pub/Sub TLS | [Plain contract requests posted as the route principal, plain JSON results, real Redis 7.0.15 ACL run with native observer](redis-pubsub.md#real-redis-evidence-2026-10-10) | Anyone allowed to publish on a request channel acts as its route principal; at-most-once Pub/Sub loss before intake; loopback single-host run, no cluster or sharded Pub/Sub |
| Redis Streams TLS | [Auto-ID plain requests posted as the route principal, request-ID dedupe, publish-before-ack and real Redis 7.0.15 ACL run with native observer](redis-streams.md#real-redis-evidence-2026-10-10) | Anyone allowed to XADD to a request stream acts as its route principal; refused entries stay pending for operator removal; loopback single-host run, no cluster |
| TLS syslog | [Pinned sender grants, exact native posts, retained record replay, refusals and joined shutdown](syslog.md#service-native-integration-evidence) | Darwin full normal/race acceptance and Linux ARM64 selected native controls; send-only, no syslog ACK, signed-origin freshness or Linux race claim |
| Signed syslog | [Verified origin coverage, atomic fresh admission, native replay/refusals and joined shutdown](signed-syslog.md#service-native-integration-evidence) | Selected RFC 5848 SHA-256/DSA SG0 profile over pinned TLS; Darwin full normal/race acceptance and Linux ARM64 native controls; send-only, no syslog ACK, general RFC interoperability or Linux race claim |
| SNMPv3 traps | [Exact engine/user and context grants, bounded field mapping, atomic timeliness/replay, native loss/restart/refusals and joined shutdown](snmp-v3.md#service-native-integration-evidence) | Selected authNoPriv USM trap profile; Darwin full normal/race and real Net-SNMP client, Linux ARM64 independent UDP/native controls; unavailable Linux Net-SNMP skipped, no trap ACK, privacy/informs/general-management or Linux race claim |
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
| Linux Unix stream | [Encrypted native sends, complete cursor pages, real example CLI and peer boundaries](linux-unix-stream-testing.md) | Linux local fixture; real foreign-UID child rejected; cross-compiled executables without race instrumentation |
| Darwin Unix datagram | [Loss/reconnect replay, native observer and bounded mailbox cursor read](darwin-unixgram-testing.md) | Owner-provisioned private paths |
| Linux Unix datagram | [Encrypted native delivery, complete mailbox reads and reconnect replay](linux-unixgram-testing.md) | Private fixed response mailbox, watch lifecycle and path/inode controls; cross-compiled fixtures without race instrumentation |
| Linux Unix sequenced-packet | [Encrypted native delivery, restart replay and complete bounded record cursor pages](unix-seqpacket.md#native-acceptance) | Owner-only record socket; exact-limit/refusal, peer and joined shutdown controls; Darwin unsupported; Linux cross-compiled fixtures without race instrumentation |
| GitHub inbound webhooks | [Signed issue-comment event importer](github-webhook.md) | Explicit provider identity/freshness and durable send admission implemented; testing deferred, no live GitHub or native AIRC qualification |
| Linux POSIX message queue | [Encrypted native sends, complete cursor reads, replay and uncertain-dequeue producer retry](posix-mqueue.md#native-queue-qualification) | Linux ARM64 real queues with journal ownership, fixed protected responses, pressure/authentication/replacement refusals and joined native watch shutdown; AMD64 compile-only; no Linux race instrumentation; same-UID stat/unlink namespace-race limitation documented |
| System V queues | [Protected native sends, whole cursor/history/watch replies and process-death recovery](system-v-queue.md) | Darwin/Linux ARM64; Darwin race, Linux actual native fixtures without race instrumentation; lifecycle, pressure and refusal controls; additional architectures unqualified |
| Mach IPC | [Kernel-audited caller mapping, protected native sends, whole cursor reads, retry and joined shutdown](mach-ipc.md#native-qualification) | Darwin ARM64 unary requests in a compatible bootstrap namespace; separate C client and native AIRC observer, full normal/race/static acceptance; no foreign-UID process, XPC, sandboxed-app or cross-login-session qualification |
| Embedded XPC bridge | [Same-UID caller mapping, native sends, complete cursor reads, retry and resource cleanup](xpc.md#native-qualification) | Darwin ARM64 app-bundled XPCService through the qualified Mach listener; actual C client/service and independent AIRC observer, full normal/race/static acceptance; bridge PID is diagnostic, no foreign-UID, sandboxed-client, installed-system-service or cross-login qualification |
| Private D-Bus peer bridge | [Same-UID caller mapping, native sends, complete cursor reads, retry and joined shutdown](dbus.md#native-qualification) | Darwin ARM64 GIO peer connections over an owner-only Unix socket through the qualified Mach listener; actual C client/server and independent AIRC observer, full normal/race/static acceptance; fixed authentication and connection deadlines; no shared-bus, foreign-UID or Linux qualification |
| Framed stdio | [Authenticated sends, native observer, bounded pages and EOF joining](stdio-testing.md) | Darwin runtime; dedicated parent/child pipes |
| BPv7 signed-send file ingress | [Exact signed metadata binding, delayed native acceptance and durable replay/recovery](bundle-protocol.md) | Darwin race and Linux ARM64 native fixtures; bounded one-way RFC 9171 profile, whole-second validity, no router/BPSec/remote receipts; no Linux race instrumentation |
| Paired POSIX FIFO | [Signed/encrypted native delivery and complete bounded cursor pages](fifo.md) | Darwin and Linux single active sender; same-session/fresh-child replay, authentication refusals, joined EOF/signals, frame/peer failure and pending termination recovery verified; Linux uses cross-compiled native Go fixtures without race instrumentation |
| Parent-owned socket pair | [Signed/encrypted native acceptance and complete bounded cursor pages](socket-pair.md) | Darwin and Linux unnamed Unix stream endpoint on FD 3; stable same-session/fresh-child replay, joined EOF/signals, frame failure and pending termination recovery; Linux uses cross-compiled native fixtures without race instrumentation |
| Inherited Unix-stream FD | [Signed/encrypted named-endpoint native delivery and complete bounded cursor pages](inherited-fd.md) | Darwin and Linux parent-established named connection on FD 3; same-session/fresh-child replay, frame refusal, pending termination recovery and joined EOF/signals; Linux uses cross-compiled native fixtures without race instrumentation |
| File mailbox | [Signed/encrypted native acceptance, clean command replay and complete cursor pages](file-mailbox.md) | Darwin and Linux one-shot CLI with stable accepted replay, complete cursor pages, refusal/exact-size controls, ownership/publication boundaries and pending signal recovery; Linux uses cross-compiled fixtures without race instrumentation |
| Directory mailbox | [Signed/encrypted native acceptance, restart replay and complete cursor pages](directory-mailbox.md) | Darwin and Linux polling service with complete cursor pages, restart replay, owner/refusal controls, quota/size and pending signal recovery; Linux filesystem claim/publication controls also passed, without race instrumentation |
| Append-log mailbox | [Owner-restricted signed/encrypted sends, restart replay and complete cursor pages](append-log.md) | Darwin and Linux service with complete cursor pages, stable replay, retained claims, sealed rotation, partial recovery, owner/refusal/exact-size controls and pending signal recovery; Linux adapter controls passed without race instrumentation |
| Offline signed bundles | [Authenticated native import and durable replay rejection](offline-bundle.md) | Darwin and Linux real CLI, whole native bodies, partial capacity reports and kill/signal recovery; physical removable-media verification remains absent |
| QR image-sequence import | [Native PNG encoding/decoding, signed-bundle import, exact native delivery and durable replay rejection](qr-sequence.md#native-qualification) | Darwin ARM64 CoreImage/Vision clients, bounded complete sequences, original freshness and authentication, image/file refusals and process deadlines; full normal/race/static acceptance passed; send-only local image files, no camera or physical optical qualification |
| Local and offline carriers, plain authors | [Guest and registered posts, bound authors and request-ID replay with no principals or keys](local-authors.md) | Linux AMD64 against a current aircd: stdio, socket pair, FIFO, plaintext UART (PTY), Unix stream/seqpacket/datagram, System V queue, file/directory mailboxes, append log, offline bundle and BPv7; POSIX queue, shared memory, mapped file, Mach IPC, XPC, D-Bus and QR compile but were not run in this model |
| MCP Streamable HTTP | [Four-tool SDK network acceptance, native sends and bounded pages](mcp-http-testing.md) | Historical loopback standalone gateway evidence; outside the current roadmap scope |
| JSON-RPC | [Durable retry identity, native observer and bounded correlated pages](json-rpc-testing.md) | Local fixtures; RPC correlation is separate from durable identity |
| gRPC | [Generated client, native send/replay and exact bounded cursor pages](grpc-testing.md) | Local Darwin native gRPC; no gRPC-Web |
| WebTransport bidirectional | [Authenticated native send/replay and bounded cursor pages](webtransport-bidi-testing.md) | Local Go client; trusted-certificate browser acceptance remains open |
| WebTransport paired unidirectional | [Paired send/receipt/replay and bounded cursor reads](webtransport-uni-testing.md) | Local Go client; trusted-certificate browser acceptance remains open |
| WebTransport datagrams | [Discarded-admission retry, native send and bounded cursor reads](webtransport-datagram-testing.md) | Local Go client; browser lossy-network acceptance unverified |
| Raw QUIC bidirectional | [Authenticated native send/replay and exact bounded cursor pages](quic-bidi-testing.md) | Local Darwin QUIC endpoint |
| Raw QUIC paired unidirectional | [Paired authenticated sends, native observer and bounded cursor reads](quic-uni-testing.md) | Local Darwin QUIC endpoint |
| Raw QUIC datagrams | [Native send/replay, bounded cursor pages and incomplete-fragment denial](quic-datagram-testing.md) | Local packet fixtures; no reliable-delivery promise |
| UDP unicast | [Actual loss/reconnect replay and exact bounded encrypted mailbox pages](udp-testing.md) | Provisioned return route; no multicast/broadcast qualification |
| ICMPv6 encapsulation | [Encrypted native sends, lost-receipt replay, whole bounded history and joined shutdown](icmp-v6.md#native-qualification) | Darwin ARM64 unprivileged loopback paired endpoints; fixed protected replies, source/interface checks, fragment and receive-queue bounds; full normal/race/static acceptance passed; no IPv4 ICMP, Linux runtime, remote-peer or carrier qualification |
| IPv4 broadcast | [Encrypted packet ingress and fixed unicast replies](broadcast-ipv4.md) | Darwin/Linux service implementation with explicit interface/subnet; runtime and native AIRC qualification deferred |
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
| Native CoAP OSCORE | [Authenticated native sends, protected bounded history, whole-gateway replay and transfer bounds](oscore-listener.md) | Darwin pinned native worker/client; Linux runtime and production peer crash durability remain unverified |
| Native CoAP OSCORE non-confirmable POST | [NON signed native acceptance and stable-intent replay](oscore-listener.md) | Darwin native client; no NON history/watch or transport retransmission guarantee; Linux runtime unverified |

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
configuration; and owner-selected targets for open-ended APIs/products. Native
OSCORE and the three mailbox modes have the limited recorded evidence above;
the earlier standalone OSCORE prototype is not their acceptance boundary. Other
broker, IPC, packet, physical and overlay entries are unfinished unless
separately qualified. No full catalogue or final
acceptance claim follows from this table.

The unfinished WHO/WHOIS increment has been removed from this branch and preserved in a verified private recovery archive under the common Git directory. Test-speed work is part of the roadmap. The branch has no configured upstream; previous milestone notes report no installation or deployment. Keep the live service outside test fixtures.

The previous chronological acceptance record remains in Git at `28d255e:docs/gateway/STATUS.md`. Earlier passes apply to their recorded source versions; they do not certify a subsequently modified tree. Future verification belongs with the relevant transport specification and commit, not an expanding master log.
