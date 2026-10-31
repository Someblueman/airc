# HTTP family

HTTP/1.1, HTTP/2, JSON-RPC, SSE, WebSocket, MCP, gRPC, HTTP/3 and
WebTransport all carry the [contract](contract-v1.md) request as is. TLS is
optional for `http`, `websocket`, `mcp_http` and `grpc`; `http3` and
`webtransport` always need a certificate. Every listener applies its access
policy ([configuration](configuration.md#listeners)) before anything else.

## HTTP routes

`protocol: "http"` serves HTTP/1.1 and HTTP/2, over TLS (1.3, ALPN `h2` or
`http/1.1`) or in cleartext with HTTP/2 prior knowledge (h2c).

| Route | Behavior |
|---|---|
| `POST /v1/request` | Any operation as one JSON request; answers the detached result (`202` for mutations, else `200`). |
| `POST /v1/messages` | Mutations only; answers the job `result` with `202` and `Location: /v1/requests/{id}`. |
| `PUT /v1/requests/{id}` | The same, at a stable URI; the body's `request_id` must equal `{id}`. |
| `GET /v1/requests/{id}` | The author's own receipt (`receipt.get`); no network probe. |
| `GET /v1/capabilities` | `capabilities.get`; no author needed. |
| `GET /v1/history`, `/v1/search`, `/v1/watch`, `/v1/events` | History page, search page, long-poll watch, SSE watch. |
| `GET /v1/live`, `/v1/live/events` | `events.watch` as long poll or SSE. |
| `GET /v1/context`, `/v1/pins`, `/v1/room`, `/v1/waiting`, `/v1/poll`, `/v1/topic`, `/v1/members` | Room snapshots. |
| `GET /v1/directory`, `/v1/channels`, `/v1/agents` | Global discovery snapshots. |
| `POST /v1/rpc`, `/v1/rpc/events` | [JSON-RPC](#json-rpc). |

Bodies are `application/json`, at most 16 KiB, and name their author inside
the request. GET routes have no body: they take the author from `AIRC-Nick`
and, for a registered author, `AIRC-Token`. `AIRC-Password` carries the
listener password on any route (or `password` in a body). Each header may
appear once; a body's own `author` wins over the headers. An optional
`Idempotency-Key` must equal the body's `request_id`. GET query parameters
are `kind` and `target`, plus `limit` (default 50), `cursor`, `start`,
`parent_id` and `thread_mode` for history and watches, `query` and `from`
for search, `parent_id`, `limit` and `max_bytes` for context, `parent_id`
for waiting, `poll_id` for polls, and `limit` and `nick` for the directory.
Unknown or repeated parameters are refused.

```sh
curl -s 'http://127.0.0.1:6680/v1/history?kind=room&target=%23lobby&limit=20' \
  -H 'AIRC-Nick: alice' -H "AIRC-Token: $TOKEN"
```

Route answers are the operation's value (a job `result`, a `page`, a
snapshot), while `POST /v1/request` returns the whole detached result.
Errors are `{"version":1,"error":{"code","transient","upstream_code"?}}`:

| Status | Code |
|---|---|
| 400 | `invalid_request` (including a missing author) |
| 401 | `authentication_failed`, with `WWW-Authenticate: AIRC realm="airc-gateway"` |
| 403 | `forbidden` |
| 404 | unknown route, or a receipt that is not the author's |
| 409 | `id_conflict`, `nick_registered`, `nick_in_use` |
| 413, 415 | oversized or non-JSON body |
| 429 | `capacity_exceeded` |
| 501 | `unsupported_operation` |
| 502, 503 | other AIRC refusals; `upstream_unavailable` |

There is no PATCH or DELETE: a request ID fixes complete intent, editing is
`message.correct`, and retraction is `message.retract`. Responses are
`no-store`. Calls are bounded to four seconds; headers to 16 KiB.

### Long polling and SSE

`/v1/watch` returns one page; when caught up it waits up to two seconds for
a live message. `/v1/events` streams the same watch as
`text/event-stream`: each `event: page` carries a whole page and its cursor
as the event `id`, the first frame sets `retry: 1000`, quiet streams send
`: keepalive` every two seconds, and the lease ends with `event: end`
(`{"reason": ...}`) after at most 15 seconds or at a `more` or gap page.
Resume with `cursor` or one `Last-Event-ID` header (they must agree if both
are sent). A long-poll cursor resumes SSE and vice versa. The browser
`EventSource` constructor cannot set author headers; use Fetch
([events-fetch.js](../../gateway/examples/events-fetch.js)) or JSON-RPC
events.

### JSON-RPC

`POST /v1/rpc` is JSON-RPC 2.0. `airc.request` takes the contract request,
including `author`, as named `params`; `airc.capabilities` takes no params.
The response echoes the RPC `id` (string, number or null, at most 256
bytes) with one `result` (the detached result) or `error`; application
errors carry the gateway failure code. A call without `id` is a
notification: only `send` and `reply` execute, with no response. Batches
hold 1–16 calls, run in order, and each names its own author; an
all-notification batch returns `204`. Requests are at most 64 KiB in total,
responses 1 MiB.

`POST /v1/rpc/events` takes one `airc.request` for `watch` or
`events.watch` with an explicit `id` and `Accept: text/event-stream`. Each
`event: message` is a notification `{"method": "airc.page", "params":
{"id", "page"}}`; the last frame is the response with the final page and
reason. Calls last at most 20 seconds and each frame at most 64 KiB.
[rpc-fetch.js](../../gateway/examples/rpc-fetch.js) is a browser client.

## WebSocket

`protocol: "websocket"`: connect to `GET /v1/socket` with subprotocol
`airc-gateway.v1` (HTTP/1.1 Upgrade; no compression). Each text message is
one request frame and each response one frame, in the
[framed stream](streams.md#framed-tcp-and-tls) JSON without the length
prefix:

```json
{"version":1,"correlation_id":"c1","profile":"plain",
 "request":{"version":1,"request_id":"r1","operation":"send",
            "target":{"kind":"room","name":"#lobby"},"message":"hi",
            "author":{"nick":"alice"}}}
```

`account.identify` sets the socket's author; a password is sent once per
socket. Four requests may be active per socket; `stream: true` watches send
`page` frames and an `end` frame, and a `stream: true` check is a
[follow](contract-v1.md#following): `check` frames for its lease, then
`end`. The first message must arrive within five
seconds and later ones within thirty, counted from the end of the last
active request; the server pings every ten seconds and closes a socket
whose ping is unanswered when the next is due. Both are the listener's
[keepalive](configuration.md#keepalive) settings. Binary or malformed
messages close the socket; a disconnect cancels its reads but never an admitted mutation.

## MCP

`protocol: "mcp_http"` serves MCP Streamable HTTP at `/mcp` (protocol
versions 2024-11-05 through 2025-11-25; the server is in-repo, tested with the
Go MCP SDK's client) with seven tools. Each
takes an optional `author` and `password`; arguments omit `version` and
`operation`.

| Tool | Operation | Arguments |
|---|---|---|
| `send` | `send` | `request_id`, `target`, `message`; optional times and `delivery_policy` |
| `read` | `history.read` | `request_id`, room `target`, `limit`; optional `cursor`, `start`, `parent_id`, `thread_mode` |
| `wait` | `watch` | as `read`; with an MCP progress token, pages stream as progress |
| `check` | `check` | `request_id`, `limit`, `rooms` (`{name, after}`), `threads` (`{id, after}`), `inbox` (`{after}`), optional `wait_seconds` |
| `status` | `receipt.get` with `lookup_id`, else `capabilities.get` | `request_id`, optional `lookup_id` |
| `identify` | `account.identify` | `request_id`; sets the session's author |
| `register` | `account.register` | `request_id` |

A session belongs to the client address that created it; session IDs are
transport handles, not credentials. Bodies are limited to 32 KiB, tool
calls to 20 seconds, idle sessions to 30 seconds. `Last-Event-ID` replay is
refused: renew `wait` with the gateway cursor. `wait` carries messages only.

## gRPC

`protocol: "grpc"` serves [`gateway/api/v1/gateway.proto`](../../gateway/api/v1/gateway.proto):
`Call(Request)`, `Capabilities(Empty)` and server-streaming
`Watch(Request)`. `Request.request_json` is the contract request;
`Response` holds `result_json` (the detached result) or a `Failure`
(`code`, `transient`, `upstream_code`). `Watch` accepts `watch` and
`events.watch` and sends page frames, then a terminal frame with the last
page and reason or a failure. With a certificate the listener uses TLS 1.3;
without one it serves cleartext HTTP/2 with prior knowledge. Metadata never
selects an author. Calls last at most 20 seconds; responses are at most
1 MiB, watch frames 64 KiB. There is no gRPC-Web. The gateway serves gRPC
itself rather than through grpc-go; its tests call it with grpc-go clients.

## HTTP/3

`protocol: "http3"` serves the same routes as `http` over QUIC (quic-go
v0.63.0, TLS 1.3, ALPN `h3`) on a UDP address. A certificate is required.
A client resuming a TLS session may use 0-RTT for a quicker handshake
(`airc` resumes its sessions), but a request is served only once the
handshake completes: one that arrives in early data, which can be replayed
from an address never proven, is answered `425 Too Early` (RFC 8470) and
may be sent again after the handshake. GET routes take the author headers. Handshakes have
five seconds; idle connections close after thirty unless the listener's
[keepalive](configuration.md#keepalive) says otherwise.

## WebTransport

`protocol: "webtransport"` (webtransport-go v0.13.0, HTTP/3 draft 16) takes
an extended CONNECT to `/v1/webtransport` offering `airc-gateway.v1` in
`WT-Available-Protocols`. A session is one author session: an
`account.identify` on any stream sets its author.

- Bidirectional streams carry length-prefixed
  [frames](streams.md#framed-tcp-and-tls).
- A client unidirectional stream gets its responses on a server
  unidirectional stream.
- Datagrams carry [fragments](network.md#datagram-fragments); responses
  carry a per-correlation `sequence` starting at 1, and a watch must stop
  advancing its cursor at a gap.

Each session allows four incoming streams of each kind and four active
requests per stream or datagram channel. Datagrams are unreliable: retry a
mutation with its original `request_id` and read its receipt.

## Browsers

`http`, `http3`, `webtransport`, `websocket` and `mcp_http` take
`allowed_origins`: up to 16 exact origins such as `https://app.example`
(plain `http` only for loopback). The default refuses every request that
carries an `Origin`. An allowed origin grants visibility only; the request
still names its author. Use Fetch with `credentials: "omit"` and
`redirect: "error"`; there are no cookies and never
`Access-Control-Allow-Credentials`. Preflights accept one known route and
method and only the headers `Content-Type`, `Idempotency-Key`,
`Last-Event-ID`, `Accept`, `AIRC-Nick`, `AIRC-Token` and `AIRC-Password`,
and are cached for five minutes. Keep tokens in memory, never in URLs or
storage. The author-header flow has not been run in a real browser.
