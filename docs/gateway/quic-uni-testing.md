# Raw QUIC paired unidirectional acceptance evidence

> Dated evidence. Runs recorded here predate the
> [identity removal](removed-identity.md): their principals, signed or
> encrypted requests and return routes no longer exist.

`TestRawQUICUniStreamRoundTrip` sends every request through an opened
unidirectional stream and receives the correlated result through a separate
accepted response stream. Send, receipt lookup, replay, one-record history and
cursor resume all use that pairing. Writes and response decoding have checked
five-second deadlines; consumed response streams are cancelled during cleanup.

The encrypted authenticated 3500-byte send must reach durable native acceptance.
An independent observer verifies the complete body, ID, author and target.
Replay preserves native acceptance with one send attempt. Bounded history must
preserve that record, complete status, a nonempty cursor and no gap; following
the saved cursor returns an empty complete page.

Paired and retained bidirectional witnesses passed twice under race detection
(terminal exit 0, 10.633 seconds):

```sh
go test -race ./integration/gateway -run '^(TestRawQUICUniStreamRoundTrip|TestRawQUICBidiSignedEncryptedSendReplyReceiptReplay)$' -count=2 -timeout=180s
```

Affected vet, formatting and diff checks passed. Output is retained in
`/tmp/airc-roadmap-baseline/quic-uni-qualification-race-fixed.out`.
The initial run regenerated default authenticated timestamps during replay;
fixing issue/expiry times made replay intent identical. Its failure is preserved
in `quic-uni-qualification-race.out`. This qualifies local Darwin paired streams.
Datagrams and fresh final repository acceptance remain separate, including the
incomplete full race gate.
