# JSON-RPC acceptance evidence

`TestRPCRequestMutationReplayAndNotification` submits an authenticated durable
request through the standalone gateway and compares native acceptance with an
independent observer's message ID, author, target and complete multiline Unicode
body. Replaying identical intent changes the RPC ID from a string to a number;
the response must echo that RPC ID while preserving the durable request ID,
accepted state, native message ID and one send attempt.

A notification submits another native-observed message without an RPC response
body. One-record history pages must match both observations exactly, including
more/complete statuses, nonempty cursors and no gaps. Each page echoes its own
RPC correlation ID while retaining the same logical read request ID. A numeric
RPC ID on final cursor resume must return an empty complete page.

The RPC integration group retains HTTP/1.1 and HTTP/2 correlation forms, batch,
protocol, permissions and operation coverage. It passed under race detection
(terminal exit 0, 43.939 seconds):

```sh
go test -race ./integration/gateway -run '^TestRPC' -count=1 -timeout=180s
```

Affected vet, formatting and diff checks passed. Outputs are retained in
`/tmp/airc-roadmap-baseline/json-rpc-qualification-race.out` and
`json-rpc-qualification-vet.out`. This qualifies local Darwin JSON-RPC over
authenticated HTTP against a private native daemon. Fresh final repository
acceptance, including the incomplete full race gate, remains outstanding.
