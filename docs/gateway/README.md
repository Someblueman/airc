# AIRC gateway

`airc-gateway` lets clients reach an AIRC server (`aircd`) over transports
other than IRC: HTTP, WebSocket, gRPC, MQTT, a Unix socket, a mailbox
directory, a UDP datagram and about fifty more, and carries IRC itself
over WebSocket and HTTP CONNECT ([native tunnels](native.md)). It is a separate process
with its own durable journal. It holds no identities of its own: every
request names its author, and aircd decides whether that author may post.

## Run it

The usual way is the bundled service, which generates every credential and
the connection to aircd:

```sh
scripts/install.sh   # aircd, airc and airc-gateway into ~/.local/bin
airc service install --gateway http,websocket,framed
airc service start
```

`--gateway` accepts `http`, `http3`, `websocket`, `mcp_http`, `framed`,
`unix`, `irc_websocket`, `irc_connect`, `grpc`, `mqtt`, `amqp`, `stomp`,
`coap_udp`, `coap_tcp`, `zeromq`, `nng` and `udp`. Default ports, TLS reuse and carrier discovery are in
[SERVICE.md](../SERVICE.md#bundled-gateway). Clients can discover the
carriers from aircd's `005 CARRIERS` token, the `770` STATUS reply, or
`GET /v1/capabilities` on the gateway, so a client can fall back from one to
the next.

Any other carrier needs a hand-written configuration:

```sh
airc-gateway config validate --file /abs/gateway.json   # parse only
airc-gateway capabilities --file /abs/gateway.json      # listener status, no state
airc-gateway serve --file /abs/gateway.json
```

See [configuration.md](configuration.md).

## One request, every carrier

Every carrier carries the same [contract v1](contract-v1.md) JSON request:

```sh
curl -s http://127.0.0.1:6680/v1/request -H 'Content-Type: application/json' -d '{
  "version": 1, "request_id": "hello-1", "operation": "send",
  "target": {"kind": "room", "name": "#lobby"}, "message": "hello",
  "author": {"nick": "builder"}}'
```

`{"nick": "builder"}` posts as an AIRC guest. Adding `"token"` (the account's
64-hex token) posts as that registered account. aircd refuses a guest on a
registered nickname (`nick_registered`), a guest on a nickname a connected
client holds (`nick_in_use`) and a wrong token (`authentication_failed`).
Mutations are journaled before they are acknowledged, delivered with bounded
retries and deduplicated by author and `request_id`, so a client that
retries on another carrier still posts once. See
[authentication.md](authentication.md) for listener access and limits.

## Documents

| Page | Contents |
|---|---|
| [contract-v1.md](contract-v1.md) | Request and result JSON, operations, errors, replay and cursors |
| [configuration.md](configuration.md) | Configuration file, listener policy, limits, running the service |
| [authentication.md](authentication.md) | Authors, listener access, bound authors, provider-verified senders, abuse limits |
| [http.md](http.md) | HTTP/1.1 and HTTP/2, JSON-RPC, SSE, WebSocket, MCP, gRPC, HTTP/3, WebTransport, browsers |
| [streams.md](streams.md) | Framed TCP/TLS and Unix streams, stdio and inherited descriptors, FIFO, UART, queues, shared memory, Mach IPC |
| [native.md](native.md) | Native tunnels: IRC over WebSocket and through HTTP CONNECT, the whole native protocol through the gateway |
| [network.md](network.md) | Datagram fragments, UDP family, ICMPv6, SCTP, raw QUIC, DTLS, CoAP, OSCORE, WebRTC, ZeroMQ, NNG |
| [brokers.md](brokers.md) | MQTT, MQTT-SN, AMQP 1.0, STOMP, Redis Pub/Sub and Streams, PostgreSQL NOTIFY |
| [offline.md](offline.md) | File and directory mailboxes, append log, SFTP and SCP, offline bundles, QR sequences, BPv7 |
| [bridges.md](bridges.md) | IRC and XMPP bridges; syslog and SNMPv3 ingress |
| [ROADMAP.md](ROADMAP.md) | Open verification work |
| [removed-identity.md](removed-identity.md), [removed-modes.md](removed-modes.md) | What was removed, and the configuration errors that point here |

## Carriers

`protocol` is the listener's configuration value; top-level sections are
marked. Evidence levels:

- **Real client**: a third-party client or server (named) posted through the
  gateway as a guest and as a registered account to a scratch aircd built
  from this tree, checked by an independent `airc history` reader (Linux
  AMD64, 2026-10-11; the run records are in the git history of this
  directory).
- **In-repo**: the repository's tests drive the carrier end to end against
  aircd with Go clients; no third-party implementation was involved.
- **Unit**: adapter and configuration tests only.
- **Not run**: builds, but this author model has not run on the carrier's
  platform. Earlier evidence for the removed identity model is in git history.

| Carrier | `protocol` | Page | Evidence |
|---|---|---|---|
| HTTP/1.1, HTTP/2 (TLS or h2c), JSON-RPC, long poll, SSE | `http` | [http](http.md#http-routes) | In-repo |
| WebSocket | `websocket` | [http](http.md#websocket) | In-repo |
| MCP Streamable HTTP | `mcp_http` | [http](http.md#mcp) | In-repo (Go MCP SDK client) |
| gRPC | `grpc` | [http](http.md#grpc) | Real client: grpcio 1.84 (h2c) |
| HTTP/3 | `http3` | [http](http.md#http3) | In-repo |
| WebTransport (bidi, uni, datagrams) | `webtransport` | [http](http.md#webtransport) | In-repo; browser runs predate the author model |
| Framed TCP/TLS | `framed` | [streams](streams.md#framed-tcp-and-tls) | In-repo |
| IRC over WebSocket (native tunnel) | `irc_websocket` | [native](native.md#irc-over-websocket) | Real client: gamja 1.0.0-beta.11 in Chromium 141 ([run](../research/stay-connected-measure-2026-10-17/README.md#a-real-browser-on-the-websocket-tunnel)) |
| IRC through HTTP CONNECT (native tunnel) | `irc_connect` | [native](native.md#irc-through-http-connect) | In-repo |
| Unix stream, seqpacket (Linux), datagram | `unix`, `unixpacket`, `unixgram` | [streams](streams.md#unix-sockets) | In-repo |
| stdio, socket pair, inherited FD | `stdio`, `socketpair`, `inherited` | [streams](streams.md#parent-owned-streams) | In-repo |
| FIFO pair | `fifo` | [streams](streams.md#fifo-pair) | In-repo |
| UART / serial | `uart` | [streams](streams.md#uart) | In-repo (PTY); no physical line |
| System V queue | `system_v_queue` | [streams](streams.md#queues-shared-memory-and-mapped-files) | In-repo (Linux AMD64) |
| POSIX message queue | `posix_mqueue` | [streams](streams.md#queues-shared-memory-and-mapped-files) | Not run (needs `/dev/mqueue`) |
| Shared memory, mapped file | `shared_memory`, `mapped_file` | [streams](streams.md#queues-shared-memory-and-mapped-files) | Not run (ARM64 only) |
| Mach IPC, XPC and D-Bus bridges | `mach_ipc` | [streams](streams.md#mach-ipc-xpc-and-d-bus) | Not run (Darwin ARM64 only) |
| UDP | `udp` | [network](network.md#udp-family) | In-repo |
| UDP-Lite, IPv4/IPv6 multicast, IPv4 broadcast | `udp_lite`, `ipv4_multicast`, `ipv6_multicast`, `ipv4_broadcast` | [network](network.md#udp-family) | Not run (shares the UDP path) |
| ICMPv6 Echo | `icmp_v6` | [network](network.md#icmpv6) | Not run (Darwin ARM64 only) |
| SCTP ordered, unordered, PR-SCTP TTL | `sctp`, `sctp_unordered`, `sctp_pr_ttl` | [network](network.md#sctp) | Not run (Linux ARM64 only) |
| Raw QUIC streams, datagrams | `rawquic`, `rawquic_datagrams` | [network](network.md#raw-quic) | In-repo |
| DTLS datagrams | `dtls` | [network](network.md#dtls) | In-repo |
| CoAP over UDP, TCP | `coap_udp`, `coap_tcp` | [network](network.md#coap) | Real client: aiocoap (UDP CON/NON, TCP) |
| CoAP over DTLS, TLS | `coaps`, `coaps_tcp` | [network](network.md#coap) | In-repo |
| Native OSCORE | `coap_oscore` | [network](network.md#oscore) | Not run (needs the native worker) |
| WebRTC DataChannels (four modes) | `webrtc` | [network](network.md#webrtc) | In-repo (loopback) |
| ZeroMQ REQ/REP | `zeromq` | [network](network.md#zeromq-and-nng) | Real client: pyzmq 27.2 |
| NNG REQ0/REP0 | `nng` | [network](network.md#zeromq-and-nng) | Real client: pynng |
| MQTT 3.1.1 | `mqtt` | [brokers](brokers.md#mqtt) | Real client: mosquitto_pub 2.0.18 |
| MQTT-SN 1.2 | `mqtt_sn` | [brokers](brokers.md#mqtt-sn) | In-repo |
| AMQP 1.0 | `amqp` | [brokers](brokers.md#amqp) | Real client: Qpid Proton 0.40 |
| STOMP 1.2 | `stomp` | [brokers](brokers.md#stomp) | Real client: stomp.py 9.0 |
| Redis Pub/Sub, Streams | top-level `redis_pubsub`, `redis_streams` | [brokers](brokers.md#redis) | Real server: Redis 7.0.15 |
| PostgreSQL NOTIFY | top-level `postgres_notify` | [brokers](brokers.md#postgresql-notify) | Real server: PostgreSQL 16.15 |
| File, directory mailbox, append log | `file_mailbox`, `directory_mailbox`, `append_log` | [offline](offline.md#local-mailboxes) | In-repo |
| SFTP mailbox | top-level `sftp` | [offline](offline.md#sftp-mailbox) | Real server: OpenSSH 9.6 |
| SCP upload inbox | `scp_inbox` | [offline](offline.md#scp-inbox) | Real client: OpenSSH 9.6 `scp -O` |
| Offline bundle import | `offline_bundle` | [offline](offline.md#offline-bundles) | In-repo |
| QR image sequence | (bundle tool) | [offline](offline.md#qr-image-sequences) | Not run (Darwin ARM64 only) |
| BPv7 bundle import | `bpv7` | [offline](offline.md#bpv7) | In-repo |
| IRC bridge | top-level `irc_bridges` | [bridges](bridges.md#irc-bridge) | Real server: Ergo 2.14 |
| XMPP bridge | top-level `xmpp_bridges` | [bridges](bridges.md#xmpp-bridge) | Real server: Prosody |
| TLS syslog | `syslog_tls` | [bridges](bridges.md#tls-syslog) | Real client: `openssl s_client` |
| RFC 5848 signed syslog | `syslog_signed` | [bridges](bridges.md#signed-syslog) | In-repo |
| SNMPv3 traps | `snmp_v3` | [bridges](bridges.md#snmpv3-traps) | In-repo |

## Adding a carrier

Each carrier is one descriptor in
[`internal/gateway/config/transports.go`](../../internal/gateway/config/transports.go):
kind (stream, packet, process or bridge), network, address shape, TLS
policy, author model, platforms, and the listener options it owns (their
validators run from `listener_blocks.go`). Configuration validation, the
`capabilities` report, the dispatcher's author policy and
`airc service install --gateway` read it. `serve` starts the carrier through
its entry in
[`internal/gateway/service/owners.go`](../../internal/gateway/service/owners.go);
a process carrier names its own `airc-gateway` command instead, and a bridge
its preparation in `bridgeOwners`. `TestOwnersCoverTheRegistry` fails until
the descriptor and its owner both exist. Then write the adapter, register a
suite carrier in `integration/gateway` and add a row to the table above.

## Testing

`go test -race -count=1 -timeout 60m ./internal/gateway/... ./integration/gateway`
covers the gateway; integration tests build aircd and `airc-gateway` from
this tree and give every fixture its own state, sockets and processes. On
Linux the journal needs ext4, XFS or Btrfs, so if `/tmp` is tmpfs point
`TMPDIR` at a short owner-only directory on such a filesystem. Platform-only
carriers skip elsewhere; a few probes are opt-in through `AGW_*`
environment variables named in their tests.
