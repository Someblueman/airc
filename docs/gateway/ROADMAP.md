# Connection gateway roadmap

This is the only active gateway work list. Take one item at a time. Each item owns one protocol/mode, one concrete test fix, or one target decision. If it needs several independently verifiable changes, split it before coding. Delete the line after verification and commit; keep evidence in the relevant specification and commit, not a progress log. If blocked, record the exact missing dependency on that line and continue with an independent item. Do not expand into adjacent items or another backlog.

Current owner direction: simplify the tests and implementations, remove Kafka and the [never-run connection modes](removed-modes.md), and verify the retained behavior locally and on the VPS. Earlier implementation-first work below remains unverified until its stated runtime acceptance is performed. The authorized UNIX verification host is `ubuntu@vps-2bb89225.vps.ovh.ca`; preserve its existing AIRC services when later running isolated fixtures.

The owner has authorized choosing concrete providers and APIs for unspecified catalogue entries. Record the chosen target when implementing each entry; do not leave those entries blocked solely on a product preference. Live credentials, identity mappings, external-client migrations and deployment access still require their actual configured values.

Scope: the [connection catalogue](airc-connection-gateway.html) and `/Users/sws/airc-connection-gateway.html`. The [catalogue evidence map](catalogue-evidence.md) reconciles individual modes and retains unresolved dependencies. Minimum: authenticate an authorized sender and submit a bounded message under the correct existing AIRC identity. Prioritize bounded reading wherever a usable return path exists; explicitly one-way connectors may remain send-only. Other chat features are optional. No new AIRC functionality or full native-operation parity is required.

Preserve working adapters, journal data, stable request IDs, identity/credential boundaries, bounded resources, retries, and honest uncertain outcomes. Use isolated fixtures. Leave the primary checkout and live service unchanged. Push, installation, and deployment require separate authorization.

For each connector/mode, use a real client or device and an independent AIRC observer to verify author, target, and body. Verify complete bounded reading with an explicit page/cursor boundary where supported, plus relevant authentication, replay, size, and shutdown behavior. Transport acknowledgements are not AIRC acceptance. Missing platform/device evidence stays unfinished. Reuse shared implementations where appropriate; verification of one mode does not complete another.

For test-speed fixes, record comparable before/after timings and preserve the tested claim, race detection, production deadlines, and durability. Use affected checks during implementation and required full acceptance at integration checkpoints. Reuse still-valid recorded evidence; do not rerun unchanged checks merely to delete a task line. Detailed prior findings remain at `2ac8559:docs/TEST_SPEED_TASKS.md`.

## Make tests faster

- Run fresh final full normal/race acceptance and required build, formatting, vet and staticcheck gates after remaining executable changes; preserve exact source and terminal results. Acceptance commands and triggers are documented in [testing.md](testing.md#gateway-test-selection-and-acceptance).

## Qualify existing connection modes

Linux Go-native qualification can now use host cross-compilation and digest-verified fixtures in the existing Ubuntu container, without installing a toolchain. Each mode still requires its own actual runtime acceptance. Native OSCORE remains blocked on a Linux C worker/client build environment: existing images contain no C compiler, and downloading a disposable build image requires approval.

- Verify Linux native OSCORE gateway send, bounded protected cursor reads and replay using the actual worker/client; retain missing Linux runtime evidence as unfinished.
- Verify WebTransport browser acceptance with an actually trusted certificate. Chrome 154 rejects the existing locally trusted mkcert root for QUIC; an owner-provided certificate chain trusted by Chrome for QUIC and its matching authorized endpoint are required. See [webtransport.md](webtransport.md#browser-acceptance-with-ordinary-certificate-trust).
- Verify WebRTC through a real remote NAT/relay path; loopback TURN evidence does not complete this item.

## Add request and mailbox connections

- Verify implemented GitHub.com inbound issue-comment webhooks: provider HMAC, explicit repository/user mapping, original-event freshness, durable redelivery, migration, bounds, native AIRC delivery and joined shutdown. [Chosen API and implementation](github-webhook.md); testing is deferred. Deployment requires actual owner-configured IDs, a webhook secret and a GitHub-trusted HTTPS endpoint.
- Verify implemented OpenSSH SFTP v3 mailbox: pinned host/client keys, exact signed principal/binding/target, whole encrypted results, retained requests, nonoverwriting publication, interrupted-temp recovery, bounds and joined shutdown. [Chosen provider and implementation](sftp.md); testing is deferred. Actual server/account/UID/key provisioning remains a deployment input.
- Verify implemented OpenSSH forced-command SCP upload inbox: dedicated UID/native mapping, exact encrypted signed Send grants, durable final acknowledgement, original freshness and retained replay, bounds, interruption and joined shutdown. [Chosen service and implementation](scp-inbox.md); testing is deferred. A restricted SSH account, authorized keys, host-key pin and configured UID remain deployment inputs.
- Verify the offline bundle importer through removable media with freshness and replay rejection; blocked on an authorized real device/mounted path: `diskutil list external physical` found no attached external physical disk on 2026-10-06.

## Add broker and event connections

- Verify implemented PostgreSQL 18 LISTEN/NOTIFY ingress: verified TLS/client certificates, exact encrypted signed Send grants, original freshness, retained recovery, notification loss, frame/capacity bounds and joined shutdown. [Chosen API and implementation](postgres-notify.md); testing is deferred. Database, role, certificates and authentication policy remain deployment inputs.
- Qualify Redis Pub/Sub with a real Redis client/server and independent native AIRC observer, including pre-intake loss, replay, bounds and shutdown. No Redis client/server executable or cached Redis image was found on 2026-10-07; obtaining a pinned isolated fixture requires separate owner authorization.
- Qualify Redis Streams with real Redis client/server and independent native AIRC observer, including pending replay, deleted entries, bounds and shutdown. A pinned isolated Redis fixture requires separate owner authorization; no executable or cached image was found on 2026-10-07.
- Qualify ZeroMQ with a real REQ client and independent native AIRC observer, including replay, bounds and shutdown. No ZeroMQ library/client or Python binding was found in system/Homebrew, bundled runtimes or the Go module cache on 2026-10-08; obtaining a pinned isolated fixture requires separate owner authorization.
- Qualify NNG with a real REQ0 client and independent native AIRC observer, including resend/replay, bounds and shutdown. No NNG library/client or Python binding was found in system/Homebrew, bundled runtimes or the Go module cache on 2026-10-08; obtaining a pinned isolated fixture requires separate owner authorization.
- Verify implemented IRC bounded read requests and encrypted account replies: explicit recipient keys, original-event freshness, current read grants, whole protected page/cursor framing, correlation, capacity and joined shutdown. [Chosen profile and implementation](irc-bridge.md#encrypted-bounded-read-replies); testing is deferred.
- Qualify the IRC bridge with a real IRC server/client and independent native AIRC observer, including author/room mapping, loops, replay, bounds, whole reads and shutdown. No IRC server/client executable or cache was found on 2026-10-08; cached containers contain only Debian/Ubuntu base images. An owner-provided fixture or separate authorization to obtain one is required.
- Verify implemented XMPP signed original-intent admission: original-time bounds, exact mapped signing principal/target/operation, durable duplicate/conflict handling and full replay-marker retention. [Chosen policy and implementation](xmpp-bridge.md#signed-original-intent-and-durable-admission); testing is deferred. Raw delay stamps grant no freshness.
- Verify implemented XMPP service ownership and encrypted whole Send/read replies: fixed response keys, signed correlation, MUC subject/ping handling, idle/frame bounds, shared capacity and joined shutdown. [Chosen service profile](xmpp-bridge.md#service-ownership-and-encrypted-replies); testing is deferred.
- Qualify XMPP with a real client/server and independent native AIRC observer, including author/room mapping, replay, bounds, reads and joined shutdown.
- Verify implemented Matrix cold-start and gap recovery: complete visible forward history, original freshness, durable whole-event admission, explicit source refusals, resumable page/room cursors, mapping-bound positions and schema migration. [Chosen recovery policy](matrix-bridge.md#complete-visible-history-recovery); testing is deferred.
- Verify implemented Matrix service ownership and encrypted whole Send/read replies: exact read marker and bounded extension, fixed recipient keys, original-time bounds, protected correlation, publication-before-cursor, shared capacity and joined shutdown. [Chosen service profile](matrix-bridge.md#service-ownership-and-encrypted-replies); testing is deferred.
- Qualify Matrix with a real homeserver/client and independent native AIRC observer, including mapping, replay, bounds, whole reads and joined shutdown.

## Add local and virtual-machine connections

- Verify implemented Linux AMD64 System V queues: actual kernel layout and queue lifecycle, schema-23 preservation, protected Send and whole reads, replay, crash recovery and joined shutdown. [Chosen architecture and implementation](system-v-queue.md#linux-amd64-implementation); testing is deferred. An isolated AMD64 Linux fixture is required; existing ARM64 qualification does not qualify it.

## Add network packet connections

- Verify Linux IPv6 multicast socket delivery and native send/read/replay, per-sender trust, destination/interface filtering, unicast-only replies and joined close. Darwin ARM64 is qualified in [multicast-ipv6.md](multicast-ipv6.md#native-gateway-qualification-on-darwin); the isolated Linux fixture lacks a multicast route and NET_ADMIN setup awaits owner authorization.
- Verify IPv4 broadcast socket subnet ownership, destination/interface filtering, unicast-only replies and joined close.
- Verify implemented IPv4 broadcast service ingress with per-sender trust, whole protected unicast replies, replay, bounds and joined shutdown. [Implementation profile](broadcast-ipv4.md); runtime and native AIRC testing are deferred by owner direction.

## Add device and physical connections

- Verify implemented ChirpStack 4 HTTP LoRaWAN unconfirmed uplinks: trusted server MIC/counter policy, exact device/native grants, original event freshness, durable redelivery, migration, bounds, native AIRC delivery and joined shutdown. [Chosen API and implementation](chirpstack.md); testing is deferred. A provisioned server, radio device, keys and trusted HTTPS endpoint remain deployment inputs.
- Verify the implemented UART connector through a real physical UART link; Darwin and Linux virtual serial are qualified in [uart.md](uart.md#native-virtual-serial-qualification-on-darwin-and-linux), while electrical behavior and real baud throughput require an authorized device.
- Verify the serial connector over a real RS-232 link.
- Verify the serial connector over a real RS-485 link.
- Verify the serial connector over a real modem link.

## Verify carrier and overlay paths

- Verify an existing IP connector over Ethernet; do not add another transport implementation.
- Verify an existing IP connector over Wi-Fi; do not add another transport implementation.
- Verify an existing IP connector over cellular; retain unavailable network evidence explicitly.
- Verify an existing IP connector over satellite; retain unavailable network evidence explicitly.
- Verify an existing connector through a configured VPN.
- Verify an existing connector through a configured private mesh overlay.
- Verify an existing TCP connector through Tor.

## Final acceptance

- Run the final required acceptance checks on an unchanged source tree and capture their terminal results.
- Update the supported-mode inventory from those results, then remove completed roadmap lines.
