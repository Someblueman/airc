# HTTP POST acceptance evidence

`TestHTTPDurableSubmissionStatusAndReplyOverTLS` explicitly checks HTTP/1.1 and
HTTP/2 authenticated POST admission. Admission must be durable and retain the
logical request ID. Native acceptance is compared with an independent observer's
ID, author, target and complete multiline Unicode body; scoped GET receipt lookup
must preserve the observed ID.

Repeating the same POST must preserve the protocol, durable request ID, accepted
state, native message ID and exactly one send attempt. Changed intent under that
ID must conflict. A subsequent reply must retain the native parent/root linkage.
Existing denial/isolation and POST/PUT cross-method coalescing tests ran alongside
the strengthened witness. All three tests passed twice under race detection
(terminal exit 0, 7.151 seconds):

```sh
go test -race ./integration/gateway -run '^(TestHTTPDurableSubmissionStatusAndReplyOverTLS|TestHTTPDenialsAndScopedLookup|TestHTTPPUTStableRequestResourceAndCrossMethodCoalescing)$' -count=2 -timeout=180s
```

Affected vet, formatting and diff checks passed. Outputs are retained in
`/tmp/airc-roadmap-baseline/http-post-qualification-race.out` and
`http-post-qualification-vet.out`. This qualifies local Darwin TLS adapter
submission against a private real native daemon. Fresh final repository
acceptance, including the incomplete full race gate, remains outstanding.
