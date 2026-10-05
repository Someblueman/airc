# Connection gateway roadmap

This is the only active gateway work list. Work top to bottom. Delete a task line after its stated result is verified and committed; do not add completed entries, progress logs, or another backlog. Record checks and limitations in the commit and the relevant transport specification.

Scope: the [connection catalogue](airc-connection-gateway.html) and original brief at `/Users/sws/airc-connection-gateway.html`. A connector is complete when it authenticates an authorized sender and submits a bounded message as the correct existing AIRC identity. Prioritize bounded reading wherever the connection has a usable return path; explicitly one-way connectors may remain send-only. Replies, live streams, and other chat features are optional capabilities. Keep working implementations; additional AIRC features and full operation parity across transports are outside this project.

Gateway ownership includes adapters, authentication, identity mapping, bounded resources, durable intake, retries, and correlation. AIRC remains the existing chat service. Preserve stable request IDs, existing journal data, credential boundaries, and honest uncertain outcomes. Use isolated fixtures; do not alter the primary checkout or live service. Publishing, installation, and deployment require separate authorization.

A task is done after a real client or device crosses its actual connection, an independent AIRC observer confirms author/target/body, a return-capable connector reads complete bounded native records with an explicit cursor or page boundary, and relevant authentication, replay, size, and shutdown checks pass. A transport acknowledgement is not AIRC acceptance. Preserve this distinction even when the caller has no return path. Run affected checks while working and the required full race gate at integration checkpoints. Platform or hardware limitations stay on the task line with the exact missing evidence; they are not completed work. Catalogue entries can share implementation, but each named protocol or mode needs its own evidence.

## Recover the branch

- Separate unfinished WHO/WHOIS work from this project, including new CLI wrappers and gateway wiring; preserve unrelated dirty edits and isolate the recovery commit.
- Reduce gateway MCP to a small chat interface (send, read, wait, status), with narrow argument schemas and permission-scoped discovery; remove the automatic/native-operation tool mirror and update its clients and tests together.
- Review the existing local `airc mcp` with the same small-interface goal; resolve known client use before changing its published tool contract, without growing this gateway branch into an AIRC feature project.
- Remove gateway-only SDK changes that are unnecessary for the retained connectors; keep necessary wire collectors and leave the AIRC daemon and ordinary CLI unchanged.
- Remove operation-expansion requirements from retained docs; distinguish available optional features from connector completion, and preserve actual protocol/security contracts.
- Verify the shared authentication, journal, retry, receipt, capacity, and shutdown paths once; identify any missing baseline acceptance and fix only those defects.
- Qualify the existing HTTP, TCP/TLS, WebSocket, Unix stream/datagram, stdio, MCP HTTP, RPC, QUIC, UDP/DTLS, WebTransport, WebRTC, and secure CoAP adapters for authenticated send and bounded reading where supported; retain explicit platform/browser limitations.
- Remove duplicated operation-parity test work while retaining each carrier's real authentication, framing, send, failure, and cleanup checks; use the test-speed investigation only where measurements justify a concrete change.
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
