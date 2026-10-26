# Removed carriers

On 2026-10-26 the gateway was cut down to the carriers in the
[carriers table](README.md#carriers): HTTP and its relatives, the framed
and Unix streams, the parent-owned streams, the native IRC tunnels, the
local mailboxes, MQTT, CoAP over UDP and DTLS, ZeroMQ, NNG, UDP, DTLS, raw
QUIC and WebRTC. Everything else was removed, with its adapters, codecs,
configuration, journal tables, process subcommands, native helpers, build
scripts, test fixtures and documents; the commits before the one that adds
this note keep them in git history.

| Group | Removed carriers |
|---|---|
| Local streams and IPC | FIFO pair (`fifo`), UART (`uart`), System V queue (`system_v_queue`), POSIX message queue (`posix_mqueue`), shared memory (`shared_memory`), mapped file (`mapped_file`), Mach IPC with the XPC and D-Bus bridges (`mach_ipc`) |
| Packet carriers | UDP-Lite (`udp_lite`), IPv4 and IPv6 multicast (`ipv4_multicast`, `ipv6_multicast`), IPv4 broadcast (`ipv4_broadcast`), ICMPv6 Echo (`icmp_v6`) |
| SCTP | `sctp`, `sctp_unordered`, `sctp_pr_ttl` |
| CoAP | CoAP over TCP and TLS (`coap_tcp`, `coaps_tcp`), the native OSCORE worker (`coap_oscore`, `oscore init`) |
| Brokers and databases | MQTT-SN (`mqtt_sn`), AMQP 1.0 (`amqp`), STOMP 1.2 (`stomp`), Redis Pub/Sub and Streams (`redis_pubsub`, `redis_streams`), PostgreSQL NOTIFY (`postgres_notify`) |
| Files carried by hand | SFTP mailbox (`sftp`), SCP inbox (`scp_inbox`), offline bundles (`offline_bundle`, `import-bundle`), the QR image-sequence tool, BPv7 (`bpv7`, `import-bpv7`) |
| Bridges and provider ingress | IRC bridge (`irc_bridges`), XMPP bridge (`xmpp_bridges`), TLS syslog (`syslog_tls`), RFC 5848 signed syslog (`syslog_signed`), SNMPv3 traps (`snmp_v3`) |

With them went the whole bridge kind (the gateway is no longer a client of
another service), route authors (senders verified by a provider protocol),
the `reply_to` listener option and the `bound_author` of anything but a
mailbox.

They were removed for one of two reasons, which the carriers table's
evidence column recorded before this change. Most had never run on any
machine in the author model: the Darwin ARM64 and Linux ARM64 carriers
(Mach IPC, XPC, D-Bus, QR sequences, shared memory, mapped files, ICMPv6,
SCTP), the POSIX queues, UDP-Lite, multicast, broadcast and the native
OSCORE worker. The rest had run only in this repository's tests or once
against a scratch server or client, and none has a known user; keeping
them meant carrying their protocol stacks, native code and platform-only
test fixtures for no one. A physical link that reaches a remaining carrier
over IP, such as a serial adapter with a TCP bridge, still works as that
carrier.

## Existing configuration

A configuration that names a removed carrier fails to load with one error
naming the first such field and this note, whether the listener is enabled
or not: a listener whose `protocol` is one of the names above, a listener
with an option object only a removed carrier had (`fifo`, `udp_lite`,
`ipv4_multicast`, `ipv6_multicast`, `ipv4_broadcast`, `icmp_v6`, `snmp`,
`signed_syslog`, `syslog`, `stomp`, `amqp`, `mqtt_sn`, `bundle_protocol`,
`posix_queue`, `shared_memory`, `mach_ipc`, `oscore`, `scp_inbox`) or
`reply_to`, or one of the top-level sections `redis_pubsub`,
`redis_streams`, `irc_bridges`, `xmpp_bridges`, `sftp` and
`postgres_notify`. Remove the listener or section; the remaining carriers
take the same requests.

`airc service install --gateway` no longer accepts `amqp`, `stomp` or
`coap_tcp`. The `serve-fifo`, `serve-uart`, `scp-inbox`, `import-bundle`,
`import-bpv7` and `oscore init` subcommands of `airc-gateway` no longer
exist, nor do `scripts/build-oscore.sh`, `build-qr-tool.sh`,
`build-xpc-bridge.sh` and `build-dbus-bridge.sh`.

## Journal compatibility

The journal schema is now version 28: the seven tables that only the
removed carriers used (OSCORE replay state, POSIX and System V queue
ownership, signed syslog state and the three SNMP tables) are gone. The
gateway was never deployed, so nothing is migrated: a journal from an
earlier schema refuses to open and is left unchanged; see
[configuration](configuration.md#journal).
