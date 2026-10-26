# Network carriers

Packet, QUIC, CoAP, WebRTC and message-socket carriers. All take the
[contract](contract-v1.md) request, usually inside a
[frame](streams.md#frames), and apply the listener access policy. AIRC sees
the client's IP address (without port) as its source. Connection-oriented
carriers (a QUIC connection, a DTLS association, a ZeroMQ or NNG
connection, a WebRTC peer) are one author session, where
`account.identify` sets the default author; datagram carriers have no
session, so every request names its author. Cleartext carriers expose
tokens to anyone on the path, as plain IRC does.

## Datagram fragments

Datagram carriers split a frame into datagrams of at most 1000 bytes:

| Bytes | Field |
|---|---|
| 4 | wire tag `AGD1` |
| 16 | random message ID |
| 4 | total frame length, big-endian |
| 2 | fragment index from 0, big-endian |
| 2 | fragment count, big-endian |
| ≤ 972 | payload slice |

The receiver reassembles regardless of order and identical duplicates;
conflicting fragments discard the frame. Requests reassemble to at most
16 KiB and responses to 64 KiB. At most four incomplete frames are kept per
source (or session), each for five seconds; completed IDs are remembered
for thirty seconds (64 entries). Responses carry a per-correlation
`sequence` from 1; a watch client must stop advancing its cursor at a gap.
Datagrams are unreliable: retry a mutation with its original `request_id`
under a new correlation and message ID, then read its receipt.

Replies go to the request's source. To keep a spoofed source from turning
the gateway into an amplifier, a reply to a non-loopback source may use at
most three times the request's wire bytes plus one 512-byte error frame; pad
a request with JSON whitespace to make room for a larger page.

## UDP

`protocol: "udp"` listens on a numeric IP and port (wildcard and port 0
allowed), on every platform, with no options. Each request is a fragmented
frame; replies go to the source. Calls last four seconds and streamed
watches twenty; idle sources expire after five seconds; invalid input is
dropped without reply.

## Raw QUIC

`rawquic` (streams) and `rawquic_datagrams` (streams plus QUIC datagrams)
listen on UDP with TLS 1.3 and ALPN `airc-gateway-quic/1`; a certificate
is required. A resumed client may send 0-RTT, but nothing it sends is served
until the handshake completes: early data can be replayed and its source
address is unproven, so it would bypass the per-source limits. Bidirectional streams carry length-prefixed
frames; a client unidirectional stream gets responses on a server
unidirectional stream; datagrams carry fragments. Correlation IDs are
unique per stream or per datagram channel; match responses by them, not by
arrival order. Idle connections close after thirty seconds; the listener's
[keepalive](configuration.md#keepalive) settings change that and add PINGs.

## DTLS

`protocol: "dtls"` uses DTLS 1.2 (Pion DTLS v3) with ALPN
`airc-gateway-dtls/1`, ECDHE, AES-128-GCM and extended master secret. The
server certificate must be RSA or ECDSA; client certificates are not AIRC
credentials. Each DTLS record carries one fragment.

## CoAP

| `protocol` | Transport | Security |
|---|---|---|
| `coap_udp` | UDP, CON or NON (RFC 7252) | none; no TLS settings allowed |
| `coaps` | DTLS 1.2, ALPN `coap` | certificate required |

`POST /v1/coap/rpc` with Content-Format `application/json` carries one frame
(no `stream`); the response is the response frame (Bad Request for an
error frame). Block1 and Block2 move whole bodies in blocks of at most
1024 bytes. Over UDP the source is unverified, so there is no session, and
each request packet gets at most one 1 KiB block back; fetch the rest with
Block2.

Watches use Observe in two steps, because a GET payload cannot carry a
request: POST the watch frame with `stream: true` to
`/v1/coap/observe/register`, which answers an `observation_token` (16 hex
digits) valid for ten seconds; then GET `/v1/coap/observe` with Observe=0
and that eight-byte token as the CoAP token. Notifications carry
fragments. Observe=1 with the token, a Reset or a closed connection
cancels.

## WebRTC

`protocol: "webrtc"` needs a certificate (it serves HTTPS signaling) and
usually `allowed_origins`. Signal with `POST /v1/webrtc/offer`:

```json
{"version": 1, "sdp": "<complete offer>", "mode": "reliable_ordered"}
```

The answer is `{"version": 1, "sdp": "..."}`; there is no trickle ICE. The
offer has one data-only section and the peer opens one channel labelled
`airc-gateway-v1` with protocol `agd1`. `mode` is `reliable_ordered`,
`reliable_unordered`, `unreliable_ordered` or `unreliable_unordered`
(unreliable modes use `maxRetransmits: 0`). Each binary message is one
fragment. Optional `webrtc.ice_servers` (`urls`, and for TURN `username`
and an owner-only `credential_file`) and `ice_transport_policy` (`all` or
`relay`, which needs a TURN server) configure ICE. Peers are limited per
source address and each holds a session slot. Only loopback paths have been
exercised.

## ZeroMQ and NNG

Both are cleartext TCP request/reply with no TLS settings, on any numeric
address. The request body is the contract request itself (no frame
wrapper); the reply is `{"version":1,"request_id","result"|"error"}`,
bodies at most 64 KiB. Idle connections stay open within the listener's
connection caps; the three-second deadline applies while a message or reply
is in transit.

- `zeromq`: ZMTP 3.1 with the NULL mechanism; the client is REQ (its READY
  must declare `Socket-Type: REQ`) and the gateway REP. A message is an
  empty delimiter frame and one data frame. Heartbeat `PING` is answered
  with `PONG`; other commands are refused.
- `nng`: SP over TCP with the eight-byte handshake, client REQ0 (0x30) and
  gateway REP0 (0x31). A message is an eight-byte big-endian length, one
  four-byte request ID with the high bit set, and the body; the reply echoes
  that ID. Forwarding devices are not supported.
