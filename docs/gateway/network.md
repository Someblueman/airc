# Network carriers

Packet, QUIC, CoAP, WebRTC and message-socket carriers. All take the
[contract](contract-v1.md) request, usually inside a
[frame](streams.md#frames), and apply the listener access policy. AIRC sees
the client's IP address (without port) as its source. Connection-oriented
carriers (a QUIC connection, a DTLS association, a CoAP/TCP, SCTP, ZeroMQ
or NNG connection, a WebRTC peer) are one author session, where
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

## UDP family

| `protocol` | Address | Options | Platforms |
|---|---|---|---|
| `udp` | numeric IP and port (wildcard and port 0 allowed) | none | all |
| `udp_lite` | numeric unicast IPv4 or IPv6 | `udp_lite: {"checksum_coverage": "full"}` (required; no fallback to UDP) | Linux |
| `ipv4_multicast` | group in `239.0.0.0/8`, port 1–65535 | `ipv4_multicast: {"interface": "eth0"}` | Darwin, Linux |
| `ipv6_multicast` | transient group with scope 1, 2, 3, 4, 5, 8 or 14, e.g. `[ff12::1234]:9001` | `ipv6_multicast: {"interface": "en0"}` (supplies the zone) | Darwin, Linux |
| `ipv4_broadcast` | `255.255.255.255` or the subnet's broadcast address | `ipv4_broadcast: {"interface", "subnet"}` | Darwin, Linux |

Each request is a fragmented frame; replies are unicast to the source,
never to a group or broadcast address. A broadcast reaches every gateway on
the segment, so run one receiver per broadcast domain unless every receiver
should answer. Calls last four seconds and streamed watches twenty; idle
sources expire after five seconds; invalid input is dropped without reply.

## ICMPv6

`protocol: "icmp_v6"` (Darwin ARM64) carries fragments in ICMPv6 Echo
Requests in both directions. `address` is `[IPv6]:channel`, where the
channel (1–65535) is the Echo identifier, not a port; link-local,
multicast and zoned addresses are refused. `icmp_v6.interface` must own the
address. Replies go to the source with the channel in the Echo sequence;
kernel Echo Replies are ignored.

## SCTP

`sctp` (ordered), `sctp_unordered` and `sctp_pr_ttl` (Linux ARM64, kernel
SCTP) need an explicit literal unicast `IP:port`. Each record on stream 0
with PPID 0 carries one frame without a length prefix; each reply is one
record. `sctp_unordered` requires `SCTP_UNORDERED` on every request and sets
it on every reply. `sctp_pr_ttl` uses ordered records with PR-SCTP timed
reliability fixed at 1000 ms; peers must negotiate PR support. A record
fits 16 KiB; truncated, interleaved or off-stream records close the
association.

## Raw QUIC

`rawquic` (streams) and `rawquic_datagrams` (streams plus QUIC datagrams)
listen on UDP with TLS 1.3 and ALPN `airc-gateway-quic/1`; a certificate
is required, and 0-RTT is accepted as on [HTTP/3](http.md#http3). Bidirectional streams carry length-prefixed
frames; a client unidirectional stream gets responses on a server
unidirectional stream; datagrams carry fragments. Correlation IDs are
unique per stream or per datagram channel; match responses by them, not by
arrival order.

## DTLS

`protocol: "dtls"` uses DTLS 1.2 (Pion DTLS v3) with ALPN
`airc-gateway-dtls/1`, ECDHE, AES-128-GCM and extended master secret. The
server certificate must be RSA or ECDSA; client certificates are not AIRC
credentials. Each DTLS record carries one fragment.

## CoAP

| `protocol` | Transport | Security |
|---|---|---|
| `coap_udp` | UDP, CON or NON (RFC 7252) | none; no TLS settings allowed |
| `coap_tcp` | TCP (RFC 8323) | none; no TLS settings allowed |
| `coaps` | DTLS 1.2, ALPN `coap` | certificate required |
| `coaps_tcp` | TLS 1.3, ALPN `coap` | certificate required |

`POST /v1/coap/rpc` with Content-Format `application/json` carries one frame
(no `stream`); the response is the response frame (Bad Request for an
error frame). Block1 and Block2 move whole bodies in blocks of at most
1024 bytes. Over UDP the source is unverified, so there is no session, and
each request packet gets at most one 1 KiB block back; fetch the rest with
Block2. The TCP variants exchange CSM first (the gateway advertises a
1152-byte frame) and pick block sizes that fit the peer.

Watches use Observe in two steps, because a GET payload cannot carry a
request: POST the watch frame with `stream: true` to
`/v1/coap/observe/register`, which answers an `observation_token` (16 hex
digits) valid for ten seconds; then GET `/v1/coap/observe` with Observe=0
and that eight-byte token as the CoAP token (CoAP over TCP adds
`?observation=<token-hex>` when notifications need blockwise transfer).
Notifications carry fragments. Observe=1 with the token, a Reset or a closed
connection cancels.

## OSCORE

`protocol: "coap_oscore"` runs a native libcoap worker that terminates
OSCORE and hands the gateway plain frames. Build it with
`scripts/build-oscore.sh` ([native/oscore](../../native/oscore/README.md)),
then configure and provision the context:

```json
{"id": "oscore-device", "protocol": "coap_oscore", "enabled": true,
 "address": "0.0.0.0:5683", "operations": ["send", "receipt.get", "history.read"],
 "oscore": {"binary": "/abs/build/oscore-gateway",
            "context_file": "/abs/private/device-oscore.json",
            "author": {"nick": "device", "token_file": "/abs/private/device.token"}}}
```

```sh
airc-gateway oscore init --file CONFIG --listener oscore-device \
  --context-file /abs/private/device-oscore.json
```

`init` creates a fresh context (256-bit master secret, 128-bit salt and
context ID; AES-CCM-16-64-128, HKDF-SHA-256, server sender ID `01`, client
`02`, replay window 64) and its replay record in the journal. It never
imports, resets or overwrites one; keep the file paired with its journal.
Requests are OSCORE-protected `POST /v1/coap/rpc` with one frame (16 KiB in,
64 KiB out, no Observe). `oscore.author` is the default author for requests
that name none (a guest without `token_file`); the source is
`listener:<id>`. The worker has only run on Darwin, before the author model.

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
