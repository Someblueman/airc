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
  author, and to 4.89 ms (+13%, p=0.000) for a guest. The cause was not
  isolated: the RecordAccount change below does not affect it (p=0.12).

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
[gateway-recordaccount-ab.txt](gateway-recordaccount-ab.txt)). **Proposed
fix, not implemented:** carry the account ID on the grant, set from
`backend.Run`. Then `saveAcceptance` and `Complete` can write `account_id` in
their existing transactions, and the separate call goes away. That touches the
journal's commit paths, so it needs its own review.

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
