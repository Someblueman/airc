# Gateway identity merge: performance check

Measured 11 October 2026. Did the merge of PR #1 slow anything down? It brought
gateway IRC-style identity, WEBIRC source forwarding, a cross-connection
failed-credential throttle, the ephemeral-guest nick check and SASL 779.
Baseline is `9d37a96`, main before the merge. Candidate is `18c00f8`, main
after it. The fix described below was then committed on top as `a5e1113`.

## Environment and method

The host was a 4-vCPU Intel Xeon at 2.10 GHz with 15 GiB RAM, running linux/amd64,
ext4 and Go 1.27.0 (GOMAXPROCS 4). **The machine was shared:** other workers'
test suites ran at the same time, and the load average moved between 1 and 5.6.
Absolute numbers are therefore noisy. Only differences inside an interleaved
comparison mean anything.

- Test binaries and executables were built once per tree with `go test -c` and
  `go build`. Base and head then ran alternately, with `-count=1 -benchmem`
  each round ([round.sh.txt](round.sh.txt)). There were 12 rounds of all
  benchmarks, then a 10-round re-run of every case that came out significant.
- The `airc-load` matrix from [load-2026-10-03](../load-2026-10-03/README.md)
  ran alternately, reversing base/head order each repetition, with 5
  repetitions per case. Cases were persistent history with 100 agents, and
  memory-only with 500 agents (30 s measured, 5 s warmup, rate 1). The larger
  cases do not fit on a shared 4-CPU host.
- Gateway end to end used a scratch native `aircd` on a Unix socket with fsync
  history, and the gateway's HTTP listener with a fresh journal. Base and head
  alternated over 10 repetitions. Each run made 10 warmup sends, then 200
  sends with 8 concurrent posters (or 60 sequential sends). Times run from POST
  until an independent native observer sees the message. The harness is
  derived from `examples/gateway/scratch`:
  [base](sendbench-base.go.txt) and [head](sendbench-head.go.txt) versions.
  **The configurations differ:** base authenticates a principal with a bearer
  token bound to a native account, while head names the author with
  `AIRC-Nick`/`AIRC-Token`. Both log in to the same native account with SASL
  on every job. Head's per-author flood limit (60 requests/min by default)
  rejected 150 of 200 sends with HTTP 429 in a first run. Head runs therefore
  set `author_requests_per_minute` and `requests_per_minute` to 100000.
- `benchstat` (golang.org/x/perf) produced every p-value. Load and gateway
  metrics were fed to it as one custom-unit sample per repetition.

## Results

**Unchanged** (12 rounds, all p ≥ 0.05): `BenchmarkHistory/*`,
`BenchmarkHistoryAddWithQuota`, `BenchmarkContextWireBudget/*`,
`BenchmarkUIAppendRedraw`, gateway `BenchmarkHistoryPage/*`, and every
`BenchmarkConnect` time except SASL. The history code itself did not change.
`ValidHistoryID/invalid-first` came out 13% faster (p=0.000), but that function
did not change: this is a code-layout or noise effect.

**Did not reproduce:** In the first pass, `AgentCLI` login/send/check were
7–8% slower (p=0.007–0.039) and `Connect/account_sasl_ephemeral` was 5.4%
slower (p=0.014). In the 10-round re-run, crossing base/head CLI binaries with
base/head servers, none of these timings was significant (p=0.075–0.74).
See [rerun-benchstat.txt](rerun-benchstat.txt).

**Real but by design:** each SASL login costs 12 more allocations and 0.7 KiB
more (+5%, p=0.000, deterministic). The extra 779 numeric
(`internal/server/sasl.go:128`) accounts for the difference: it is formatted
on the server and parsed by `pkg/irc`, as an alloc-profile diff showed. The
same line explains +6 allocs per `AgentCLI` command. Every connect also
allocates about 60–80 B more.

**Load generator** ([load-benchstat.txt](load-benchstat.txt),
[load-summary.csv](load-summary.csv)): no significant difference in any
metric. All 15,000 and 3,000 scheduled slots completed in every run.

| Case | Post p50 base / head (ms) | Post p99 base / head (ms) | ops/s | Server CPU s |
| --- | ---: | ---: | ---: | ---: |
| persistent, 100 agents | 0.942 / 0.942 (p=0.84) | 3.34 / 2.84 (p=0.095) | 99.7 / 99.7 | 2.71 / 2.73 |
| memory, 500 agents | 0.617 / 0.603 (p=0.55) | 2.56 / 2.72 (p=1.0) | 498.2 / 498.1 | 15.9 / 15.6 |

**Gateway send** ([gateway-send-benchstat.txt](gateway-send-benchstat.txt)).
With 8 concurrent posters and a single author, sends/s did not change
significantly (121.5 vs 115.3, p=0.075). Two things did change, both
significant and reproduced from the first run:

- The lane's per-job service time rose for a **registered author**. That time
  is the gap between consecutive observations while a backlog waits. The p50
  went from 7.21 to 8.31 ms (+15%, p=0.000) and the p90 from 8.67 to
  10.24 ms (+18%, p=0.000). A **guest author** showed no p50 change (7.37 ms,
  p=0.32).
- HTTP admission p50 rose from 4.35 to 4.67 ms (+7%, p=0.006) for a registered
  author, and to 4.89 ms (+13%, p=0.000) for a guest. The RecordAccount
  change below does not affect it (p=0.12); for the cause, see
  [After fixes](#after-fixes).

The sequential (c=1) numbers are dominated by the delivery runner's 200 ms
poll ticker (`internal/gateway/delivery/delivery.go:53`). Base observes every
send after 199.95 ms. Head shows 8.9 ms only because its extra completion
commit outlasts the next admission, so the runner re-polls straight away.
That is a race artefact, not a speed-up, and both trees can leave an idle
send waiting up to 200 ms.

## Root causes

**Extra journal commit per registered job.** `delivery.go:154-157` calls
`store.RecordAccount` (`internal/gateway/store/intake.go:491-502`) after the
outcome has already committed. It runs a separate autocommit `UPDATE`, with
its own fsync, while holding the store lock. A head build with that call
disabled cut the gap p50 from 8.56 to 7.60 ms (−11%, p=0.001; see
[gateway-recordaccount-ab.txt](gateway-recordaccount-ab.txt)). Fixed
afterwards; see [After fixes](#after-fixes).

**Throttle table sweep: fixed in `a5e1113`.** `noteFailedAddressLocked`
(`internal/server/access.go`) handled a new address on a full table
(4096 entries) by sweeping every entry, then evicting just one. As a result,
each failure from a new address cost a full sweep under the server lock. A
remote peer can trigger this from many /64s. The change evicts down to three
quarters full, so a sweep happens once per 1024 new addresses and the eviction
rate stays the same ([fix-benchstat.txt](fix-benchstat.txt)):

| Benchmark (head before → after, 10 rounds) | sec/op | p |
| --- | ---: | ---: |
| NoteFailedAddress/new_address_table_full | 192.8 µs → 0.515 µs (−99.7%) | 0.000 |
| GatewayConnect/wrong_sasl_new_source_table_full | 621.5 µs → 287.5 µs (−53.7%) | 0.000 |
| NoteFailedAddress/known_address | 150.7 ns → 149.7 ns | 0.59 |

## New benchmarks

These are added in `a5e1113`. `BenchmarkConnect` (`pkg/irc`) runs on both trees
and covers guest, SASL, access PASS and wrong-credential connects.
`BenchmarkGatewayConnect` (`pkg/irc`) and `BenchmarkNoteFailedAddress`
(`internal/server`) are head-only, because they need WEBIRC and the throttle
table.

```sh
go test ./pkg/irc -run '^$' -bench 'Connect' -benchmem
go test ./internal/server -run '^$' -bench NoteFailedAddress -benchmem
```

Race tests for `internal/server`, `pkg/irc` and `internal/gateway/backend`,
plus vet, gofmt and staticcheck, passed after the fix.

## After fixes

Three gateway commits on `bench-check`, then a merge of main `8fdf101`. Main
adds journal migration 25, which drops `UNIQUE(upstream_id)` and so writes one
index page fewer per admission. The final numbers come from the merged tree.
The pre-fix comparison is `8fdf101` itself. Interleaved rounds rotated the
build order: 12 for the send harness ([send-after.sh.txt](send-after.sh.txt))
and 10 for a store-level benchmark. That benchmark is added only through
`go test -overlay` ([store-bench.go.txt](store-bench.go.txt)). It times
admit, Ready, Reserve and the acceptance save for one job each. The harness
gained a `-port` flag: the ephemeral port range was full of TIME_WAIT
sockets.

**Root causes.** The store lock serializes every journal call, so any time
spent under it adds to admission latency. Under it:

- `delivery.go` committed the account ID separately, after the outcome
  (0.68 ms a registered job, store-level).
- `Ready` (`store/ready.go`) bound `LIMIT ?`. SQLite prepares that statement
  twice per call, and the call also scanned every retained job:
  0.70 ms against base's 0.49 ms. Ready runs after every completion.
- `admitSubmitterInTx` (`store/intake.go`) probed for an existing job with the
  36-column `readJob`, which is parsed on every admission. That cost about
  95 µs of CPU where base's fingerprint probe cost 40 µs.

Statements per admission did not grow (8 against base's 9).

**Fixes.**

- `104bdf2`: `Grant.NoteAccount` carries the account ID, and the outcome's
  own transaction writes it. An outcome and its account ID now commit
  together.
- `07b7ea6`: a narrow fingerprint probe before `readJob`. `Ready` applies the
  limit while reading rows and filters `terminal_at IS NULL`, so it searches
  the retention index instead of scanning.
- `1ed3a00`: admission signals a coalescing channel, and an idle runner polls
  at once. A busy runner ignores the signal and still polls on completion,
  and Ready and Reserve still gate every attempt.

Measured before the merge, each fix against `00fb97c`
([after-send-perfix-benchstat.txt](after-send-perfix-benchstat.txt)):

- `104bdf2` cut the registered gap p50 by 5.8% (p=0.003).
- `07b7ea6` cut it by 14.4% overall and admission p50 by 7.0% (p=0.001).

**Merged tree** ([after-send-benchstat.txt](after-send-benchstat.txt),
[after-store-benchstat.txt](after-store-benchstat.txt)):

| Metric | base | pre-fix main | fixed | fixed vs main | fixed vs base |
| --- | ---: | ---: | ---: | ---: | ---: |
| registered c8 gap p50 (ms) | 6.92 | 8.21 | 7.14 | −13.0% (p=0.000) | ~ (p=0.13) |
| registered c8 gap p90 (ms) | 8.29 | 9.96 | 8.79 | −11.7% (p=0.000) | ~ (p=0.48) |
| registered c8 admit p50 (ms) | 4.23 | 4.54 | 4.34 | −4.4% (p=0.020) | ~ (p=0.37) |
| guest c8 admit p50 (ms) | — | 4.59 | 4.35 | −5.3% (p=0.001) | — |
| c8 sends/s | 125.2 | 117.1 | 134.4 | +14.8% (p=0.000) | +7.4% (p=0.000) |
| c1 observed p50 (ms) | 199.9 | 8.4 | 7.3 | −12.9% (p=0.000) | −96.3% (p=0.000) |
| store work per registered job (ms) | 5.43 | 6.52 | 5.45 | −16.4% (p=0.000) | ~ (p=0.97) |
| Ready (µs) | 493 | 704 | 374 | −47.0% (p=0.000) | −24.3% (p=0.000) |

The pre-fix sequential figure of 8.4 ms is the race artefact described above,
and its p90 swings to 36 ms (±246%). Fixed, the p90 is 8.9 ms (±4%). The
remaining admission difference from base is not significant at either the
store or the HTTP level.

Admission still writes the `job_request` index. Dropping it saves 4.3 KB of
WAL per admission, but changes no timing significantly (p≥0.19), and
maintenance needs it.

**Per-author limit (not changed).** aircd has no message rate limit for a
direct client. It has only opt-in per-room slow mode, the failed-credential
throttle and limits on pending connections per address, with loopback and
Unix peers exempt. The gateway allows 60 author requests a minute, refilled at
1/s. Reads such as `receipt.get` count against the same bucket, and a
120-a-minute per-source limit applies on top. In the harness that rejected 150
of 200 burst sends. `pending_per_author` (128) and the single delivery lane
per author already bound an author's backlog: the lane runs about 130 jobs/s
here. A proposed default is 1200 a minute per author and 2400 per source, with
loopback exempt from the per-source limit as in aircd. That keeps a limit
against runaway loops without making the gateway stricter than aircd for
ordinary bursts.
