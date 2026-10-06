# Raw QUIC bidirectional acceptance evidence

`TestRawQUICBidiSignedEncryptedSendReplyReceiptReplay` uses real standalone
gateway bidirectional streams with signed and encrypted authenticated requests.
Durable send/reply acceptance and stable replay are checked against a separate
native CHECK containing exactly two records, including complete multiline
Unicode body, author, target and reply linkage. Replay preserves one send attempt.

One-record history pages compare every metadata field with that native CHECK,
require correct more/complete statuses, nonempty cursors and no gaps. Two final
saved-cursor resumes return empty complete pages. The bidi helper checks response
correlation and operation and bounds stream reads/writes with a five-second
deadline. History/capabilities and tampered-authentication/zero-frame rejection
tests ran alongside the witness.

All four tests passed twice under race detection (exit 0, 15.144 seconds):

```sh
go test -race ./integration/gateway -run '^(TestRawQUICBidiSignedEncryptedSendReplyReceiptReplay|TestRawQUICHistoryAndCapabilitiesMatchNative|TestRawQUICTamperedEnvelopeReturnsAuthenticationFailure|TestRawQUICRejectsZeroLengthFrame)$' -count=2 -timeout=180s
```

Affected vet, formatting and diff checks passed. Outputs are retained in
`/tmp/airc-roadmap-baseline/quic-bidi-qualification-race.out` and
`quic-bidi-qualification-vet.out`. This qualifies local Darwin raw QUIC bidi
behavior. Paired unidirectional streams, datagrams and fresh final repository
acceptance remain separate, including the incomplete full race gate.
