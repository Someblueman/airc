# HTTP/1.1 acceptance evidence

`TestHTTP1HistorySnapshot` now submits two authenticated durable messages while
explicitly checking HTTP/1 negotiation. Its independent native observer checks
the author and target and captures each complete multiline Unicode body. History
requests also require HTTP/1. One-record pages must match the observed native
IDs, bodies, authors and targets, with more/complete status, nonempty cursors and
no gaps. Two successive caught-up cursor resumes must return empty complete pages.

The existing `TestHTTPDurableSubmissionStatusAndReplyOverTLS` independently
checks accepted native IDs, scoped receipt lookup, stable-ID coalescing,
conflicting intent rejection and reply linkage for HTTP/1.1 and HTTP/2.
`TestHTTPDenialsAndScopedLookup` retains credential, target and principal-isolation
boundaries. The HTTP/2 history test retains its page, recent and revocation checks.
All four tests passed twice under race detection (terminal exit 0, 6.857 seconds):

```sh
go test -race ./integration/gateway -run '^(TestHTTP1HistorySnapshot|TestHTTP2HistoryPagesRecentAndHonorsRevocation|TestHTTPDurableSubmissionStatusAndReplyOverTLS|TestHTTPDenialsAndScopedLookup)$' -count=2 -timeout=180s
```

Affected vet, formatting and diff checks passed. Outputs are retained in
`/tmp/airc-roadmap-baseline/http1-qualification-race.out` and
`http1-qualification-vet.out`. This qualifies the local Darwin TLS HTTP/1.1
adapter fixture against the real native daemon. Other HTTP modes and fresh final
repository acceptance remain separate, including the incomplete full race gate.
