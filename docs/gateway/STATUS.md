# Connection gateway inventory

Remaining work lives only in [ROADMAP.md](ROADMAP.md). The [72-entry catalogue reconciliation](catalogue-evidence.md) maps qualified submodes and exact unresolved evidence boundaries. This file is a concise implementation inventory, not a second task list or a running acceptance log.

## Recorded mode evidence

This inventory maps source-specific evidence in the linked specifications; it
is not fresh certification of every mode. Full normal/race acceptance and build,
formatting, vet, staticcheck and diff gates passed at Go/test/assembly/module source
`8fb90d17…` ([exact checkpoint](testing.md#udp-lite-service-activation-acceptance)).
Each normal/race run recorded 4,468 pass entries and 68 helper/opt-in/platform skips.
Linux UDP-Lite protected startup/capabilities passed; complete native mode
qualification remains open.
IPv4 multicast native send, whole cursor/history replies, replay, refusal, fixed
unicast reply routing, capacity recovery and joined shutdown passed on
Darwin/Linux ARM64 loopback fixtures. IPv6 multicast and broadcast qualification remain open.
The configured System V listener and service lifecycle remain qualified
on Darwin/Linux ARM64, with schema 18 ownership and verified startup/restart.
Actual System V/native AIRC delivery, whole protected cursor/history/watch replies
and joined observer shutdown passed on Darwin and Linux ARM64. Queue pressure,
replacement and pre-admission/accepted/pending-native crash recovery also passed.
Linux AMD64 System V support and schema-23 migration are implemented with testing deferred. Matrix service ownership, durable Send recovery and encrypted bounded reads
are implemented with testing deferred; real-client/native qualification remains open.
Existing IRC send owners retain scripted native delivery evidence;
encrypted bounded replies are implemented with testing deferred, and real IRC
server/client qualification remains open.
The earlier AMQP checkpoint's CoAP SQLite crash remains documented there.
This source includes NNG, ZeroMQ, Kafka, Redis Streams, Redis Pub/Sub and NATS service integration, whose real-client/native
qualification remains outstanding, plus SNMPv3 traps, signed syslog, TLS syslog, STOMP, AMQP, MQTT-SN and MQTT TLS ingestion, BPv7 ingestion
and the Linux native queue tests;
actual Linux runtime evidence is recorded separately without Linux race
instrumentation. The skips do not qualify remaining browser, worker or physical
paths.
Exact results and limitations are in [testing.md](testing.md)
and the linked specifications. Mode qualification remains individually open in
the roadmap until its complete acceptance boundary is audited and verified.

The rows below distinguish modes even when they share an implementation. Local
evidence primarily uses real Go clients and private Darwin native fixtures with
independent AIRC observers. A compiled adapter or skipped opt-in probe is not
runtime evidence.

| Mode | Recorded local exercise / specification | Evidence limit |
|---|---|---|
| Discord | [Bot REST v10 signed-bundle bridge](discord.md) | Send-only implementation; actual API permissions, publication/retrieval and AIRC testing deferred |
| DDS | [Cyclone DDS 0.10.5 signed-bundle ingress](dds.md) | Send-only implementation; native SDK/IDL build, real DDS discovery/delivery and AIRC testing deferred |
| Windows RPC | [Local ncalrpc workers and framed TLS supervisor](windows-rpc.md) | Implementation reviewed and Go cross-built; native SDK worker build, Windows identity/access and AIRC testing deferred |
| Windows COM | [Local IDispatch workers and SID-verified bootstrap](windows-com.md) | Source-reviewed and Go cross-built; native SDK build, COM marshalling/caller admission and AIRC testing deferred |
| Android Binder | [Bound service and companion client app](android-binder.md) | Source-reviewed implementation with current APK signer/UID admission; Android SDK/Gradle build and device/AIRC testing deferred |
| Hyper-V sockets | [Windows guest TLS and fixed host relay](hyper-v-sockets.md) | Go implementation; actual Hyper-V registration, host/guest peer filtering, relay shutdown and native AIRC testing deferred |
| Windows named pipes | [Local Win32 pipe and fixed framed TLS bridge](windows-named-pipes.md) | SID/DACL admission, Send and bounded reads implemented; Windows, access-control and native AIRC testing deferred |
| Direct RDMA | [Linux rdma-core stream-rsocket relay with framed TLS](rdma.md) | Bounded one-connection relay implemented; native ABI/provider, real fabric and AIRC testing deferred |
| ESP-NOW broadcast | [ESP32/MicroPython broadcast and signed-bundle import](espnow-broadcast.md) | Group-keyed Send-only implementation with independent native author verification; multi-receiver RF and native AIRC testing deferred |
| ESP-NOW unicast | [ESP32/MicroPython UART bridge and signed-bundle import](espnow-unicast.md) | Peer-keyed Send-only implementation; actual firmware, radio, watchdog and native AIRC testing deferred |
| BLE extended advertising | [Linux HCI extended signed-bundle fragments](ble-extended-advertising.md) | Send-only helper implementation; actual Bluetooth 5 extended reports, radio path and native AIRC testing deferred |
| BLE legacy advertising | [Linux HCI user-channel signed-bundle fragments](ble-advertising.md) | Send-only helper implementation; actual legacy RF path, controller cleanup and native AIRC testing deferred |
| LoRa | [Linux REYAX RYLR998 commands and signed-bundle import](lora.md) | Send-only helper implementation; actual RF path, timing and native AIRC testing deferred |
| Zigbee | [Linux Digi XBee 3 API2 and signed-bundle import](zigbee.md) | Send-only helper implementation; actual radio, UART cleanup and native AIRC testing deferred |
| Acoustic | [Linux minimodem Bell 202/ALSA and signed-bundle import](acoustic.md) | Send-only helper implementation; real sound path, native dependency and AIRC testing deferred |
| Optical QR camera | [Darwin AVFoundation/Vision capture and signed-bundle import](optical-camera.md) | Send-only implementation; camera permissions, physical optical and native AIRC testing deferred |
| I²C | [Linux NXP SC16IS752 UART bridge with framed TLS](i2c.md) | Existing connector applies; actual I²C-device transfer testing deferred |
| USB interrupt | [Fixed vendor HID input reports and signed-bundle import](usb-interrupt.md) | Send-only helper implementation; actual USB, authentication/replay and native AIRC testing deferred |
| SPI | [Linux WIZnet W5500 bridge with framed TLS](spi.md) | Existing connector applies; actual SPI-device transfer testing deferred |
| USB bulk | [Linux CDC-NCM gadget/host with framed TLS](usb-bulk.md) | Existing connector applies; real bulk-endpoint path testing deferred |
| IEEE 802.15.4 | [Linux ATUSB and 6LoWPAN with framed TLS](ieee802154.md) | Existing IPv6 connector applies; real radio-path testing deferred |
| NFC / NDEF | [Type 2 USB reader and signed-bundle import](nfc-ndef.md) | Send-only implementation; physical reader, signatures/replay and native AIRC testing deferred |
| Bluetooth RFCOMM | [Linux peer-filtered framed TLS listener](bluetooth-rfcomm.md) | Send and bounded reads implemented; testing deferred, no radio, pairing or native AIRC qualification |
| Bluetooth L2CAP | [Linux BR/EDR ERTM framed TLS listener](bluetooth-l2cap.md) | Send and bounded reads implemented; testing deferred, no device negotiation or native AIRC qualification |
| Additional radio mesh | [Linux IEEE 802.11s with framed TLS](radio-mesh.md) | Concrete SAE mesh profile uses existing connector; real multi-hop testing deferred |
| Specialized interconnect | [Linux ThunderboltIP with framed TLS](thunderbolt.md) | Concrete interconnect profile uses existing connector; real cable testing deferred |
| InfiniBand | [Linux IPoIB datagram mode with framed TLS](infiniband.md) | Existing connector implementation applies; real fabric testing deferred |
| ICMP tunnel | [ptunnel-ng 1.42 with the existing framed TLS connector](icmp-tunnel.md) | Concrete carrier profile documented; actual ICMP-path testing deferred |
| DNS tunnel | [iodine 0.8.0 with the existing framed TLS connector](dns-tunnel.md) | Concrete carrier profile documented; actual DNS-path testing deferred |
| LoRaWAN unconfirmed uplinks | [ChirpStack 4 HTTP JSON integration](chirpstack.md) | Fixed device-mapped Send implemented; radio, provider and native AIRC testing deferred |
| CAN-FD | [Linux SocketCAN ISO-TP with FD frame selection](canfd-isotp.md) | Send and bounded reads implemented; real FD adapter and native AIRC testing deferred |
| Classical CAN | [Linux SocketCAN ISO-TP and protected AGD1 packets](can-isotp.md) | Send and bounded reads implemented; real CAN adapter and native AIRC testing deferred |
| USB serial | [Linux CDC ACM through the existing framed TLS tty path](usb-serial.md) | Send and bounded reads implemented by `serve-uart`; real USB-device testing deferred |
| BLE GATT | [BlueZ paired-device request and protected result characteristics](ble-gatt.md) | Encrypted signed Send and bounded reads implemented; testing deferred, no BlueZ, pairing or native AIRC qualification |
| OpenSSH SFTP mailbox | [Pinned SSH account mailbox and whole encrypted results](sftp.md) | Authenticated Send and bounded reads implemented; testing deferred, no OpenSSH or native AIRC qualification |
| OpenSSH SCP upload inbox | [Forced-command UID mapping and durable acknowledgement](scp-inbox.md) | Encrypted signed Send implemented; testing deferred, no OpenSSH or native AIRC qualification |
| vsftpd FTPS inbox | [Private UID bridge and whole encrypted results](ftps-inbox.md) | Linux Send and bounded reads implemented; testing deferred, no vsftpd or native AIRC qualification |
| Amazon S3 object mailbox | [Conditional object publication and protected results](s3.md) | Fixed principal/target Send and bounded reads implemented; testing deferred, no S3 or native AIRC qualification |
| SQLite database mailbox | [Single-slot transactions and protected results](sqlite-mailbox.md) | Fixed principal/target Send and bounded reads implemented; testing deferred, no SQLite producer or native AIRC qualification |
| CouchDB change-feed ingress | [Immutable signed requests and durable page checkpoints](couchdb-changes.md) | Read-only, Send-only subscription implemented; testing deferred, no CouchDB or native AIRC qualification |
| PostgreSQL notification ingress | [Certificate-authenticated LISTEN/NOTIFY](postgres-notify.md) | Ephemeral encrypted signed Send subscription implemented; testing deferred, no PostgreSQL or native AIRC qualification |
| Twilio inbound SMS | [Provider-signed webhook and encrypted author intent](twilio-sms.md) | Fixed phone routes and durable Send implemented; testing deferred, no Twilio or native AIRC qualification |
| Postmark inbound email | [HTTPS webhook and encrypted author intent](postmark-email.md) | Fixed email routes and durable Send implemented; testing deferred, no Postmark or native AIRC qualification |
| Amazon SQS Standard | [Encrypted request and protected result queues](sqs.md) | Implementation with fixed principal/target routes, SigV4 and durable redelivery; testing deferred, no AWS or native AIRC qualification |
| Amazon EventBridge Classic | [Custom event bus through SQS](eventbridge.md) | Explicit event envelope, fixed grants and protected whole results implemented; testing deferred, no AWS or native AIRC qualification |
| Matrix bridge | [Visible-history recovery and encrypted whole replies](matrix-bridge.md) | Service ownership, durable Send and bounded authenticated reads implemented; testing deferred, no real-homeserver or native AIRC qualification |
| XMPP bridge | [Signed MUC requests and encrypted whole replies](xmpp-bridge.md) | Service ownership, signed admission and bounded encrypted replies implemented; testing deferred, no real-server or native AIRC qualification |
| IRC bridge | [Account-mapped sends and encrypted bounded reads](irc-bridge.md) | Prior sends have scripted native evidence; encrypted replies are compiled with testing deferred; real IRC server/client qualification remains open |
| Framed TCP IPv4 | [Authenticated durable sends, independent observer, stable replay and bounded cursor pages](framed-ipv4-testing.md) | Loopback Darwin |
| Framed TCP IPv6 | [Authenticated sends, native observer and bounded cursor resume](framed-ipv6-testing.md) | Loopback Darwin |
| Framed TLS | [Verified CA/name, signed/encrypted sends, native observer and cursor pages](framed-tls-testing.md) | Local certificates and clients |
| MQTT 3.1.1 TLS | [Signed publisher/topic authority, durable PUBACK, exact native delivery, lost first acknowledgement/reconnect replay and joined shutdown](mqtt.md) | Publish-only QoS 1 with fixed literal grants; Darwin and Linux ARM64 loopback clients, no Linux race instrumentation or general subscription-broker claim |
| MQTT-SN 1.2 connectionless ingestion | [Encrypted signed publisher/topic authority, deliberate pre-intake loss, native delivery, durable replay and joined shutdown](mqtt-sn.md) | QoS -1 predefined topics, encrypted signed sends and no success ACK; Darwin race and Linux ARM64 loopback clients, no Linux race instrumentation or physical radio claim |
| AMQP 1.0 TLS | [Signed fixed-link dispatch, durable dispositions, whole bounded reads, loss/restart replay and native observer](amqp.md) | Darwin full normal/race acceptance and Linux ARM64 selected profile checks passed; no general-broker or Linux race claim |
| STOMP 1.2 TLS | [Signed fixed-destination dispatch, durable receipts, individual ACK retention, whole bounded reads, loss/restart replay and native observer](stomp.md) | Darwin full normal/race acceptance and Linux ARM64 selected profile checks passed; no general-broker or Linux race claim |
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
| Virtio-vsock | [TLS framed host/guest connections](virtio-vsock.md) | Linux implementation with explicit CID/port and peer allowlist; testing deferred, no native host/guest or AIRC qualification |
| DCCP | [Authenticated connected packet ingress](dccp.md) | Linux IPv4 implementation with fixed service code and protected peer routes; testing deferred, no native DCCP or AIRC qualification |
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
| Data-diode file ingress | [One-way signed-bundle file delivery](data-diode.md) | Uses the existing authenticated importer with receiving-side results; actual diode and native path acceptance deferred |
| QR image-sequence import | [Native PNG encoding/decoding, signed-bundle import, exact native delivery and durable replay rejection](qr-sequence.md#native-qualification) | Darwin ARM64 CoreImage/Vision clients, bounded complete sequences, original freshness and authentication, image/file refusals and process deadlines; full normal/race/static acceptance passed; send-only local image files, no camera or physical optical qualification |
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
| IPv4 ICMP encapsulation | [Encrypted paired Echo Requests and fixed protected replies](icmp-v4.md) | Linux raw-socket implementation; testing deferred, no runtime or native AIRC qualification |
| Raw IPv4 protocol 253 | [Encrypted packet ingress and fixed protected replies](raw-ip.md) | Linux implementation with explicit interface and channel framing; testing deferred, no runtime or native AIRC qualification |
| Raw Ethernet EtherType 0x88B5 | [Encrypted link-layer ingress and fixed unicast replies](raw-ethernet.md) | Linux packet-socket implementation with explicit interface/MAC/channel; testing deferred, no runtime or native AIRC qualification |
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
