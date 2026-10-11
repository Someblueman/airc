# WebTransport bidirectional acceptance evidence

> Dated evidence. Runs recorded here predate the
> [identity removal](removed-identity.md): their principals, signed or
> encrypted requests and return routes no longer exist.

`TestWebTransportReplayKeepsNativeIDAndBody` sends encrypted authenticated
intent over actual bidirectional streams to the standalone gateway. Replay must
preserve durable native identity with one send attempt. Two independently
observed messages establish exact IDs, authors, targets and complete multiline
Unicode bodies. One-record history pages through bidirectional streams must
preserve those observations with correct more/complete statuses, nonempty
cursors and no gaps; final saved-cursor resume returns an empty complete page.

Signed/encrypted bidirectional sends and the existing complete native read and
capability matrix ran alongside this witness. All three tests passed twice under
race detection (terminal exit 0, 12.693 seconds):

```sh
go test -race ./integration/gateway -run '^(TestWebTransportReplayKeepsNativeIDAndBody|TestWebTransportBidiSignedAndEncrypted|TestWebTransportReadFactsAndCapabilities)$' -count=2 -timeout=180s
```

Affected vet, formatting and diff checks passed. Outputs are retained in
`/tmp/airc-roadmap-baseline/webtransport-bidi-qualification-race.out` and
`webtransport-bidi-qualification-vet.out`. This qualifies local Darwin Go-client
bidirectional streams. Paired unidirectional streams, datagrams and actually
trusted browser certificates remain separate roadmap items. Fresh final
repository acceptance, including the incomplete full race gate, remains open.
