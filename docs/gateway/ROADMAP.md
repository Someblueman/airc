# Connection gateway roadmap

This is the only active gateway work list. Take one item at a time. Each item owns one protocol/mode, one concrete test fix, or one target decision. If it needs several independently verifiable changes, split it before coding. Delete the line after verification and commit; keep evidence in the relevant specification and commit, not a progress log. If blocked, record the exact missing dependency on that line and continue with an independent item. Do not expand into adjacent items or another backlog.

Scope: the [connection catalogue](airc-connection-gateway.html) and `/Users/sws/airc-connection-gateway.html`. Minimum: authenticate an authorized sender and submit a bounded message under the correct existing AIRC identity. Prioritize bounded reading wherever a usable return path exists; explicitly one-way connectors may remain send-only. Other chat features are optional. No new AIRC functionality or full native-operation parity is required.

Preserve working adapters, journal data, stable request IDs, identity/credential boundaries, bounded resources, retries, and honest uncertain outcomes. Use isolated fixtures. Leave the primary checkout and live service unchanged. Push, installation, and deployment require separate authorization.

For each connector/mode, use a real client or device and an independent AIRC observer to verify author, target, and body. Verify complete bounded reading with an explicit page/cursor boundary where supported, plus relevant authentication, replay, size, and shutdown behavior. Transport acknowledgements are not AIRC acceptance. Missing platform/device evidence stays unfinished. Reuse shared implementations where appropriate; verification of one mode does not complete another.

For test-speed fixes, record comparable before/after timings and preserve the tested claim, race detection, production deadlines, and durability. Use affected checks during implementation and required full acceptance at integration checkpoints. Reuse still-valid recorded evidence; do not rerun unchanged checks merely to delete a task line. Detailed prior findings remain at `2ac8559:docs/TEST_SPEED_TASKS.md`.

## Recover the branch

- Reduce gateway MCP to send/read/wait/status with narrow schemas and permission-scoped discovery; update its clients and tests together.
- Identify the existing local `airc mcp` clients and required tool calls before changing its published contract. Repository callers are mapped in [MCP.md](../MCP.md); blocked on the owner identifying any deployed clients and required calls outside this repository (no AIRC entry was found in the local Claude/Codex configuration).
- Reduce local `airc mcp` to the agreed small tool surface; migrate the identified callers together. Blocked on the owner choosing the reduced published tool surface; the existing contract has 19 tools.

## Make tests faster

- Document the full acceptance command and when it is required; retain fresh final execution.
- Shorten repeated WebTransport datagram lease checks using existing signed-validity bounds; retain one real grant-expiry witness.
- Shorten repeated UDP mailbox lease checks using existing signed-validity bounds; retain one real grant-expiry witness.
- Shorten repeated DTLS lease checks using existing signed-validity bounds; retain one real grant-expiry witness.
- Remove duplicate bearer gRPC lease waits only where equivalent; retain actual bearer expiry semantics.
- Remove duplicate bearer JSON-RPC lease waits only where equivalent; retain actual bearer expiry semantics.
- Remove duplicate HTTP SSE lease waits only where equivalent; retain the proof that quiet streams outlive the ordinary write deadline.
- Reduce repeated WebRTC idle-policy waits through existing time boundaries; retain valid renewal and invalid-traffic non-renewal.
- Reduce repeated DTLS idle-policy waits through existing time boundaries; retain valid renewal and invalid-traffic non-renewal.
- Reduce repeated UDP source-idle waits through the existing explicit-time reaper; retain slot release and fresh admission.
- Reduce repeated CoAP TCP missing-CSM waits; retain the real failed-negotiation closure witness.
- Reduce repeated CoAP heartbeat waits without losing quiet notification or cancellation checks.
- Reduce repeated CoAP TCP heartbeat waits without losing quiet notification or cancellation checks.
- Reduce repeated CoAP revocation waits; retain immediate denial of new work and expiry of an already granted read.
- Reduce repeated WebSocket missing-pong waits; retain real closure and default timer evidence.
- Reduce repeated WebSocket pre-authentication waits; retain rejection before native intake.
- Move exhaustive AGD1 assembly expiry checks to explicit-time tests; retain real UDP incomplete-request expiry and retry evidence.
- Remove redundant Unix-datagram assembly expiry waits only where shared AGD1 checks cover the policy; retain carrier-specific cleanup evidence.
- Remove avoidable delivery-expiry waits using existing admission time and deterministic quota/maintenance phase ordering.
- Shorten negative CLI waits only where a causal fence proves the same absence; retain continuous-noise and real deadline checks.
- Shorten raw-collector negative waits only where ordered completion proves the same absence; retain actual timeout coverage.
- Reduce gRPC slow-reader setup only after witnessing actual write pressure; retain deadline and lease-release checks.
- Reduce HTTP/3 slow-reader setup only after witnessing actual write pressure; retain deadline and lease-release checks.
- Reduce raw QUIC slow-reader setup only after witnessing actual write pressure; retain deadline and lease-release checks.
- Reduce WebTransport slow-reader setup only after witnessing actual write pressure; retain deadline and lease-release checks.
- Reduce WebRTC slow-reader pacing only after witnessing blocked consumption; retain channel closure and fresh-peer recovery.
- Consolidate duplicate send-semantics checks into the shared backend tests; retain each carrier's real send and failure checks.
- Consolidate duplicate history-semantics checks into the shared read tests; retain each carrier's real framing, paging, and cancellation checks.
- Pilot bounded parallelism in pure gateway core tests; measure improvement and run shuffled race checks.
- Pilot bounded parallelism in independent gateway config fixtures; retain environment-sensitive tests serially.
- Pilot bounded parallelism in independent gateway auth fixtures; retain private credential/state isolation.
- Pilot bounded parallelism in independent gateway store fixtures; retain durable-state and migration isolation.
- Pilot bounded parallelism in independent HTTP integration fixtures; retain per-test daemon, port, journal, and identity ownership.
- Compare package concurrency against `-p 1` separately from within-package parallelism; require a measured stable improvement.
- Measure credential/key fixture setup; reuse only safe immutable material if its cost is material.
- Measure fresh store initialization; consolidate schema creation only if justified and fresh/migrated contracts remain equivalent.
- Measure remaining test allocation/I/O costs; optimize one demonstrated hotspot without weakening sync or secure-file boundaries.
- Use a lighter existing durability mode only in individually identified native fixtures that make no persistence claim.
- Shorten the load-generator test schedule only if the same work, receipt, health, and cancellation assertions remain reliable.

## Verify the shared gateway

- Verify baseline authentication and principal/target isolation through the existing real fixture.
- Verify durable intake and stable-ID replay through restart without losing accepted jobs.
- Verify bounded retries and honest uncertain-send outcomes without resending known acceptance.
- Verify correlated native receipts and retained receipt recovery with an independent observer.
- Verify bounded history pages and principal-scoped cursor resume.
- Verify session/request capacity and the reservation that keeps reads from starving sends.
- Verify joined shutdown and listener rebinding with an active baseline read.
- Add one scratch-server/client send-and-read example that works from the documented commands.
- Produce a concise supported-mode inventory from actual evidence, with exact outstanding blockers.

## Qualify existing connection modes

- Verify framed TCP over IPv4 against authenticated send and bounded-read acceptance.
- Verify framed TCP over IPv6 against authenticated send and bounded-read acceptance.
- Verify framed TLS against certificate, authentication, send, and bounded-read acceptance.
- Verify HTTP/1.1 against authenticated send and bounded-read acceptance.
- Verify HTTP/2 against authenticated send and bounded-read acceptance.
- Verify HTTP/3 against authenticated send and bounded-read acceptance.
- Verify HTTP GET polling and explicit cursor/page behavior.
- Verify HTTP long-poll wakeup, quiet deadline, and cancellation.
- Verify HTTP POST durable message submission and stable-ID retry.
- Verify HTTP PUT stable request-resource semantics and conflicting-ID rejection.
- Verify SSE bounded reads and cursor-based reconnection as the HTTP submission companion.
- Verify WebSocket authenticated send and bounded reads.
- Verify WSS certificate/origin checks, authenticated send, and bounded reads.
- Verify Darwin Unix-stream authenticated send and bounded reads with owner-only socket permissions.
- Verify Linux Unix-stream authenticated send and bounded reads; retain missing Linux runtime evidence as unfinished.
- Verify Darwin Unix-datagram authenticated send and bounded mailbox reads.
- Verify Linux Unix-datagram authenticated send and bounded mailbox reads; retain missing Linux runtime evidence as unfinished.
- Verify framed stdio authenticated send and bounded reads over owned anonymous pipes.
- Verify MCP Streamable HTTP through the reduced tool interface and a real remote SDK client.
- Verify JSON-RPC authenticated send and bounded reads with durable IDs separate from RPC IDs.
- Verify gRPC authenticated unary send and bounded reads through the generated client.
- Verify WebTransport bidirectional streams against authenticated send and bounded reads.
- Verify WebTransport paired unidirectional streams against authenticated send and bounded reads.
- Verify WebTransport datagrams against authenticated send and bounded reads with honest loss semantics.
- Verify WebTransport browser acceptance with an actually trusted certificate.
- Verify raw QUIC bidirectional streams against authenticated send and bounded reads.
- Verify raw QUIC paired unidirectional streams against authenticated send and bounded reads.
- Verify raw QUIC datagrams against authenticated send and bounded reads with honest loss semantics.
- Verify UDP unicast authenticated send and encrypted mailbox reads, including loss/replay recovery.
- Verify DTLS datagram authenticated send and bounded reads after certificate-verified setup.
- Verify reliable ordered WebRTC DataChannels against authenticated send and bounded reads.
- Verify reliable unordered WebRTC DataChannels against authenticated send and bounded reads.
- Verify unreliable ordered WebRTC DataChannels against authenticated send and bounded reads with honest loss semantics.
- Verify unreliable unordered WebRTC DataChannels against authenticated send and bounded reads with honest loss semantics.
- Verify WebRTC browser acceptance with an actually trusted certificate.
- Verify WebRTC through a real remote NAT/relay path; loopback TURN evidence does not complete this item.
- Verify CoAP over DTLS authenticated send and bounded reads through confirmable requests.
- Verify CoAP over DTLS authenticated non-confirmable message submission.
- Verify CoAP over TLS/TCP authenticated send and bounded reads within negotiated frame bounds.

## Add request and mailbox connections

- Add authenticated plain CoAP over UDP with bounded reading where a return path exists.
- Add authenticated plain CoAP over TCP with bounded reading where a return path exists.
- Add CoAP OSCORE protection with durable sequence/replay-state restart acceptance.
- Add CoAP non-confirmable POST under OSCORE with authenticated send-only acceptance.
- Add authenticated inbound webhooks with provider-verified identity and freshness.
- Add a local message-file mailbox with atomic intake and stable-ID restart replay.
- Add a watched-directory mailbox with safe per-principal ownership and crash replay.
- Add append-only log ingestion with generation/offset recovery and bounded partial records.
- Add an authenticated SFTP upload inbox through an existing transfer service.
- Add an authenticated SCP upload inbox through an existing transfer service.
- Add an authenticated FTPS upload inbox through an existing transfer service.
- Add Bundle Protocol ingestion with stable bundle identity and delayed-delivery semantics.
- Add offline signed-message bundle import without executing supplied content.
- Verify the offline bundle importer through removable media with freshness and replay rejection.

## Add broker and event connections

- Add MQTT ingestion with verified publisher/topic authority and explicit acknowledgement timing.
- Add MQTT-SN ingestion with verified publisher/topic authority and explicit loss semantics.
- Add AMQP ingestion with durable intake before acknowledgement and stable redelivery IDs.
- Add STOMP ingestion with durable intake before acknowledgement and stable redelivery IDs.
- Add authenticated TLS syslog ingestion with a provisioned connection-to-sender mapping.
- Add signed syslog ingestion that verifies covered records and freshness before posting.
- Add authenticated SNMPv3 trap ingestion with bounded allowed notification fields.
- Add NATS publish ingestion with verified publisher mapping and honest pre-intake loss semantics.
- Add Redis Pub/Sub ingestion with verified publisher mapping and honest pre-intake loss semantics.
- Add Redis Streams ingestion with stable entry identity and durable acknowledgement/replay behavior.
- Add Kafka ingestion with stable record identity and contiguous durable offset advancement.
- Add a ZeroMQ connector using one documented authenticated messaging pattern.
- Add an NNG connector using one documented authenticated messaging pattern.
- Add an IRC chat bridge with explicit author/room mapping and loop prevention.
- Add an XMPP chat bridge with explicit author/room mapping and loop prevention.
- Add a Matrix chat bridge with explicit author/room mapping and loop prevention.

## Add local and virtual-machine connections

- Add Unix sequenced-packet IPC on a supported native host; retain unsupported Darwin evidence explicitly.
- Add paired POSIX FIFOs with authenticated sender ownership and non-interleaved return routing.
- Add Windows named pipes with verified client identity and tested access controls.
- Add socket-pair ingress through a parent-owned channel.
- Add inherited-file-descriptor ingress with an explicit ownership and shutdown contract.
- Add a shared-memory message queue with authenticated producer ownership and crash-safe synchronization.
- Add a memory-mapped-file message queue with authenticated producer ownership and crash-safe synchronization.
- Add POSIX message-queue ingress with sender authorization and bounded queue handling.
- Add System V message-queue ingress with sender authorization and bounded queue handling.
- Add a Mach IPC bridge with verified caller mapping.
- Add an XPC bridge with verified caller mapping.
- Add a Windows RPC bridge with verified caller mapping.
- Add a COM bridge with verified caller mapping.
- Add a D-Bus bridge with verified caller mapping.
- Add an Android Binder bridge with verified caller mapping.
- Add virtio-vsock connectivity through a real host/guest fixture.
- Add Hyper-V socket connectivity through a real host/guest fixture.

## Add network packet connections

- Add SCTP ordered-message connectivity through a native client.
- Add SCTP unordered-message connectivity through a native client.
- Add SCTP partial-reliability connectivity through a native client that supports the selected mode.
- Add DCCP authenticated packet ingress through compatible native endpoints.
- Add UDP-Lite authenticated packet ingress with an explicit integrity policy.
- Add IPv4 multicast ingress with per-sender trust and no response storm.
- Add IPv6 multicast ingress with per-sender trust and no response storm.
- Add IPv4 broadcast ingress with per-sender trust and no response storm.
- Add raw-IP custom-protocol ingress with bounded authenticated framing.
- Add ICMP message encapsulation through paired authorized endpoints.
- Add ICMPv6 message encapsulation through paired authorized endpoints.
- Add raw-Ethernet frame ingress with bounded authenticated framing.

## Add device and physical connections

- Add Bluetooth RFCOMM connectivity through a real authenticated device/client.
- Add Bluetooth L2CAP connectivity through a real authenticated device/client.
- Add BLE GATT connectivity through a real authenticated device/client.
- Add BLE advertising ingress with a verifiable fresh application envelope.
- Add BLE extended-advertising ingress with a verifiable fresh application envelope.
- Add ESP-NOW unicast ingress with provisioned peer trust.
- Add ESP-NOW broadcast ingress with independently verified application authentication.
- Add LoRaWAN unconfirmed-uplink ingestion through a trusted device-verifying server integration.
- Add UART connectivity with bounded authenticated serial framing.
- Verify the serial connector over a real RS-232 link.
- Verify the serial connector over a real RS-485 link.
- Verify the serial connector over a virtual serial link.
- Verify the serial connector over a real modem link.
- Add USB serial connectivity through a real device.
- Add USB bulk-transfer connectivity through a real device.
- Add USB interrupt-transfer connectivity through a real device.
- Add CAN ingress through a real bus adapter with bounded authenticated messages.
- Add CAN-FD ingress through a real bus adapter with bounded authenticated messages.
- Add SPI ingress through a real device bridge with bounded authenticated messages.
- Add I²C ingress through a real device bridge with bounded authenticated messages.
- Add NFC/NDEF signed-message import with trusted signatures, permissions, and replay rejection.
- Add IEEE 802.15.4 ingress through a real provisioned radio bridge.
- Add Zigbee ingress through a real provisioned radio bridge.
- Add LoRa ingress through a real provisioned radio bridge.
- Add InfiniBand connectivity through real compatible endpoints.
- Add RDMA connectivity through real compatible endpoints.
- Add optical message-sequence ingress with authenticated framing and freshness.
- Add QR-sequence message import with authenticated framing and freshness.
- Add acoustic message ingress with authenticated framing and freshness.
- Verify authenticated message ingestion across a real data-diode link without assuming a return path.

## Verify carrier and overlay paths

- Verify an existing IP connector over Ethernet; do not add another transport implementation.
- Verify an existing IP connector over Wi-Fi; do not add another transport implementation.
- Verify an existing IP connector over cellular; retain unavailable network evidence explicitly.
- Verify an existing IP connector over satellite; retain unavailable network evidence explicitly.
- Verify an existing connector through SSH local forwarding.
- Verify an existing connector through an SSH reverse tunnel.
- Verify an existing connector through SOCKS.
- Verify an existing connector through HTTP CONNECT.
- Verify an existing connector through a configured VPN.
- Verify an existing connector through a configured private mesh overlay.
- Verify an existing TCP connector through Tor.

## Choose concrete targets before implementation

These catalogue entries do not name a concrete implementation target. Each decision must produce one named API/protocol/product and one bounded implementation line; do not build a universal adapter or keep searching indefinitely. Ask for the owner's choice when it materially changes scope or access.

- Select one DDS implementation and its authenticated ingress mode; replace this line with its concrete implementation task.
- Select one managed queue service/API; replace this line with its concrete implementation task.
- Select one cloud event-bus service/API; replace this line with its concrete implementation task.
- Select one object-storage service/API; replace this line with its concrete mailbox implementation task.
- Select one database mailbox engine/API; replace this line with its concrete implementation task.
- Select one database change-feed API; replace this line with its concrete implementation task.
- Select one database notification API; replace this line with its concrete implementation task.
- Select one email ingestion service/protocol and sender-verification rule; replace this line with its concrete implementation task.
- Select one SMS ingestion service/API and sender-verification rule; replace this line with its concrete implementation task.
- Select one additional chat-bridge protocol beyond IRC/XMPP/Matrix, or explicitly exclude that open-ended catalogue label.
- Select any required OS message-queue API beyond POSIX/System V, or explicitly exclude that open-ended catalogue label.
- Select one radio-mesh protocol beyond IEEE 802.15.4/Zigbee/LoRa, or explicitly exclude that open-ended catalogue label.
- Select one specialized interconnect beyond InfiniBand/RDMA, or explicitly exclude that open-ended catalogue label.
- Select one DNS tunnel implementation and allowed path; replace this line with its concrete configured-path acceptance task.
- Select one ICMP tunnel implementation and allowed path; replace this line with its concrete configured-path acceptance task.
- Decide whether outbound webhook callbacks are needed; if so, add one concrete authenticated callback implementation task.

## Final acceptance

- Reconcile the 72 catalogue entries with individual mode evidence; retain exact unresolved platform/device/target dependencies.
- Run the final required acceptance checks on an unchanged source tree and capture their terminal results.
- Update the supported-mode inventory from those results, then remove completed roadmap lines.
