# Connection gateway roadmap

This is the only active gateway work list. Work top to bottom. Delete a task line after its stated result is verified and committed; do not add completed entries, progress logs, or another backlog. Record checks and limitations in the commit and the relevant transport specification.

Scope: the [connection catalogue](airc-connection-gateway.html) and original brief at `/Users/sws/airc-connection-gateway.html`. A connector is complete when it authenticates an authorized sender and submits a bounded message as the correct existing AIRC identity. Prioritize bounded reading wherever the connection has a usable return path; explicitly one-way connectors may remain send-only. Replies, live streams, and other chat features are optional capabilities. Keep working implementations; additional AIRC features and full operation parity across transports are outside this project.

Gateway ownership includes adapters, authentication, identity mapping, bounded resources, durable intake, retries, and correlation. AIRC remains the existing chat service. Preserve stable request IDs, existing journal data, credential boundaries, and honest uncertain outcomes. Use isolated fixtures; do not alter the primary checkout or live service. Publishing, installation, and deployment require separate authorization.

A task is done after a real client or device crosses its actual connection, an independent AIRC observer confirms author/target/body, a return-capable connector reads complete bounded native records with an explicit cursor or page boundary, and relevant authentication, replay, size, and shutdown checks pass. A transport acknowledgement is not AIRC acceptance. Preserve this distinction even when the caller has no return path. Run affected checks while working and the required full race gate at integration checkpoints. Platform or hardware limitations stay on the task line with the exact missing evidence; they are not completed work. Catalogue entries can share implementation, but each named protocol or mode needs its own evidence.

## Recover the branch

- Reduce gateway MCP to a small chat interface (send, read, wait, status), with narrow argument schemas and permission-scoped discovery; remove the automatic/native-operation tool mirror and update its clients and tests together.
- Review the existing local `airc mcp` with the same small-interface goal; resolve known client use before changing its published tool contract, without growing this gateway branch into an AIRC feature project.
- Remove gateway-only SDK changes that are unnecessary for the retained connectors; keep necessary wire collectors and leave the AIRC daemon and ordinary CLI unchanged.
- Remove operation-expansion requirements from retained docs; distinguish available optional features from connector completion, and preserve actual protocol/security contracts.

## Make tests faster

These tasks replace `docs/TEST_SPEED_TASKS.md`; its detailed findings remain in Git at `2ac8559:docs/TEST_SPEED_TASKS.md`. Suspected costs are not measured speedups. Record comparable before/after timings with each fix; preserve real carrier checks, race detection, durability, and production deadlines. Start here before the expensive recovery acceptance runs.

- Capture one ranked normal/race timing baseline with source version, build/startup costs, leaf-test times, host load, and terminal results; reuse adequate logs and run full suites sequentially.
- Document fast affected-package/test commands and a distinct full acceptance command; handle runtime-built child changes correctly when caching, keep fresh final acceptance, and avoid full normal/race runs after each small edit.
- Match gateway child binaries to the parent test's race mode (`gateway_binary_test.go`); keep race-enabled children and race-report detection in the race gate.
- Build CLI and example executables once per test process instead of repeated builds or `go run`; batch pinned-fixture checks and add cross-run artifact caching only if measured cost justifies verified source/toolchain keys.
- Seed bulk UI/history fixtures over one persistent native session instead of hundreds of CLI connections and outbox syncs; retain real separate CLI invocations for cursor/outbox recovery tests.
- Reuse an independently owned native STATUS client during observer-count polling; preserve count/capacity semantics, exclusive event consumption, and cleanup.
- Remove throttle sleeps from receipt-only cases that cannot resend; replace redundant readiness, membership, and mutation waits with existing ACK/observer/ready signals, retaining real expiry and no-effect proofs.
- Separate genuine 15-second read-lease tests from repeated stream terminal/resume checks; use existing signed-validity bounds only where equivalent and retain bearer expiry and HTTP SSE write-deadline witnesses.
- Shorten repeated WebRTC/DTLS/UDP idle, CoAP CSM/heartbeat/revocation, WebSocket ping/pong, and AGD1 assembly-expiry checks through existing explicit-time boundaries; retain real default-timer and carrier cleanup evidence without public test-only settings.
- Remove avoidable delivery-expiry waits using existing admission time input and deterministic phase ordering; preserve runner-driven expiry, quota pressure/release, and zero-send assertions.
- Replace negative CLI/raw-collector/carrier waits with causal fences only where they prove the same absence; retain continuous-noise, actual deadline, and quiet-expiry coverage.
- Reduce slow-reader setup/pacing only after witnessing actual blocked consumption or write pressure; preserve each carrier's deadline, lease release, closure, and fresh-admission checks.
- Consolidate duplicated operation semantics into shared-layer tests while retaining each carrier's real authentication, framing, send/read, retry, cancellation, and failure checks.
- Pilot bounded parallelism on independent fixtures, then separately compare package concurrency with `-p 1`; keep shared clocks/environment tests serial, run shuffled race repetitions, and require measured wall-time improvement without resource regressions.
- If remaining measurements justify it, reduce setup/allocation/I/O and load-test scheduling costs; preserve work/cancellation assertions, secure credential creation, pinned-daemon provenance, sync barriers, and fresh-schema/migration equivalence. Use lighter native durability modes only for fixtures whose claims do not require persistence.

## Verify recovery

- Verify the shared authentication, journal, retry, receipt, capacity, and shutdown paths once; identify any missing baseline acceptance and fix only those defects.
- Qualify the existing HTTP, TCP/TLS, WebSocket, Unix stream/datagram, stdio, MCP HTTP, RPC, QUIC, UDP/DTLS, WebTransport, WebRTC, and secure CoAP adapters for authenticated send and bounded reading where supported; retain explicit platform/browser limitations.
- Add one documented scratch-server/client send-and-read example for ordinary first use and an accurate inventory of supported, unavailable, and unimplemented catalogue modes.

## Finish connection coverage

- Finish plain CoAP UDP/TCP and OSCORE, including non-confirmable POST and durable replay-state recovery.
- Add authenticated inbound webhooks; implement outbound callbacks only as a separately configured optional capability.
- Add file/directory/log mailboxes and authenticated SFTP/SCP/FTPS upload inboxes, with atomic intake and restart replay checks.
- Add MQTT/MQTT-SN and AMQP/STOMP, verifying publisher identity, stable IDs, and acknowledgement timing for each protocol.
- Add authenticated syslog/signed syslog and SNMPv3 traps with provisioned sender trust and freshness checks.
- Add NATS, Redis Pub/Sub/Streams, Kafka, DDS, ZeroMQ/NNG, and managed queues/event buses; state each product/mode's actual loss and acknowledgement semantics.
- Add chat bridges, object-storage/database mailboxes, email/SMS, Bundle Protocol, and offline/removable-media import without loops or execution of supplied content.
- Add Unix sequenced packets, FIFOs, Windows named pipes, inherited channels, shared-memory/OS queues, platform IPC, and VM sockets; record platform evidence individually.
- Add SCTP, DCCP, UDP-Lite, multicast/broadcast, raw IP/Ethernet, and ICMP encapsulation; record unavailable native endpoints explicitly.
- Add Bluetooth/BLE advertising, ESP-NOW, LoRaWAN, serial/USB, device buses, NFC, radio mesh, specialized interconnects, optical/acoustic, and one-way-link bridges; retain each missing device test as unfinished work.
- Verify IP carriers, SSH, SOCKS/HTTP CONNECT, VPN/private mesh, Tor, and custom DNS/ICMP tunnels through their real configured paths; reuse existing connectors where appropriate.
- Check every catalogue entry against actual evidence, run final acceptance on an unchanged tree, and leave a concise supported-mode inventory with exact remaining blockers.
