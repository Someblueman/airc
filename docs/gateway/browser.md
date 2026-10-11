# Exact-origin browser Fetch

Each HTTP listener can explicitly configure `allowed_origins`, for example
`["https://app.example", "http://127.0.0.1:8080"]`. The default empty list rejects
Origin-bearing requests. Match the browser's complete serialized origin exactly,
including a nondefault port. Paths, credentials, fragments, wildcards, `null`,
Unicode hostnames, duplicate entries and explicit default ports are rejected.
Use canonical ASCII/punycode hosts and canonical IP addresses. At most 16 origins
and 512 bytes per origin are accepted. Plain HTTP origins are restricted to
localhost or literal loopback addresses. A permitted origin grants browser
visibility only: every actual request still names its author and passes the
listener's access policy, as a request without Origin does.

Use Fetch with `credentials: "omit"`, the `AIRC-Nick` header (plus `AIRC-Token`
for a registered account and `AIRC-Password` when the listener has a password),
and `redirect: "error"`. The gateway provides no cookie/session authentication
and never sets `Access-Control-Allow-Credentials`. Keep tokens in memory: never
in query strings or browser storage. `Access-Control-Allow-Origin` contains only
the exact configured origin. Errors, including capacity rejection, remain
visible to permitted origins. Responses remain `no-store`, `nosniff`, and vary
on Origin. The policy follows the
[Fetch CORS protocol](https://fetch.spec.whatwg.org/#http-cors-protocol).

Preflights require an allowed origin, exactly one requested method and a known
route/method pair. Only `Content-Type`, `Idempotency-Key`, `Last-Event-ID`,
`Accept`, `AIRC-Nick`, `AIRC-Token` and `AIRC-Password` are allowed requested
headers. Unknown/duplicate headers, unknown routes and unsupported methods fail.
Successful preflight is `204`, without any journal job, account, backend
session or permission change. Preflight approval expires after five minutes.
The actual request is still independently parsed and checked; preflight does
not authorize it. `Location` and `WWW-Authenticate` are exposed so a browser
can follow a scoped request status or read an authentication failure.

The [Fetch SSE example](../../examples/gateway/events-fetch.js) reads one bounded
lease with complete-frame checkpoints, fatal UTF-8 decoding, frame/wire limits,
reader cancellation and the `Last-Event-ID` header. `messageText` decodes the
`body_base64` metadata field without losing Unicode, newlines or an initial
BOM. Hold the last fully decoded page ID and renew explicitly under a bounded
retry policy. Native EventSource's constructor cannot set the author headers,
so use Fetch. [rpc-fetch.js](../../examples/gateway/rpc-fetch.js) makes
JSON-RPC calls whose params name their author.

```js
import { roomEvents, messageText } from "./events-fetch.js";
for await (const { event, id, value } of roomEvents(gatewayURL, { nick: "alice", token }, {
  target: "#room", cursor: lastCompleteCursor, signal: abortController.signal
})) {
  if (event === "page") {
    for (const message of value.messages) renderMessage(messageText(message));
    lastCompleteCursor = id;
    if (value.gap) showRetentionGap();
  } else {
    showLeaseEnd(value.reason);
  }
}
```

```sh
go test -race ./internal/gateway/config ./integration/gateway \
  -run 'ExactBrowser|ExactOrigins|ListenerOrigin|StreamingEvents' -count=1
```

Native TLS HTTP/2 tests verify allowed and denied origins, unsafe preflight
requests and credential-free preflight. The actual CLI streaming test verifies
configured origins propagate to the listener. Real-browser evidence recorded
before the [identity removal](removed-identity.md) used bearer and signed
requests; the author-header flow has not been run in a real browser, and
trusted remote TLS and other browser engines remain unverified.

WebTransport uses its separate draft-16 UDP listener and exact Origin policy;
requests carry their author in-band. See [webtransport.md](webtransport.md).
