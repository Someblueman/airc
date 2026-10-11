# Author requests on network carriers

These carriers take the same plain contract request as HTTP and WebSocket
(see [contract](contract-v1.md#authors)) with no principal, bearer token,
signing, recipient or response key: a request naming `{"nick"}` posts as an
AIRC guest, `{"nick","token"}` as that account, and AIRC checks it. Results are
plain contract JSON. Every listener takes the author access policy (guests on
by default, optional `targets`, `password_file` and `limits`; see
[configuration](configuration.md#upstream-and-author-access)). The source AIRC
limits by is the client's IP address without its port, or `listener:<id>`
where there is none.

| Carrier | Request form | Author on the carrier | Reply |
|---|---|---|---|
| gRPC (`grpc`) | `Call`/`Watch` envelope bytes = the contract request; `Capabilities` | per request | `ResultJson` = the DetachedResult, or `Failure` (with AIRC's numeric) |
| Raw QUIC bidi / paired uni (`rawquic`) | plain [framed](framed-stream-v1.md) frame | `account.identify` holds for the QUIC connection (all streams and datagrams) | frame on the stream |
| Raw QUIC datagrams (`rawquic_datagrams`) | plain frame in AGD1 fragments | same connection session | AGD1 frames on the connection |
| HTTP/3 (`http3`) | the HTTP routes ([http](http.md)) | per request (`AIRC-Nick`/`AIRC-Token` on GET) | as HTTP |
| WebTransport bidi / uni / datagrams | plain frames, as raw QUIC | one session per WebTransport session | as raw QUIC |
| WebRTC, all four channel modes | offer without `Authorization`; plain frames in AGD1 on the channel | one session per peer; peers limited per source IP | AGD1 frames on the channel |
| CoAP over DTLS (`coaps`), CON or NON | POST `/v1/coap/rpc` (or the Observe registration) with a plain frame | one session per DTLS association | the CoAP response |
| CoAP over TLS (`coaps_tcp`) or plain TCP (`coap_tcp`) | same | one session per connection | the CoAP response |
| CoAP over UDP (`coap_udp`), CON or NON | ordinary CoAP: same paths, Block2 for large results | per request (an unverified UDP source has no session) | the CoAP response to the source |
| Native OSCORE (`coap_oscore`) | plain frame inside the OSCORE-protected request | the context's optional `oscore.author` (nick, optional `token_file`), else the body's; source `listener:<id>` | the OSCORE-protected response |
| Generic DTLS (`dtls`) | plain frame in AGD1 records | one session per association | AGD1 records |
| UDP, UDP-Lite, IPv4/IPv6 multicast, IPv4 broadcast, ICMPv6 Echo | plain frame in AGD1 datagrams | per request | AGD1 frames to the request's unicast source |
| SCTP (ordered, unordered, PR-SCTP TTL) | one plain frame per record | one session per association | one plain frame per record |
| ZeroMQ REQ/REP (`zeromq`, NULL) | the contract request as the REQ body | one session per connection | `{"version","request_id","result"\|"error"}` |
| NNG REQ0/REP0 (`nng`) | same | one session per connection | same |
| MQTT-SN QoS -1 (`mqtt_sn`) | PUBLISH payload = a `send`; the predefined topic's `target` must equal the request's | per request | none on success; invalid-topic PUBACK |

Cleartext carriers (gRPC h2c, CoAP UDP/TCP, the UDP family, ICMPv6, SCTP,
ZeroMQ, NNG, MQTT-SN) need no TLS, as on IRC: an account token sent on them
is visible to anyone on the path. QUIC, HTTP/3, WebTransport, DTLS, CoAPS,
OSCORE and WebRTC are encrypted by definition and keep their certificates or
security context. gRPC uses TLS when the listener has a certificate, and
cleartext HTTP/2 with prior knowledge otherwise.

Datagram replies go to the request's source, so the gateway bounds what a
spoofed source can be sent: on the UDP family and ICMPv6 a reply may use at
most three times the request's wire bytes (as in QUIC address validation),
plus a 512-byte final error frame; loopback sources are exempt. Pad a request
with JSON whitespace to make room for a larger page. CoAP over UDP answers one
block (at most about 1 KiB) per request packet. Fragmentation, frame, stream
and admission bounds are unchanged.

ZeroMQ answers ZMTP 3.1 `PING` heartbeats with `PONG`. ZeroMQ and NNG keep an
idle REQ connection open (bounded by the listener's connection caps); the
three-second deadline applies only while a message or reply is in transit.

A listener with protected return routes (UDP family, ICMPv6, CoAP UDP/TCP,
SCTP), principal topic routes (MQTT-SN) or an OSCORE `principal` keeps the
legacy signed/encrypted profile instead; bearer and envelope profiles on the
other carriers keep working alongside plain frames. That principal shim is
removed later.

## Evidence

`TestAuthorNetworkCarriersPostAsAuthors` runs gRPC h2c, raw QUIC streams
(identify on one stream, post on another) and datagrams, WebTransport (identify
on a bidi stream, post on a uni stream, guest datagram), all four WebRTC modes,
HTTP/3, CoAP over UDP, plain TCP and DTLS, UDP, ZeroMQ (PING, then a post
after idling past three seconds), NNG and MQTT-SN against a current aircd; an
independent IRC client checks each message's author. On 2026-10-11 real
clients, grpcio 1.84 (h2c, hand-encoded protobuf), pyzmq 27.2 (REQ with
heartbeats, 5 s idle), pynng (REQ0, 5 s idle) and aiocoap (UDP CON and NON,
CoAP over TCP), posted as guests and as a registered account (registered over
gRPC with `account.register`) to a scratch aircd; `airc history` showed all
ten messages with the right authors and the account ID on the registered
ones. Multicast, broadcast, UDP-Lite, ICMPv6, SCTP and OSCORE share these code
paths but could not be exercised in this sandbox (no IPv6, Linux ARM64 SCTP,
Darwin ICMPv6 or native OSCORE worker).
