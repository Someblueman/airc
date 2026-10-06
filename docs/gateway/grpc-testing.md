# gRPC acceptance evidence

`TestGRPCReplayAndWholeNativeHistorySearchContext` uses the generated client
against a standalone gateway and private native daemon. Authenticated unary send
and reply reach durable native acceptance. An independent observer checks IDs,
author, target, complete multiline Unicode body and reply linkage. Stable replay
preserves native identity with one send attempt; changed durable intent conflicts.

A separate native CHECK establishes exactly two complete metadata records.
History, search, context and watch snapshots preserve those records. Added
one-record history pages compare every metadata field with native CHECK, require
correct more/complete status, nonempty cursors and no gaps. Two saved-cursor
resumes must return empty complete pages through the generated client.

The full gRPC integration group, including authentication, permissions, capacity,
streaming, slow-reader, revocation and shutdown checks, passed under race detection
(terminal exit 0, 39.252 seconds):

```sh
go test -race ./integration/gateway -run '^TestGRPC' -count=1 -timeout=180s
```

Affected vet, formatting and diff checks passed. Outputs are retained in
`/tmp/airc-roadmap-baseline/grpc-qualification-race.out` and
`grpc-qualification-vet.out`. This qualifies local Darwin generated-client
behavior. Remote deployment and fresh final repository acceptance, including
the incomplete full race gate, remain separate.
