# HTTP ingress

Every HTTP request is a plain contract request run as its author
([contract](contract-v1.md#authors), [authentication](authentication.md)),
over HTTP/1.1, HTTP/2 with prior knowledge, or TLS. The listener's access
gate (operations, targets, guests, password, limits) applies first.

| Route | Behavior |
|---|---|
| `POST /v1/request` | Any operation as one JSON contract request, naming its `author`; answers the DetachedResult every carrier returns (`202` for mutations). |
| `POST /v1/messages` | Mutations; `202` only after journal commit, with `Location: /v1/requests/{id}`. |
| `PUT /v1/requests/{id}` | The same durable mutation at a stable request URI; body `request_id` and an optional `Idempotency-Key` must match. |
| `GET /v1/requests/{id}` | The author's own receipt; no network probe, send or budget change. |
| `GET /v1/capabilities` | The listener's operations, targets, limits and retry defaults. |
| `GET /v1/history`, `/v1/search`, `/v1/watch`, `/v1/events` | Bounded room reads, long-poll watch and SSE with opaque cursors; see [history.md](history.md), [search.md](search.md), [watch.md](watch.md), [events.md](events.md). |
| `GET /v1/context`, `/v1/pins`, `/v1/room`, `/v1/waiting`, `/v1/poll`, `/v1/topic`, `/v1/members` | Bounded snapshot reads. |
| `GET /v1/directory`, `/v1/channels`, `/v1/agents` | Public discovery snapshots; see [directory.md](directory.md), [channels.md](channels.md), [agents.md](agents.md). |
| `POST /v1/rpc`, `/v1/rpc/events` | JSON-RPC 2.0 with the same request as `params`; see [jsonrpc.md](jsonrpc.md). |

POST and PUT bodies name the author inside the request. GET routes name it
with `AIRC-Nick` and, for a registered author, `AIRC-Token`; the access
password travels as `AIRC-Password` (or `password` in a body). Each header may
occur once. A missing author where one is needed answers `401` with
`WWW-Authenticate: AIRC realm="airc-gateway"`.

The body supplies the stable request ID. An optional `Idempotency-Key` header
must occur once and match it. Duplicate intent coalesces; a different body
under the same ID conflicts. POST and PUT share the author's scope, so
cross-method repeats coalesce without resetting budgets or expiry. Request
IDs must not be reused within the retention window. Mutation routes cannot
select reads and GET routes cannot submit mutations. Responses disable
caching and carry normalized results or errors, never credential values.
Invalid requests, refusals, conflicts, oversized bodies, quotas and
unavailable intake have stable HTTP classifications; another author's or a
missing request ID is `404`.

`adapter.HTTPServer` returns a standard-library server with HTTP/1.1 and
HTTP/2, TLS 1.3 minimum when TLS is configured, header/read/write/idle
deadlines, bounded headers and bodies and a nonblocking ingress-slot limit.
Plain HTTP is accepted from anywhere, as on IRC; tokens sent without TLS can
be observed on the network. The service owns binding and lifecycle; see
[service.md](service.md). Caller disconnects never cancel admitted jobs.

Browser origins are denied by default; a listener's `allowed_origins` grants
exact-origin Fetch access with bounded preflight and no cookies. See
[browser.md](browser.md).

## PATCH and DELETE

There is no PATCH route: admission takes a complete bounded intent fixed by
its request ID, and editing an accepted AIRC message is its own operation
(`message.correct`). There is no DELETE route for messages or requests: a
disconnect does not cancel an admitted job, retained receipts follow the
journal's maintenance policy, and retraction is `message.retract`. The MCP
endpoint's DELETE only closes an MCP session.

## Capabilities

`capabilities.get` describes available intake, including durable send while
AIRC is offline. `upstream_compatible` is `null`: the response never claims a
feature negotiation succeeded. `retry_defaults` applies to newly admitted
jobs. History, search and watch targets and byte bounds are advertised only
for implemented room selectors. Discovery performs no backend dial, job
change or send.

## Evidence

```sh
go test -race -count=1 ./integration/gateway -run 'TestHTTP|TestAuthor'
```

Real HTTP/1.1 and HTTP/2 clients against an owned listener submit durable
posts and replies as authors, read their own receipts, repeat and conflict
IDs, and are checked by an independent AIRC observer (author, room, body,
message ID, parent and thread root). No live daemon or account is involved.
