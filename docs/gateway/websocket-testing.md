# WebSocket acceptance evidence

`TestWebSocketCLIStableRetryAfterDisconnect` explicitly requires a ws URL and
encrypted authentication. It disconnects without observing admission, reconnects
and repeats the identical authenticated intent with a different correlation ID.
Admission must be durable; receipt lookup must reach native acceptance with one
send attempt. An independently joined native observer checks the accepted ID,
author, target and complete multiline Unicode body.

A one-record retained history read must contain exactly that native record with
complete status, a nonempty cursor and no gap. Following the saved cursor must
produce a correlated empty complete page with a valid cursor and no error or gap.
The standalone gateway must join its hijacked connections during shutdown.

This test and shared operation/multiplexed-watch conformance passed twice under
race detection (terminal exit 0, 24.425 seconds):

```sh
go test -race ./integration/gateway -run '^(TestWebSocketCLIStableRetryAfterDisconnect|TestWebSocketCLISharedOperationsAndMultiplexedWatch)$' -count=2 -timeout=180s
```

Affected vet, formatting and diff checks passed. Outputs are retained in
`/tmp/airc-roadmap-baseline/websocket-qualification-race.out` and
`websocket-qualification-vet.out`. This qualifies local Darwin plaintext
WebSocket with encrypted envelopes. WSS certificate/origin and browser trust
evidence remain separate. Fresh final repository acceptance remains outstanding,
including the previously incomplete full race gate.
