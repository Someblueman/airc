# JSON-RPC over HTTP

This profile uses the existing configured `http` listener and its exact operation
allowlist. It implements [JSON-RPC 2.0](https://www.jsonrpc.org/specification)
over HTTP/1.1 and HTTP/2, with the listener's TLS, access, origin, admission
and shutdown rules. It is separate from MCP. The complete catalogue and native DM
reads remain unfinished.

`POST /v1/rpc` accepts `application/json` with one request or a bounded batch.
`airc.request` takes the exact contract v1 request object, including its
`author`, as named `params`. `airc.capabilities` accepts omitted params or an
empty object and returns the listener's capabilities. Every current shared operation
uses its existing dispatcher, read lease or durable mutation admission.

```json
{"jsonrpc":"2.0","method":"airc.request","params":{"version":1,"request_id":"durable-send-1","operation":"send","target":{"kind":"room","name":"#room"},"message":"hello","author":{"nick":"alice"}},"id":"rpc-call-1"}
```

The response echoes the RPC `id` and has exactly one `result` or `error`. A send
result reports durable admission, not necessarily native acceptance. Query
`receipt.get` using the gateway `lookup_id` to inspect later acceptance. Changing
the RPC correlation ID does not change the durable intent. An uncertain retry
must preserve the gateway `request_id` and complete original intent; its stored
bounded retry policy and possible-duplicate behavior remain unchanged.

IDs may be a bounded JSON string, number or null; their raw value is echoed
without float conversion. Prefer string IDs. Omitted ID means a notification:
only `send` and `reply` notifications execute, and no RPC response is sent even
when execution fails. HTTP 204 does not prove journal admission or native
acceptance. A later receipt query by the same author can inspect the stable
request ID. Other notification operations are silently denied without execution.

Batches have 1–16 entries and execute sequentially under one bounded HTTP call.
Each item names its own author and is checked independently. Invalid
RPC items have their own error responses; notifications have none. An empty
batch or aggregate JSON validation failure produces one invalid-request error
before any execution. An all-notification batch returns HTTP 204
with no body. RPC IDs correlate replies; array position is not an identity.
Ordinary `watch` and `events.watch` calls, including batch entries, return bounded
long-poll pages.

## Live stream profile

`POST /v1/rpc/events` accepts one `airc.request` call for `watch` or
`events.watch`, with an explicit RPC ID and `Accept: text/event-stream`.
It returns bounded UTF-8 SSE frames. Each `event: message` contains a JSON-RPC
notification:

```json
{"jsonrpc":"2.0","method":"airc.page","params":{"id":"rpc-call-1","page":{"messages":[],"cursor":"opaque-checkpoint","status":"ok","gap":false}}}
```

The final frame is a JSON-RPC response with the original ID and the final page
and terminal reason, or a typed error. A caller disconnect may prevent delivery
of that terminal response. Frames have no invented SSE replay IDs. Renew using
the last completely decoded `page.cursor` in a new gateway request. `Last-Event-ID` is not an RPC replay mechanism. Retention gaps remain
explicit; [nonmessage events](live-events.md) are live-only.

Calls are bounded to 20 seconds, with the existing shorter setup/read leases,
session reserve and watch quotas. Stream writes flush synchronously under a
three-second deadline, without an unbounded queue. A lease has a 1 MiB aggregate
wire bound, 128 ordinary frame limit and a reserved terminal budget. Cancellation,
write failures and shutdown close and join native read sessions.

## Validation and limits

Only the exact POST routes are accepted; URL selectors, alternate authority
headers and replay headers are not part of this profile; a listener password
travels in `AIRC-Password`. Browser cookies do not grant authority.
Exact-origin preflight is permitted only when configured. Use Fetch with
`credentials: "omit"`.

The aggregate request bound is 64 KiB, with a 16 KiB contract request and bounded
RPC wrapper per item; correlation IDs are at most 256 encoded bytes. JSON-RPC
responses have a 1 MiB aggregate bound. Stream frames have a 64 KiB wire bound.
Unknown/duplicate JSON-RPC object fields, invalid Unicode and excessive nesting fail;
gateway params retain the strict existing core schema. Positional params are
not defined for these methods. JSON-RPC errors distinguish parse (`-32700`),
request (`-32600`), method (`-32601`) and params (`-32602`) failures; application
errors carry the existing stable gateway failure code without internal details.

The [Fetch example](../../examples/gateway/rpc-fetch.js) supports ordinary calls,
send-only notifications and one live lease. It bounds received bytes, checks
correlation and closes its reader on cancellation. Native acceptance tests and
the opt-in real browser probe are recorded in [STATUS.md](STATUS.md); a proposed
or skipped probe is not evidence of browser verification.
