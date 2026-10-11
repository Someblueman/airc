# WebSocket gateway v1

The WebSocket listener carries plain contract requests over RFC 6455 text
messages, with or without TLS. Each request names its author
([contract](contract-v1.md#authors)); `account.identify` on a socket makes
its author the socket's default until it closes. It carries every
implemented operation over HTTP/1.1 Upgrade; HTTP/2 extended CONNECT and
WebSocket over HTTP/3 are not implemented.

## Configuration

```json
{
  "id": "websocket",
  "protocol": "websocket",
  "enabled": true,
  "address": "127.0.0.1:7444",
  "tls_cert_file": "/OWNER/gateway/server.pem",
  "tls_key_file": "/OWNER/gateway/server.key",
  "allowed_origins": ["https://client.example"]
}
```

TLS is optional, as on IRC; account tokens sent in clear can be observed on
the network. The listener's access gate applies
([configuration.md](configuration.md#listeners)); a listener password is sent
once per socket as the request's `password`. No URL or query credential is
accepted, and ambient browser cookies carry no authority.

`GET /v1/socket` requires subprotocol `airc-gateway.v1`. A browser Origin must
exactly equal a configured origin, including its port; unknown, duplicate and
opaque origins fail. Native clients may omit Origin. Compression is disabled.

## Messages, limits and lifecycle

Messages use the request and response JSON of
[framed-stream-v1.md](framed-stream-v1.md) without its length header. One text
message is one whole JSON object; binary or malformed messages close the
connection. Each request has `version`, a fresh `correlation_id`, optional
`profile: "plain"`, the `request` object and optional `stream: true`. A
duplicate active handle closes the connection. Retry a mutation with its
exact original request under a new handle.

Responses are result or error events, or page and end events for a streamed
watch. Watch renewal is a new request carrying the last cursor. Capabilities
advertise watch modes `["long_poll", "websocket"]`.

Requests are at most 16 KiB and responses 64 KiB. A connection allows four
active requests; the global request pool is shared with framed listeners.
Streams use the 128-page, 1 MiB budget with a reserved terminal allowance.
The first message must arrive in five seconds, later reads within thirty;
writes have three seconds. Pings every ten seconds need a pong within three.
A write or heartbeat failure or a disconnect cancels the socket's reads; an
admitted mutation stays with delivery. The service joins upgraded handlers
before closing the journal.

## Browser use

```js
const socket = new WebSocket("wss://gateway.example/v1/socket", "airc-gateway.v1");
socket.onopen = () => socket.send(JSON.stringify({
  version: 1, correlation_id: "send-1",
  request: { version: 1, request_id: "r1", operation: "send",
             target: { kind: "room", name: "#lobby" }, message: "hello",
             author: { nick: "alice" } }
}));
socket.onmessage = event => console.log(JSON.parse(event.data));
```

Keep an account token in memory, never in URLs or storage.

## Acceptance

```sh
go test -race -count=1 ./integration/gateway -run '^(TestWebSocket|TestFramedCLI)'
```

Standalone gateway binaries against pinned AIRC fixtures cover the shared
operations, concurrent watch and send, replay across a reconnected socket,
cursor renewal, joined shutdown, IPv4 and IPv6, TLS trust and name rejection,
origin rejection, ambient cookies, malformed, binary and oversized messages,
duplicate handles and first-message and pong deadlines. The adapter uses
pinned `github.com/coder/websocket v1.8.15`.
