# AIRC gateway

`airc-gateway` lets clients reach an AIRC server (`aircd`) over transports
other than IRC: HTTP, WebSocket, gRPC, MQTT, a Unix socket, a mailbox
directory, a UDP datagram and about twenty more, and carries IRC itself
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
`unix`, `irc_websocket`, `irc_connect`, `grpc`, `mqtt`, `mqtt_websocket`,
`coap_udp`, `zeromq`, `nng` and `udp`. Default ports, TLS reuse and carrier discovery are in
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
| [authentication.md](authentication.md) | Authors, listener access, bound authors, abuse limits |
| [http.md](http.md) | HTTP/1.1 and HTTP/2, JSON-RPC, SSE, WebSocket, MCP, gRPC, HTTP/3, WebTransport, browsers |
| [streams.md](streams.md) | Framed TCP/TLS and Unix streams, stdio and inherited descriptors |
| [native.md](native.md) | Native tunnels: IRC over WebSocket and through HTTP CONNECT, the whole native protocol through the gateway |
| [network.md](network.md) | Datagram fragments, UDP, raw QUIC, DTLS, CoAP, WebRTC, ZeroMQ, NNG |
| [brokers.md](brokers.md) | MQTT 3.1.1 and 5, MQTT over WebSocket |
| [offline.md](offline.md) | File and directory mailboxes, append log |
| [ROADMAP.md](ROADMAP.md) | Open verification work |
| [removed-identity.md](removed-identity.md), [removed-modes.md](removed-modes.md), [removed-carriers.md](removed-carriers.md) | What was removed, and the configuration errors that point here |

## Carriers

`protocol` is the listener's configuration value. Evidence levels:

- **Real client**: a third-party client or server (named) posted through the
  gateway as a guest and as a registered account to a scratch aircd built
  from this tree, checked by an independent `airc history` reader (Linux
  AMD64, 2026-10-11; the run records are in the git history of this
  directory).
- **In-repo**: the repository's tests drive the carrier end to end against
  aircd with Go clients; no third-party implementation was involved.

Carriers with no evidence beyond building, and those nobody is known to
use, were [removed](removed-carriers.md) on 2026-10-26.

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
| IRC through HTTP CONNECT (native tunnel) | `irc_connect` | [native](native.md#irc-through-http-connect) | Real client: WeeChat 4.1.1 ([run](../research/stay-connected-measure-2026-10-17/README.md#an-irc-client-through-the-connect-tunnel)) |
| Unix stream, seqpacket (Linux), datagram | `unix`, `unixpacket`, `unixgram` | [streams](streams.md#unix-sockets) | In-repo |
| stdio, socket pair, inherited FD | `stdio`, `socketpair`, `inherited` | [streams](streams.md#parent-owned-streams) | In-repo |
| UDP | `udp` | [network](network.md#udp) | In-repo |
| Raw QUIC streams, datagrams | `rawquic`, `rawquic_datagrams` | [network](network.md#raw-quic) | In-repo |
| DTLS datagrams | `dtls` | [network](network.md#dtls) | In-repo |
| CoAP over UDP | `coap_udp` | [network](network.md#coap) | Real client: aiocoap (CON and NON) |
| CoAP over DTLS | `coaps` | [network](network.md#coap) | In-repo |
| WebRTC DataChannels (four modes) | `webrtc` | [network](network.md#webrtc) | In-repo (loopback) |
| ZeroMQ REQ/REP | `zeromq` | [network](network.md#zeromq-and-nng) | Real client: pyzmq 27.2 |
| NNG REQ0/REP0 | `nng` | [network](network.md#zeromq-and-nng) | Real client: pynng |
| MQTT 3.1.1 and 5 | `mqtt` | [brokers](brokers.md#mqtt) | Real clients: mosquitto 2.0.18 (3.1.1 and 5), paho-mqtt 2.1.0 (5) |
| MQTT over WebSocket | `mqtt_websocket` | [brokers](brokers.md#mqtt-over-websocket) | Real clients: MQTT.js 5.16.0 in Chromium 141, paho-mqtt 2.1.0 |
| File, directory mailbox, append log | `file_mailbox`, `directory_mailbox`, `append_log` | [offline](offline.md#local-mailboxes) | In-repo |

## Adding a carrier

Each carrier is one descriptor in
[`internal/gateway/config/transports.go`](../../internal/gateway/config/transports.go):
kind (stream, packet or process), network, address shape, TLS
policy, author model, platforms, and the listener options it owns (their
validators run from `listener_blocks.go`). Configuration validation, the
`capabilities` report, the dispatcher's author policy and
`airc service install --gateway` read it. `serve` starts the carrier through
its entry in
[`internal/gateway/service/owners.go`](../../internal/gateway/service/owners.go);
a process carrier names its own `airc-gateway` command instead.
`TestOwnersCoverTheRegistry` fails until
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
