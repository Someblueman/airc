# Concurrent load baseline

Measured 3 October 2026 with the [load harness](../../LOAD_TESTING.md), on Apple
M4, 24 GiB RAM, macOS arm64, Go 1.25.0, GOMAXPROCS 10. These are synthetic
ephemeral agent sessions on persistent TCP connections, not model invocations.
The installed daemon was untouched. Each case used a separate loopback server
process, ten rooms, 256-byte bodies, 10,000-message retention, five-second
operation deadlines and existing queue defaults. History storage was on the
local temporary filesystem. No other benchmark/test suite ran concurrently;
the host was otherwise uncontrolled.

Application source was unchanged from `9b303ae`; the build metadata records
parent commit `2a65003` with the new harness uncommitted. The matching harness is
committed with this evidence. For exact identification, SHA256 of sorted
`cmd/airc-load/*.go`, concatenating each filename, a NUL and its contents:
`23ba0d3388fec0c38710a5d0f10b9f19f09496b1840e56ead680d2b04c6148e1`.

## Workload and results

Each agent scheduled one operation per second: 40% posts, 20% mentions and 10%
each replies, reactions, checks and searches. Each case had five seconds of
warmup followed by 30 seconds measured. All requested agents connected and
completed subscription/seed setup in every case.

Post confirmation latency below is from individual successful requests, in
milliseconds. Completion is across **all six operation types**, with missed
slots included in the denominator. Throughput includes final operation/drain
time; it is not the configured offered rate.

| History | Agents | Post p50 | Post p75 | Post p90 | Post p99 | Completed scheduled actions | Successful ops/s | Server peak RSS MB |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Persistent | 50 | 4.61 | 4.69 | 4.76 | 5.22 | 1,500 / 1,500 | 49.8 | 30.0 |
| Persistent | 100 | 4.26 | 4.47 | 4.72 | 5.90 | 3,000 / 3,000 | 99.7 | 35.8 |
| Persistent | 500 | 1,851.09 | 1,908.86 | 1,934.92 | 2,010.79 | 10,886 / 15,000 | 333.8 | 79.3 |
| Persistent | 1,000 | 3,924.77 | 4,081.63 | 4,121.49 | 4,203.72 | 10,064 / 30,000 | 291.5 | 125.4 |
| Memory only | 1,000 | 0.39 | 0.48 | 0.58 | 1.67 | 30,000 / 30,000 | 996.4 | 126.5 |

The persistent 500-client case missed 4,114 slots (27.43%); the 1,000-client case
missed 19,936 (66.45%). All attempted measured operations in these cases
completed successfully; no measured timeout, rejection or disconnection was
recorded. The persistent matrix exited nonzero because missed scheduled work is
an incomplete workload. The memory-only control exited successfully. Warmup and
setup outcomes are reported separately in the JSON.

The slowdown therefore appears before 500 continuously active clients at this
rate, despite accepting all 1,000 connections. This experiment does not locate
the exact saturation threshold. It also does not establish a maximum agent count:
less active clients impose a different load.

The 1,000-client memory-only control completed more work with much lower latency.
Together with the source's serialized history append/Sync path, this supports
investigating persistence batching next. It does not isolate Sync from every
other persistence cost, prove the benefit of a proposed implementation, or justify
weakening durability. Group commit should preserve persisted-receipt semantics
and be compared using exactly this disk-backed fixture.

Skipped slots change the actual operation mix under saturation. For example,
the 500-client case completed only 326 of 1,500 scheduled replies. Read operation
counts and success latency together; do not interpret the pooled successful
throughput as a sustained fixed-mix capacity guarantee. No automatic retries or
reconnections were performed.

## Evidence and reproduction

- [summary.csv](summary.csv) contains p50/p75/p90/p99/max and outcome counts for
  every operation and case, without pooling different actions.
- [persistent/results.jsonl](persistent/results.jsonl) and
  [memory/results.jsonl](memory/results.jsonl) retain exact configuration, build,
  setup/warmup metrics and server/generator resource readings.
- Each mode directory contains `N-samples.csv.gz` and `N-setup.csv.gz`, containing
  every measured slot and setup observation. CSV units are nanoseconds; summary
  percentile units are milliseconds. Decompress with `gzip -dc FILE.csv.gz`.

```sh
go build -o /tmp/airc-load ./cmd/airc-load
/tmp/airc-load -agents 50,100,500,1000 -duration 30s -warmup 5s -rate 1 -out /tmp/airc-load-persistent-repeat
/tmp/airc-load -agents 1000 -duration 30s -warmup 5s -rate 1 -persist=false -out /tmp/airc-load-memory-repeat
```

All archived percentile values and scheduled/success counts were independently
recomputed from the raw CSV before saving this report. The real chat/cancellation
test, full race suite and vet passed. A separate CLI interrupt check returned an
incomplete report and left no server child running.

This is one exploratory run per configuration, in ascending client-count order,
followed by the memory control. For optimization acceptance, repeat baseline and
candidate runs in alternating order. At 50 clients, the less frequent operations
have only 150 observations; their p99 is supported by very few tail samples.
The 1,000-client memory control has 3,000 observations for each 10% action and
12,000 posts. Neither sample size eliminates host noise or establishes remote
TLS/SASL latency. Peak RSS includes startup/warmup and is not a leak measurement.
