# HTTP PUT acceptance evidence

`TestHTTPPUTStableRequestResourceAndCrossMethodCoalescing` exercises HTTP/1.1
and HTTP/2. PUT admission is durable and preserves the request-resource ID.
Native acceptance is compared with an independent observer's message ID, author,
target and complete multiline Unicode body.

POST and PUT repeats must preserve protocol, durable request ID, accepted state,
native message ID and one send attempt. A changed intent under the same PUT
resource must conflict. GET lookup after that rejection must still return the
original accepted resource with all those invariants intact. PUT reply linkage
remains checked against native delivery.

`TestHTTPPUTRejectsIdentityAndPermissionChanges` rejects path/body ID mismatch,
invalid credentials, receipt-only permissions, oversized and malformed bodies,
unsupported operations/routes, query credentials and ambiguous idempotency
headers. Rejected requests must create no job. These tests and POST acceptance
passed twice under race detection (terminal exit 0, 7.424 seconds):

```sh
go test -race ./integration/gateway -run '^(TestHTTPPUTStableRequestResourceAndCrossMethodCoalescing|TestHTTPPUTRejectsIdentityAndPermissionChanges|TestHTTPDurableSubmissionStatusAndReplyOverTLS)$' -count=2 -timeout=180s
```

Affected vet, formatting and diff checks passed. Outputs are retained in
`/tmp/airc-roadmap-baseline/http-put-qualification-race.out` and
`http-put-qualification-vet.out`. Qualification is local Darwin TLS adapter
behavior against a real private native daemon. Fresh final repository acceptance,
including the incomplete full race gate, remains outstanding.
