# WebTransport paired unidirectional acceptance evidence

`TestWebTransportUniStreamRoundTripAndLargeBody` sends every request through an
opened unidirectional stream and receives its correlated result through a
separate accepted unidirectional stream. Send, receipt lookup, replay, history
and cursor resume all use this pairing; none use the bidirectional call helper.
Every response must preserve correlation, operation and result event without
an error. Response streams are cancelled after their bounded frame is consumed.
Response decoding has its own five-second read deadline after stream acceptance.

The encrypted authenticated 3500-byte send must receive durable admission and
native acceptance. An independent observer checks the ID, complete body, author
and target. Replay preserves acceptance with one send attempt. A one-record
history page must preserve the native record, complete status, nonempty cursor
and no gap. Following that cursor returns an empty complete page.

This test and the retained bidirectional replay/page witness passed twice under
race detection (terminal exit 0, 10.578 seconds):

```sh
go test -race ./integration/gateway -run '^(TestWebTransportUniStreamRoundTripAndLargeBody|TestWebTransportReplayKeepsNativeIDAndBody)$' -count=2 -timeout=180s
```

Affected vet, formatting and diff checks passed. Outputs are retained in
`/tmp/airc-roadmap-baseline/webtransport-uni-qualification-race.out` and
`webtransport-uni-qualification-vet.out`. This qualifies local Darwin Go-client
paired streams. Datagrams, trusted browser certificates and fresh final
repository acceptance remain separate, including the incomplete full race gate.

Commit review found that the accept timeout did not bound decoding after a
response stream arrived. A read deadline was added; the affected paired-stream
test passed twice under race detection (exit 0, 7.268 seconds). Output is retained
in `webtransport-uni-read-deadline-race.out` under the same evidence directory.

The send request now fixes its issue and expiry times before replay, avoiding
different authenticated intent if receipt polling crosses a clock second.
The paired-stream test passed three times under race detection after this change
(exit 0, 8.825 seconds), with output in
`webtransport-uni-fixed-intent-race.out` under the same evidence directory.
