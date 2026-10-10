# Raw QUIC datagram acceptance evidence

`TestRawQUICDatagramFragmentReplayAndNativeFact` sends encrypted authenticated
fragments out of order with a duplicate. Repeated durable intent retains fixed
validity times while each transport packet ID is fresh. Receipt lookup through
datagrams must reach native acceptance with one send attempt. A separate native
CHECK must contain exactly one complete record with the accepted ID, body
exceeding 3500 bytes, author and target.

One-record history and saved-cursor resume also use datagrams. Result correlation,
sequence, operation and absence of errors are checked. The bounded page must
match every native metadata field, complete status, a nonempty cursor and no
gap. Following its cursor returns an empty complete page.

The incomplete-fragment control requires no response, unchanged native history
and no delivery job. The retained/live watch checks sequence and cancellation
joining. These tests establish bounded local reassembly and explicit durable
replay; they do not promise reliable delivery of datagrams.

The datagram group passed twice under race detection (exit 0, 21.124 seconds):

```sh
go test -race ./integration/gateway -run '^TestRawQUICDatagram' -count=2 -timeout=180s
```

Affected vet, formatting and diff checks passed. Outputs are retained in
`/tmp/airc-roadmap-baseline/quic-datagram-qualification-race.out` and
`quic-datagram-qualification-vet.out`. This qualifies local Darwin packet fixtures.
Remote loss behavior and fresh final repository acceptance, including the
incomplete full race gate, remain separate.
