# Connection health validation

Measured 3 October 2026 on the same Apple M4/macOS/Go 1.25.0 host as the
[load baseline](../load-2026-10-03/README.md), using isolated server processes.
The harness now records distinct connection losses, including idle closures,
separately from failed operations and missed scheduling slots.

| Scenario | Agents connected | Connected at end | Observed peer losses | Timeout closes | Injected socket closes | Measured action outcomes |
| --- | ---: | ---: | ---: | ---: | ---: | --- |
| Normal disk-backed load | 50 | 50 | 0 | 0 | 0 | 500 successful |
| Normal disk-backed load | 1,000 | 1,000 | 0 | 0 | 0 | 4,005 successful; 5,995 missed slots |
| Memory-only load with 10% reset | 50 | 45 | 5 | 0 | 5 | 475 successful; 25 unavailable slots |

All three cases used ten rooms, 256-byte bodies, one scheduled action per agent
per second, a two-second warmup and ten seconds measured. The fault was injected
five seconds into measurement. All five observed losses were detected while the
agents were idle. Each affected client was counted once, despite subsequent
unavailable slots. No transport-operation failures, other client-error closes,
cancellation closes or wire `ERROR` notices were observed in these completed
cases. A TCP reset need not carry an explanatory application message.

The normal 1,000-client run exited nonzero because of missed work, despite losing
no observed connections. The fault case exited nonzero because of injected
connection losses and unavailable work. The normal 50-client case passed. This
distinguishes overload latency from connection loss; it does not establish WAN
reliability, complete message delivery or recovery after reconnection.

## Reproduce

```sh
go build -o /tmp/airc-load ./cmd/airc-load
/tmp/airc-load -agents 50,1000 -duration 10s -warmup 2s -out /tmp/airc-health-normal
/tmp/airc-load -agents 50 -duration 10s -warmup 2s -persist=false \
  -disconnect-percent 10 -disconnect-after 5s -out /tmp/airc-health-reset
```

See [load testing](../../LOAD_TESTING.md#connection-health-and-controlled-faults)
for counter definitions and limitations. The loss fraction includes injected
faults; injected and observed counts describe the same fault from different sides
and must not be summed.

[summary.csv](summary.csv) contains the counters above.
[normal-results.jsonl.gz](normal-results.jsonl.gz) and
[reset-results.jsonl.gz](reset-results.jsonl.gz) retain all per-client closure
records, operation distributions and resource/configuration metadata. The
`normal-N-*.csv.gz` and `reset-N-*.csv.gz` files contain raw setup/action samples.
Decompress with `gzip -dc FILE`. Aggregates were checked against unique per-client
records before archiving.

Build metadata identifies parent `21271c3` plus the working changes committed with
this report. Harness source fingerprint, using sorted Go filenames, NUL and file
contents as in the earlier baseline:
`4bd13012599e4b83b76e88e046a861aa22b0bdd915e9067acb11fc6dd67102d6`.

The full race suite and vet passed. Targeted tests cover real idle/in-flight
socket resets, explicit server notices, timeout/cancellation classification and
no double counting. A race-instrumented CLI fault run detected both of two
injected losses without race warnings. Interrupting a run before its fault timer
fired stopped the client and server processes cleanly with no remaining child.
These instrumented checks are verification, not performance baselines.
