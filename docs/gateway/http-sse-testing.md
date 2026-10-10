# SSE acceptance evidence

`TestHTTPEventsRetainedPrefixResumeAndLeaseExpiry` submits three authenticated
durable messages through HTTP POST. Its independent native observer verifies the
author, target and complete multiline Unicode bodies. SSE negotiates HTTP/2 and
the event-stream content type. Each one-record page must match the observed
native ID, body, author and target without a gap; its SSE ID must equal the opaque
cursor and differ from the native message ID.

Closing each catch-up stream and reconnecting with Last-Event-ID must return the
next retained record. Unread-prefix pages report more and end with the resume
reason; the last page reports complete. The quiet final stream must survive the
normal five-second write bound and terminate by authorization lease expiry within
the existing 10–18 second observation bound.

`TestHTTPEventsSameLeaseLiveResumeAndRevocation` retains HTTP/1.1 and HTTP/2 live
delivery, same-lease continuity, cancellation joining, raw/tampered cursor and
permission denial, and revocation checks. Both tests passed twice under race
detection (terminal exit 0, 33.983 seconds):

```sh
go test -race ./integration/gateway -run '^(TestHTTPEventsRetainedPrefixResumeAndLeaseExpiry|TestHTTPEventsSameLeaseLiveResumeAndRevocation)$' -count=2 -timeout=180s
```

Affected vet, formatting and diff checks passed. Outputs are retained in
`/tmp/airc-roadmap-baseline/http-sse-qualification-race.out` and
`http-sse-qualification-vet.out`. This qualifies local Darwin SSE as the HTTP
submission companion. Browser deployment and fresh final repository acceptance,
including the incomplete full race gate, remain separate.
