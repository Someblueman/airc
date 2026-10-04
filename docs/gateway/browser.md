# Exact-origin browser Fetch

Each HTTP listener can explicitly configure `allowed_origins`, for example
`["https://app.example", "http://127.0.0.1:8080"]`. The default empty list rejects
Origin-bearing requests. Match the browser's complete serialized origin exactly,
including a nondefault port. Paths, credentials, fragments, wildcards, `null`,
Unicode hostnames, duplicate entries and explicit default ports are rejected.
Use canonical ASCII/punycode hosts and canonical IP addresses. At most 16 origins
and 512 bytes per origin are accepted. Plain HTTP origins are restricted to
localhost or literal loopback addresses. A permitted origin grants browser
visibility only: every actual request still needs its provisioned bearer and
current principal/operation/target permission, including durable revocation.

Use Fetch with `credentials: "omit"`, explicit `Authorization: Bearer …`, and
`redirect: "error"`. The gateway provides no cookie/session authentication and
never sets `Access-Control-Allow-Credentials`. Do not put credentials in query
strings or browser storage. `Access-Control-Allow-Origin` contains only the exact
configured origin; native clients without Origin retain the same bearer contract.
Errors, including capacity rejection, remain visible to permitted origins.
Responses remain `no-store`, `nosniff`, and vary on Origin. The policy follows the
[Fetch CORS protocol](https://fetch.spec.whatwg.org/#http-cors-protocol).

Preflights require an allowed origin, exactly one requested method and a known
route/method pair. Only Authorization, Content-Type, Idempotency-Key, Last-Event-ID
and Accept are allowed requested headers. Unknown/duplicate headers, unknown
routes and unsupported methods fail. Successful preflight is `204`, without
bearer authentication or any journal job, account, backend session or permission
change. Preflight approval expires after five minutes. The actual request is
still independently authenticated and parsed; preflight does not authorize it.
Location and WWW-Authenticate are exposed so a browser can follow a scoped request
status or read an authentication failure. Remote plaintext remains forbidden;
TLS 1.3 and existing listener credential checks are unchanged.

The [Fetch SSE example](../../examples/gateway/events-fetch.js) reads one bounded
lease with complete-frame checkpoints, fatal UTF-8 decoding, frame/wire limits,
reader cancellation and the `Last-Event-ID` header. `messageText` decodes the
existing `body_base64` metadata field without losing Unicode, newlines or an
initial BOM. Hold the last fully decoded page ID and renew explicitly under a
bounded retry policy. Native EventSource's constructor cannot supply this bearer
header; cookie-backed EventSource authentication is not implemented. This omission
does not waive the remaining catalogue functionality.

```js
import { roomEvents, messageText } from "./events-fetch.js";
for await (const { event, id, value } of roomEvents(gatewayURL, bearer, {
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

Real Chrome acceptance on macOS uses a disposable browser-origin HTTP server,
a separate loopback gateway listener and the immutable AIRC fixture. It verifies
browser-generated preflight, bearer capabilities, public directory snapshots and nickname reads, typed unauthenticated denial,
JSON send and exposed Location, scoped accepted receipt status, PUT preflight
and cross-method coalescing without another send, complete Unicode
multiline SSE body, opaque checkpoint cancellation and Last-Event-ID resume
without a duplicate message. The independent native observer confirms the post's
author and body. No live account, daemon, installed binary or persistent browser
credential is used. The browser tab closes after the probe.

To reproduce, run the opt-in probe and open its printed owned URL in a real
browser before its 90-second timeout; the page reports success/failure back to the
fixture, which performs the independent observer assertion and joins cleanup:

```sh
AGW_BROWSER_PROBE=1 go test -race ./integration/gateway \
  -run '^TestBrowserFetchOriginAndEvents$' -count=1 -v
go test -race ./internal/gateway/config ./integration/gateway \
  -run 'ExactBrowser|ExactOrigins|ListenerOrigin|StreamingEvents' -count=1
```

Native TLS HTTP/2 tests additionally verify allowed/denied origins, unsafe
preflight requests, credential-free preflight and bearer-free actual denial.
The actual CLI streaming test verifies configured origins propagate to the
listener. Browser trusted remote TLS, other browser engines, malicious-stream
coverage, nonmessage updates and full catalogue acceptance remain pending;
loopback Chrome evidence does not claim those untested modes.
