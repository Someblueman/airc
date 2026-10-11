# Framed TCP/TLS stream v1

The framed stream carries plain contract requests over TCP, with or without
TLS, and over Unix streams ([unix-stream.md](unix-stream.md)). Each request
names its author ([contract](contract-v1.md#authors)); `account.identify`
makes its author the connection's default until the connection closes, as
after IRC `NICK` and SASL. It shares HTTP's durable journal, replay identity
and opaque cursors, and every implemented operation.

## Enable

```json
{
  "id": "stream",
  "protocol": "framed",
  "enabled": true,
  "address": "127.0.0.1:7443",
  "tls_cert_file": "/OWNER/gateway/server.pem",
  "tls_key_file": "/OWNER/gateway/server.key"
}
```

Omit both TLS files for plaintext, as on IRC; account tokens sent in clear
can be observed on the network. With TLS, the gateway requires TLS 1.3 and
ALPN `airc-gateway/1`; clients verify the CA and the certificate name or IP.
The listener's access gate applies ([configuration.md](configuration.md#listeners)).

## Wire contract

Each frame is a four-byte unsigned big-endian payload length followed by that
many UTF-8 JSON bytes. Request payloads are 1–16384 bytes, response payloads
at most 65536 bytes; the header is additional. Whole objects are emitted or
fail with `capacity_exceeded`; messages and pages are never truncated.

```json
{"version":1,"correlation_id":"send-1","profile":"plain","request":{"version":1,"request_id":"r1","operation":"send","target":{"kind":"room","name":"#lobby"},"message":"hello","author":{"nick":"alice"}}}
```

`profile` is optional and, when present, must be `plain`. Unknown, duplicate,
case-aliased or null fields and invalid UTF-8 fail closed. The correlation
handle is 1–64 ASCII letters, digits, `-` or `_`; use a fresh one per
request. Reusing an active handle closes the connection. Retry a mutation
with its exact original request and request ID under a fresh handle; a
different body under the same ID is a conflict.

Responses have `version`, `correlation_id`, `event: "result"` and `result`
(the DetachedResult every carrier returns). Errors have `event: "error"` and
`error: {code, transient}` ([contract-v1.md](contract-v1.md)). An admitted
mutation is not native acceptance: read its receipt. Capabilities report
`max_request_frame_bytes`, `max_response_frame_bytes` and watch modes
`["long_poll", "framed_stream"]`.

With `stream: true` the request must be a watch. Responses are
`event: "page"` frames and a final `event: "end"` with a reason (`resume`,
`lease_expired`, `capacity` or `upstream_unavailable`). Empty pages are
keepalives and must not advance a saved checkpoint. Renew by sending a new
request with the last cursor.

A connection allows four active correlated requests with serialized writes;
the global request cap is `limits.sessions`. The first byte must arrive in
five seconds, later idle waits are thirty seconds, and the rest of a frame
must arrive within five seconds. TLS handshakes and writes have three
seconds. Calls have four seconds and streams twenty. A stream allows at most
128 page or keepalive frames and 1 MiB, reserving 1024 bytes for its end
frame. A disconnect cancels reads; an admitted mutation stays with delivery.

## Example client

The Go example reads one contract request on stdin and prints each response
frame as one JSON line:

```sh
go run ./examples/gateway/framed-client --address 127.0.0.1:7443 \
  --ca /OWNER/gateway/ca.pem --correlation send-1 < request.json
```

Omit `--ca` for plaintext; add `--stream` for a watch, or `--network unix`
for a Unix socket path. Tokens appear only in the request on stdin.

## Evidence

```sh
go test -race -count=1 ./integration/gateway -run '^TestFramed'
```

Standalone gateway binaries against pinned AIRC fixtures over IPv4, IPv6 and
TLS cover native sends, replay without an extra attempt, replies, history,
search, directory, long polling, live watch with concurrent sends, cursor
renewal, shutdown during a watch, malformed wrappers and lengths, and partial
frames. The example client runs against the same fixtures.
