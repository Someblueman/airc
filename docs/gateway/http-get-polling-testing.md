# HTTP GET polling acceptance evidence

`TestHTTPGetPollingResumesAfterCaughtUp` runs with explicit HTTP/1.1 and HTTP/2
checks. Reading an independently observed seed record establishes a nonempty
history cursor. Two empty polls advance that caught-up cursor before two new
authenticated durable messages are sent. An independent native observer checks
their author, target and complete multiline Unicode bodies.

GET polling from the saved cursor must return the two new native records in
one-record pages with exact IDs, bodies, authors and targets, correct more/complete
statuses, nonempty cursors and no gaps. Two further polls must remain empty and
complete. An initially empty room returns an empty cursor; this test deliberately
qualifies continuation from an established native position.

The test, both protocol history tests and standalone CLI cursor scope/restart
checks passed twice under race detection (terminal exit 0, 26.963 seconds):

```sh
go test -race ./integration/gateway -run '^(TestHTTPGetPollingResumesAfterCaughtUp|TestHTTP1HistorySnapshot|TestHTTP2HistoryPagesRecentAndHonorsRevocation|TestGatewayServeCLIReadOpaqueCursorScopesAndRestart)$' -count=2 -timeout=180s
```

Affected vet, formatting and diff checks passed. Output is retained in
`/tmp/airc-roadmap-baseline/http-get-polling-race-fixed.out` and
`http-get-polling-vet.out`. The initial run's incorrect expectation of a cursor
for an empty room is preserved in `http-get-polling-race.out`; only the test
setup was corrected. Local Darwin polling is qualified. Long-poll wakeup and
deadline behavior and fresh final repository acceptance remain separate.
