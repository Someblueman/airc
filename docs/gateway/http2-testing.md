# HTTP/2 acceptance evidence

> Dated evidence. Runs recorded here predate the
> [identity removal](removed-identity.md): their principals, signed or
> encrypted requests and return routes no longer exist.

`TestHTTP2HistoryPagesRecentAndHonorsRevocation` explicitly requires HTTP/2 for
both authenticated submissions and history responses. Submissions must receive
durable admission. An independent native observer verifies each author and
target and captures the complete root and reply records, including a multiline
Unicode root body.

One-record pages must preserve those native IDs, bodies, authors and targets,
the reply's parent/root linkage, more/complete status and nonempty cursors without
gaps. Two caught-up resumes must remain empty and complete. Recent-start behavior
is retained. A previously valid cursor cannot bypass principal revocation or the
second principal's lack of history permission.

The existing durable submission test also verifies accepted native IDs, receipt
lookup, stable-ID coalescing, conflicting intent rejection and reply linkage over
HTTP/2. HTTP/1 history and shared denial checks were included in validation.
All four tests passed twice under race detection (terminal exit 0, 6.501 seconds):

```sh
go test -race ./integration/gateway -run '^(TestHTTP1HistorySnapshot|TestHTTP2HistoryPagesRecentAndHonorsRevocation|TestHTTPDurableSubmissionStatusAndReplyOverTLS|TestHTTPDenialsAndScopedLookup)$' -count=2 -timeout=180s
```

Affected vet, formatting and diff checks passed. Outputs are retained in
`/tmp/airc-roadmap-baseline/http2-qualification-race.out` and
`http2-qualification-vet.out`. This qualifies the local Darwin TLS adapter
fixture against a real private native daemon. Other HTTP modes and fresh final
repository acceptance remain separate, including the incomplete full race gate.
