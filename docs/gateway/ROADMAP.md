# Connection gateway roadmap

This is the only active gateway work list. Take one item at a time. Each item owns one protocol/mode, one concrete test fix, or one target decision. If it needs several independently verifiable changes, split it before coding. Delete the line after verification and commit; keep evidence in the relevant specification and commit, not a progress log. If blocked, record the exact missing dependency on that line and continue with an independent item. Do not expand into adjacent items or another backlog.

Current owner direction: complete implementation first and leave testing for later. Review and compile each change; retain unexecuted runtime, security, normal/race and device acceptance here as verification work. Fixture availability does not block implementation. The authorized UNIX verification host is `ubuntu@vps-2bb89225.vps.ovh.ca`; preserve its existing AIRC services when later running isolated fixtures.

The owner has authorized choosing concrete providers and APIs for unspecified catalogue entries. Record the chosen target when implementing each entry; do not leave those entries blocked solely on a product preference. Live credentials, identity mappings, external-client migrations and deployment access still require their actual configured values.

Scope: the [connection catalogue](airc-connection-gateway.html) and `/Users/sws/airc-connection-gateway.html`. The [72-entry evidence map](catalogue-evidence.md) reconciles individual modes and retains unresolved dependencies. Minimum: authenticate an authorized sender and submit a bounded message under the correct existing AIRC identity. Prioritize bounded reading wherever a usable return path exists; explicitly one-way connectors may remain send-only. Other chat features are optional. No new AIRC functionality or full native-operation parity is required.

Preserve working adapters, journal data, stable request IDs, identity/credential boundaries, bounded resources, retries, and honest uncertain outcomes. Use isolated fixtures. Leave the primary checkout and live service unchanged. Push, installation, and deployment require separate authorization.

For each connector/mode, use a real client or device and an independent AIRC observer to verify author, target, and body. Verify complete bounded reading with an explicit page/cursor boundary where supported, plus relevant authentication, replay, size, and shutdown behavior. Transport acknowledgements are not AIRC acceptance. Missing platform/device evidence stays unfinished. Reuse shared implementations where appropriate; verification of one mode does not complete another.

For test-speed fixes, record comparable before/after timings and preserve the tested claim, race detection, production deadlines, and durability. Use affected checks during implementation and required full acceptance at integration checkpoints. Reuse still-valid recorded evidence; do not rerun unchanged checks merely to delete a task line. Detailed prior findings remain at `2ac8559:docs/TEST_SPEED_TASKS.md`.

## Recover the branch

- Identify the existing local `airc mcp` clients and required tool calls before changing its published contract. Repository callers are mapped in [MCP.md](../MCP.md); blocked on the owner identifying any deployed clients and required calls outside this repository (no AIRC entry was found in the local Claude/Codex configuration).
- Reduce local `airc mcp` to the agreed small tool surface; migrate the identified callers together. Blocked on the owner choosing the reduced published tool surface; the existing contract has 19 tools.

## Make tests faster

- Run fresh final full normal/race acceptance and required build, formatting, vet and staticcheck gates after remaining executable changes; preserve exact source and terminal results. Acceptance commands and triggers are documented in [testing.md](testing.md#gateway-test-selection-and-acceptance).

## Qualify existing connection modes

Linux Go-native qualification can now use host cross-compilation and digest-verified fixtures in the existing Ubuntu container, without installing a toolchain. Each mode still requires its own actual runtime acceptance. Native OSCORE remains blocked on a Linux C worker/client build environment: existing images contain no C compiler, and downloading a disposable build image requires approval.

- Verify Linux native OSCORE gateway send, bounded protected cursor reads and replay using the actual worker/client; retain missing Linux runtime evidence as unfinished.
- Verify WebTransport browser acceptance with an actually trusted certificate. Chrome 154 rejects the existing locally trusted mkcert root for QUIC; an owner-provided certificate chain trusted by Chrome for QUIC and its matching authorized endpoint are required. See [webtransport.md](webtransport.md#browser-acceptance-with-ordinary-certificate-trust).
- Verify WebRTC through a real remote NAT/relay path; loopback TURN evidence does not complete this item.

## Add request and mailbox connections

- Verify implemented GitHub.com inbound issue-comment webhooks: provider HMAC, explicit repository/user mapping, original-event freshness, durable redelivery, migration, bounds, native AIRC delivery and joined shutdown. [Chosen API and implementation](github-webhook.md); testing is deferred. Deployment requires actual owner-configured IDs, a webhook secret and a GitHub-trusted HTTPS endpoint.
- Verify implemented Twilio Programmable Messaging SMS ingress: exact URL/form HMAC, fixed phone/principal/target routes, encrypted author-signed Send, original freshness, durable redelivery, bounds, native AIRC delivery and joined shutdown. [Chosen API and implementation](twilio-sms.md); testing is deferred. Actual account credentials, numbers and trusted HTTPS routing remain deployment inputs.
- Verify implemented Postmark inbound email: HTTPS Basic authentication, exact email/principal/target routes, encrypted author-signed Send, original freshness, durable redelivery, bounds, native AIRC delivery and joined shutdown. [Chosen API and implementation](postmark-email.md); testing is deferred. Actual inbound address/stream, webhook credentials and trusted HTTPS routing remain deployment inputs.
- Verify implemented OpenSSH SFTP v3 mailbox: pinned host/client keys, exact signed principal/binding/target, whole encrypted results, retained requests, nonoverwriting publication, interrupted-temp recovery, bounds and joined shutdown. [Chosen provider and implementation](sftp.md); testing is deferred. Actual server/account/UID/key provisioning remains a deployment input.
- Verify implemented Amazon S3 object mailbox: SigV4, expected bucket owner, conditional reads/publication, exact signed grants, whole protected results, retained requests, replay, bounds and joined shutdown. [Chosen API and implementation](s3.md); testing is deferred. Actual bucket, IAM/KMS, producer coordination and credentials remain deployment inputs.
- Verify implemented SQLite database mailbox: exact schema and private ownership, transactional conditional publication, exact signed grants, whole protected results, retained Send recovery, lock/crash handling, bounds and joined shutdown. [Chosen API and implementation](sqlite-mailbox.md); testing is deferred. A provisioned private database and coordinated producer remain deployment inputs.
- Verify implemented OpenSSH forced-command SCP upload inbox: dedicated UID/native mapping, exact encrypted signed Send grants, durable final acknowledgement, original freshness and retained replay, bounds, interruption and joined shutdown. [Chosen service and implementation](scp-inbox.md); testing is deferred. A restricted SSH account, authorized keys, host-key pin and configured UID remain deployment inputs.
- Verify implemented vsftpd 3.0.5 FTPS inbox bridge: TLS/login/jail policy, fixed UID/native grants, encrypted Send and whole read results, staged publication, retained recovery, bounds and joined shutdown. [Chosen service and implementation](ftps-inbox.md); testing is deferred. A dedicated account, restricted jail, quota, certificates and producer coordination remain deployment inputs.
- Verify the offline bundle importer through removable media with freshness and replay rejection; blocked on an authorized real device/mounted path: `diskutil list external physical` found no attached external physical disk on 2026-10-06.

## Add broker and event connections

- Verify implemented Amazon SQS Standard ingestion: SigV4 and queue policy checks, encrypted author grants, durable redelivery and expiry recovery, complete protected reads, publish-before-delete, bounds, native AIRC delivery and joined shutdown. [Chosen API and implementation](sqs.md); testing is deferred. Actual AWS credentials, queues, IAM and DLQ provisioning remain deployment inputs.
- Verify implemented Amazon EventBridge Classic custom bus ingestion through an SQS Standard target: exact event filters, encrypted author grants, original freshness, protected whole reads and event correlation, durable redelivery, bounds and joined shutdown. [Chosen API and implementation](eventbridge.md); testing is deferred. Actual bus/rule/queue provisioning, IAM and credentials remain deployment inputs.
- Verify implemented Apache CouchDB 3 change-feed ingress: TLS/Basic authentication, immutable signed Send requests, exact grants, original freshness, durable page checkpoints and replay, source refusals, migration, bounds and joined shutdown. [Chosen API and implementation](couchdb-changes.md); testing is deferred. Database, read account, producer immutability policy and namespace provisioning remain deployment inputs.
- Verify implemented PostgreSQL 18 LISTEN/NOTIFY ingress: verified TLS/client certificates, exact encrypted signed Send grants, original freshness, retained recovery, notification loss, frame/capacity bounds and joined shutdown. [Chosen API and implementation](postgres-notify.md); testing is deferred. Database, role, certificates and authentication policy remain deployment inputs.
- Qualify NATS with a real client and independent native AIRC observer, including pre-intake loss, replay, bounds and shutdown. Native qualification requires an owner-provided NATS client or separate approval to obtain one; none was found in local executables, caches or existing container images on 2026-10-07.
- Qualify Redis Pub/Sub with a real Redis client/server and independent native AIRC observer, including pre-intake loss, replay, bounds and shutdown. No Redis client/server executable or cached Redis image was found on 2026-10-07; obtaining a pinned isolated fixture requires separate owner authorization.
- Qualify Redis Streams with real Redis client/server and independent native AIRC observer, including pending replay, deleted entries, bounds and shutdown. A pinned isolated Redis fixture requires separate owner authorization; no executable or cached image was found on 2026-10-07.
- Qualify Kafka with real producer/broker and independent native AIRC observer, including replay, offset gaps, loss and shutdown. No Kafka executable, client module cache or broker image was found on 2026-10-07; obtaining a pinned isolated fixture requires separate owner authorization.
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

- Add Windows named pipes with verified client identity and tested access controls.
- Identify any required System V architectures beyond qualified Darwin/Linux ARM64; a concrete owner target and runtime fixture are required before extending support.
- Add a Windows RPC bridge with verified caller mapping.
- Add a COM bridge with verified caller mapping.
- Add an Android Binder bridge with verified caller mapping.
- Verify implemented Linux virtio-vsock connectivity through a real host/guest fixture: explicit CID and peer filtering, TLS trust, authenticated send and bounded reads, replay, connection bounds and joined shutdown. [Implementation profile](virtio-vsock.md); testing is deferred. A compatible host/guest pair is still required; Docker's presence or a compile check does not qualify this mode.
- Add Hyper-V socket connectivity through a real host/guest fixture.

## Add network packet connections

- Verify implemented Linux DCCP authenticated connected packet ingress: service code and peer filtering, protected send/whole reads, replay, record/path-MTU bounds, loss and joined shutdown. [Implementation profile](dccp.md); testing is deferred. A DCCP-enabled native fixture is required: the previously probed Linux ARM64 container kernel lacks DCCP support.
- Verify Linux IPv6 multicast socket delivery and native send/read/replay, per-sender trust, destination/interface filtering, unicast-only replies and joined close. Darwin ARM64 is qualified in [multicast-ipv6.md](multicast-ipv6.md#native-gateway-qualification-on-darwin); the isolated Linux fixture lacks a multicast route and NET_ADMIN setup awaits owner authorization.
- Verify IPv4 broadcast socket subnet ownership, destination/interface filtering, unicast-only replies and joined close.
- Verify implemented IPv4 broadcast service ingress with per-sender trust, whole protected unicast replies, replay, bounds and joined shutdown. [Implementation profile](broadcast-ipv4.md); runtime and native AIRC testing are deferred by owner direction.
- Verify the implemented Linux raw-IP protocol-253 ingress with real paired endpoints: signed/encrypted send, whole protected cursor reads, replay, frame and interface bounds, fixed replies and joined shutdown. [Implementation profile](raw-ip.md); testing is deferred by owner direction. A raw-socket-capable isolated fixture is required; no runtime qualification is claimed.
- Verify the implemented Linux IPv4 ICMP paired endpoints: incoming Echo Requests, checksum/channel/interface refusals, encrypted send and whole protected reads, replay, ignored kernel Echo Replies and joined shutdown. [Implementation profile](icmp-v4.md); testing is deferred by owner direction. Darwin unprivileged ping behavior does not qualify this raw-socket mode.
- Verify implemented Linux raw-Ethernet ingress with paired endpoints: interface/MAC/channel/length/padding checks, encrypted send and whole protected reads, replay, fixed unicast replies and joined close. [Implementation profile](raw-ethernet.md); testing is deferred. A permitted link-layer fixture is required; Darwin BPF remains outside this Linux implementation.

## Add device and physical connections

- Verify implemented Linux Bluetooth RFCOMM: fixed controller/channel, kernel security and peer filtering, TLS trust, signed Send and bounded reads, replay, bounds, radio loss and joined shutdown. [Chosen API and implementation](bluetooth-rfcomm.md); testing is deferred. Provisioned BR/EDR devices, authenticated pairing and a framed TLS client remain fixture inputs.
- Verify implemented Linux BR/EDR L2CAP ERTM streams: fixed controller/PSM, peer and security checks, downgrade refusal, MTU writes, TLS trust, signed Send and bounded reads, replay, radio loss and joined shutdown. [Chosen API and implementation](bluetooth-l2cap.md); testing is deferred. Provisioned ERTM-capable devices and pairing remain fixture inputs.
- Verify implemented BlueZ BLE GATT: bus/daemon/device identity, authenticated pairing permissions, bounded request/result paging, encrypted signed Send and whole reads, replay, peer isolation, expiry, loss and joined shutdown. [Chosen API and implementation](ble-gatt.md); testing is deferred. BlueZ policy, paired LE devices and controller advertising remain fixture inputs.
- Add BLE advertising ingress with a verifiable fresh application envelope.
- Add BLE extended-advertising ingress with a verifiable fresh application envelope.
- Add ESP-NOW unicast ingress with provisioned peer trust.
- Add ESP-NOW broadcast ingress with independently verified application authentication.
- Verify implemented ChirpStack 4 HTTP LoRaWAN unconfirmed uplinks: trusted server MIC/counter policy, exact device/native grants, original event freshness, durable redelivery, migration, bounds, native AIRC delivery and joined shutdown. [Chosen API and implementation](chirpstack.md); testing is deferred. A provisioned server, radio device, keys and trusted HTTPS endpoint remain deployment inputs.
- Verify the implemented UART connector through a real physical UART link; Darwin and Linux virtual serial are qualified in [uart.md](uart.md#native-virtual-serial-qualification-on-darwin-and-linux), while electrical behavior and real baud throughput require an authorized device.
- Verify the serial connector over a real RS-232 link.
- Verify the serial connector over a real RS-485 link.
- Verify the serial connector over a real modem link.
- Verify the implemented USB serial profile through a real Linux CDC ACM device: tty ownership, TLS trust, authenticated Send and whole bounded reads, replay, frame limits, unplug/re-enumeration and joined shutdown. [Chosen API and existing implementation](usb-serial.md); testing is deferred. A provisioned CDC ACM peer remains a fixture input.
- Verify the selected Linux CDC-NCM USB bulk bridge with existing framed TLS: real gadget/host bulk data endpoints and path, TLS/native identity, Send and whole reads, replay, unplug/re-enumeration, bounds and joined shutdown. [Chosen API and profile](usb-bulk.md); testing is deferred. A provisioned device-controller board, USB host, cable and NCM network configuration remain fixture inputs.
- Verify implemented Linux vendor HID interrupt-input bundle ingress: exact gadget/report/device permissions, real interrupt IN path, sender signatures and grants, original freshness, durable replay, sequence/file bounds, native AIRC delivery and joined child shutdown. [Chosen API and implementation](usb-interrupt.md); testing is deferred. A provisioned HID gadget, USB host and compatible kernel attributes remain fixture inputs.
- Verify implemented Linux Classical CAN ISO-TP ingress: fixed interface/ID pairs, encrypted signed Send and whole bounded reads, replay, packet/flow-control bounds, bus loss and joined shutdown. [Chosen API and implementation](can-isotp.md); testing is deferred. A provisioned real CAN adapter and compatible peer remain fixture inputs.
- Verify implemented Linux CAN-FD ISO-TP ingress: FD-only frames, fixed interface/ID pairs, encrypted signed Send and whole bounded reads, replay, flow-control bounds, bus loss and joined shutdown. [Chosen API and implementation](canfd-isotp.md); testing is deferred. A provisioned real FD adapter and compatible peer remain fixture inputs.
- Verify the selected Linux SPI/WIZnet W5500 device bridge with existing framed TLS: actual SPI controller/device transfers and network route, TLS/native identity, Send and whole reads, replay, bus/link loss, bounds and joined shutdown. [Chosen API and profile](spi.md); testing is deferred. Provisioned W5500 hardware, board-specific SPI/IRQ/device-tree setup and an Ethernet peer remain fixture inputs.
- Verify the selected Linux I²C/NXP SC16IS752 UART bridge with existing framed TLS: actual I²C transfers and tty selection, TLS/native identity, Send and whole reads, replay, FIFO/bus loss, bounds and joined shutdown. [Chosen API and profile](i2c.md); testing is deferred. Provisioned hardware, board-specific clock/IRQ/device-tree setup and a UART peer remain fixture inputs.
- Verify implemented NFC Type 2/NDEF import using nfcpy 1.0.4 and the signed-bundle service: actual tag/reader, signatures and exact grants, original freshness, durable replay, record/file bounds, native AIRC delivery and child shutdown. [Chosen API and implementation](nfc-ndef.md); testing is deferred. A provisioned compatible USB reader and sufficiently large signed tag remain fixture inputs.
- Verify the selected Linux ATUSB IEEE 802.15.4/6LoWPAN bridge with existing IPv6 framed TLS: actual radio/interface path, TLS/native identity, Send and whole reads, replay, fragmentation/loss, bounds and joined shutdown. [Chosen API and profile](ieee802154.md); testing is deferred. Paired provisioned ATUSB radios, compatible firmware/kernel and radio/IPv6 configuration remain fixture inputs.
- Add Zigbee ingress through a real provisioned radio bridge.
- Add LoRa ingress through a real provisioned radio bridge.
- Verify the selected Linux IEEE 802.11s radio mesh with mac80211/HWMP, SAE peering and existing framed TLS: actual multi-hop radio path, TLS/native identity, Send and whole reads, replay, path loss, bounds and joined shutdown. [Chosen profile](radio-mesh.md); testing is deferred. Provisioned mesh-capable radios, credentials and network configuration remain deployment inputs.
- Verify the implemented InfiniBand carrier profile using Linux IPoIB datagram mode and the existing framed TLS connector: actual fabric/partition/route, TLS/native identity, Send and whole bounded reads, replay, loss, bounds and joined shutdown. [Chosen API and profile](infiniband.md); testing is deferred. Compatible HCAs, subnet-manager provisioning and paired endpoints remain deployment inputs.
- Add RDMA connectivity through real compatible endpoints.
- Verify the selected Linux ThunderboltIP specialized interconnect using `thunderbolt-net` and existing framed TLS: real cable/interface/route, TLS/native identity, Send and whole bounded reads, replay, unplug/reconnect and joined shutdown. [Chosen profile](thunderbolt.md); testing is deferred. Compatible paired hosts, cable and platform/network provisioning remain deployment inputs.
- Verify implemented Darwin camera optical QR-sequence ingress: exact device and permissions, physical capture, sequence/digest/frame bounds, original sender grants and freshness, durable replay, native AIRC delivery and shutdown. [Chosen API and implementation](optical-camera.md); testing is deferred. A provisioned camera and displayed/printed signed sequence remain fixture inputs.
- Verify implemented Linux minimodem/ALSA acoustic signed-bundle ingress: exact device and binary pin, real speaker/microphone path, carrier/frame bounds, original signatures/grants/freshness, durable replay, native AIRC delivery and child shutdown. [Chosen API and implementation](acoustic.md); testing is deferred. A provisioned minimodem 0.24 ALSA build and physical endpoints remain fixture inputs.
- Verify authenticated message ingestion across a real data-diode link without assuming a return path.

## Verify carrier and overlay paths

- Verify an existing IP connector over Ethernet; do not add another transport implementation.
- Verify an existing IP connector over Wi-Fi; do not add another transport implementation.
- Verify an existing IP connector over cellular; retain unavailable network evidence explicitly.
- Verify an existing IP connector over satellite; retain unavailable network evidence explicitly.
- Verify an existing connector through a configured VPN.
- Verify an existing connector through a configured private mesh overlay.
- Verify an existing TCP connector through Tor.
- Verify the selected iodine 0.8.0 DNS carrier with raw UDP fallback disabled: actual permitted DNS path, framed TLS trust, authenticated Send and whole bounded reads, replay, loss, bounds and joined shutdown. [Chosen profile using the existing connector](dns-tunnel.md); testing is deferred. A provisioned paired tunnel, delegated subdomain and permitted resolver/path remain deployment inputs.

- Verify the selected ptunnel-ng 1.42 ICMP carrier: actual permitted raw-ICMP path, restricted proxy destination and client access, framed TLS trust, authenticated Send and whole reads, replay, loss, bounds and joined shutdown. [Chosen profile using the existing connector](icmp-tunnel.md); testing is deferred. Paired tunnel hosts and network permissions remain deployment inputs.

## Choose concrete targets before implementation

These catalogue entries do not name a concrete implementation target. Each decision must produce one named API/protocol/product and one bounded implementation line; do not build a universal adapter or keep searching indefinitely. Ask for the owner's choice when it materially changes scope or access.

- Select one DDS implementation and its authenticated ingress mode; replace this line with its concrete implementation task.
- Select one additional chat-bridge protocol beyond IRC/XMPP/Matrix, or explicitly exclude that open-ended catalogue label.
- Select any required OS message-queue API beyond POSIX/System V, or explicitly exclude that open-ended catalogue label.
- Decide whether outbound webhook callbacks are needed; if so, add one concrete authenticated callback implementation task.

## Final acceptance

- Run the final required acceptance checks on an unchanged source tree and capture their terminal results.
- Update the supported-mode inventory from those results, then remove completed roadmap lines.
