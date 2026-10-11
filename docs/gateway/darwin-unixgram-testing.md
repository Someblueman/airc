# Darwin Unix-datagram acceptance evidence

> Dated evidence. Runs recorded here predate the
> [identity removal](removed-identity.md): their principals, signed or
> encrypted requests and return routes no longer exist.

`TestUnixgramResponseLossStableReplayAndSourceReconnect` sends authenticated
fragmented intent in reverse order with a duplicate fragment, discards the actual
encrypted response, and reconnects from a new source socket. Replaying identical
intent must preserve durable admission and native acceptance with one send
attempt. An independent observer verifies the accepted ID, author, target and
complete body exceeding 3500 bytes. A separate native CHECK must contain exactly one record.

A one-record history response through the configured encrypted mailbox must
match every native metadata field, report complete status, return a nonempty
cursor and report no gap. Following that cursor returns an empty complete page
with a valid cursor. Response opening verifies signature, encryption and route
scope; the call helper checks correlation, operation and result sequence.

The replay/read test passed twice under race detection (exit 0, 8.359 seconds):

```sh
go test -race ./integration/gateway -run '^(TestUnixgramResponseLossStableReplayAndSourceReconnect|TestUnixgram.*Bound.*)$' -count=2 -timeout=180s
```

The boundary selector above did not match the admission test's name. That test
was run explicitly twice (exit 0, 7.159 seconds):

```sh
go test -race ./integration/gateway -run '^TestUnixgramOversizeTamperSignedAndIncompleteCannotAdmit$' -count=2 -timeout=180s
```

Affected vet, formatting and diff checks passed. Outputs are retained in
`/tmp/airc-roadmap-baseline/darwin-unixgram-race.out`,
`darwin-unixgram-boundary-race.out` and `darwin-unixgram-vet.out`.
This qualifies Darwin runtime behavior with private owner-only sockets and a
configured mailbox. Linux runtime evidence and fresh final repository acceptance,
including the incomplete full race gate, remain outstanding.
