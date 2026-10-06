# Gateway test selection and acceptance

Run commands from the repository root. Tests use private state, scratch listeners
and owned child processes; they must not use the live daemon or default service
port. A sandbox that denies scratch socket binds cannot validate these fixtures.

For a localized change, start with the affected package and its meaningful
failure boundaries. Examples of existing focused checks:

```sh
go test ./internal/gateway/core -run '^TestDecodeBoundaries$' -count=1
go test ./internal/gateway/backend -run '^TestBounded(Channels|Agents)Collector$' -count=1
go test ./pkg/irc -run '^Test(ChannelsRaw|AgentsRaw|ObserveSignalsRaw|ObserveSelects)' -count=1
go test ./internal/gateway/adapter -run '^TestMCP' -count=1
go test ./integration/gateway -run '^TestCurrentGatewayBinaryKeepsFixturesIsolated$' -count=1 -timeout=180s
go vet ./internal/gateway/backend ./pkg/irc
```

Replace the selection with tests that exercise the changed behavior; these
examples are not a mandatory sequence. Add `-race` for changed concurrency or
lifetime ownership. Published contracts, authorization, cryptography, durable
state and cross-process behavior require the relevant broader checks even for
small diffs. Integration tests need enough time for the first fixture build.

`-count=1` bypasses successful test-result caching. Gateway integration builds
`cmd/airc-gateway` at runtime and exports/builds a pinned native fixture. Native
CLI subprocess tests also build their child executable at runtime, and some
example checks invoke `go run`. Go's test cache does not model all source inputs
of these external build commands: an integration-package cache hit can skip
rebuilding and exercising a changed gateway executable. Use `-count=1` whenever
validating those boundaries or measuring timings. The Go compilation cache still
applies; bypassing test-result caching does not imply a cold build. Record cache
conditions rather than deleting a shared build cache for routine checks.

Only immutable binaries may be shared across fixtures. Each invocation must own
its accounts, credentials, configuration, state, sockets and process lifetime.
Per-process lazy builds do not constitute a verified cross-run fixture cache.

At integration checkpoints, run full normal and race acceptance on unchanged
executable source:

```sh
go test ./... -count=1 -timeout=900s -p=1
go test -race ./... -count=1 -timeout=900s -p=1
go build ./cmd/...
go vet ./...
```

Check formatting with `gofmt -l` on changed Go files and whitespace with
`git diff --check`. Repository guidance also calls for staticcheck; use the
available repository toolchain without introducing a dependency upgrade merely
to run it. Full acceptance is required after architectural, authentication,
durable-state, published-API or cross-carrier changes, and before final roadmap
acceptance. A documentation-only checkpoint does not need another runtime run.

Capture source revision, Go version, host/load and cache conditions, command,
terminal exit result and package/leaf timings for performance comparisons.
Separate normal from race measurements. `-p=1` is the comparable starting point;
measure package concurrency separately from within-package parallelism. Runtime
child instrumentation and fixed native deadlines are part of the workload.
Keep one genuine expiry/deadline witness wherever the test makes that claim.
Do not report speed improvement from a skipped assertion, missing race coverage,
failed run or unmatched workload. The final acceptance execution must be fresh;
earlier baseline or affected-test passes do not certify the final source tree.

## Normal baseline before test-speed changes

On 2026-10-06, Go 1.27.1 on Darwin arm64 ran
`go test -json ./... -count=1 -timeout=900s -p=1` from Go/test source revision
`71181079a3e5cf1c9ff833267d83cabc7e8a0ea5`. Subsequent documentation checkpoints
changed no Go files, module files, example fixtures or embedded skill inputs.
Compilation caches were warm from prior builds; successful test-result caching
was disabled. No competing benchmark or race run was started during this run.
Host load averages were 14.79/15.96/15.64 before and 50.23/24.46/18.94 after;
this is a loaded-host baseline, not a stable performance estimate.

The command exited 1 after 971.14 seconds wall time (127.33 user, 33.08 system).
Gateway integration timed out at 900.483 seconds while
`TestUnixgramQuietWatchHeartbeatAndLeaseExpiry` was running. Its two surviving
scratch children were identified by exact PID and temporary executable path,
then stopped with SIGINT. Every other executed package passed; there was no
individual test assertion failure before the integration timeout. Tests after
the interrupted Unix-datagram check did not run and are not covered by this pass
record. This is baseline evidence, not full acceptance.

Completed package times include CLI 25.619s, adapter 11.042s, store 9.133s,
backend 2.535s, server 1.644s and SDK 0.964s. The slowest completed leaf tests
(excluding aggregate parent durations) were:

| Test | Seconds |
|---|---:|
| `TestDTLSMalformedAndUnauthorizedRecordsDoNotRenewIdle` | 32.570 |
| `TestCoAPRevocationDeniesFreshWorkAndExpiresExistingLease` | 16.390 |
| `TestRPCQuietStreamReturnsCorrelatedTerminalAndClosesLease` | 16.330 |
| `TestGRPCQuietStreamTerminalAndNativeLeaseCleanup` | 16.310 |
| `TestMCPHTTPWatchRenewsAndResumesSealedCursor` | 16.290 |
| `TestDTLSQuietWatchContiguousHeartbeatAndLeaseExpiry` | 16.270 |
| `TestUDPQuietWatchHeartbeatCursorAndLeaseExpiry` | 16.270 |
| `TestRawQUICQuietDatagramWatchExpiresWithContiguousSequence` | 16.250 |
| `TestHTTPEventsRetainedPrefixResumeAndLeaseExpiry` | 15.060 |
| `TestReplySignalOverUnixAndStdio` | 12.150 |

These totals include fixture setup and cleanup; separate build and startup
measurements are recorded below. The current gateway child
was built with `-race` even for this normal parent run, with SHA-256
`22052f470807138817b44f1c365ca0a6fc16f8aa18d100bc384ec61425089880`.

## Equivalent race baseline

The separate race command was
`go test -race -json ./... -count=1 -timeout=900s -p=1`, with the same Go source,
fixtures, package ordering, test selection and compilation-cache conditions.
It started at documentation revision `8e36296532b42861715d7d585776007bb6d04eca`;
the immutable gateway child was built at documentation revision
`f0c29c1f5d61801a6839f86ef0d80fc89e8f7cea`. Neither changed executable source
from `7118107`. Both baseline children report `-race=true`; their differing
hashes reflect Git revision metadata. The race child's SHA-256 was
`456ddf158f09652239903f47771dfd4ac659336ded91502d6687edb4e5901bdf`.
The pending child-build test changes were prepared only after this integration
test process and its shared gateway child were loaded. No verification build
or competing benchmark was run until the baseline terminated.

The command exited 1 after 1046.39 seconds wall time (194.38 user, 36.42 system).
Integration timed out at 900.684 seconds during `TestUnixgramAllSharedOperations`.
Its two surviving scratch children were verified by PID/path and stopped with
SIGINT. Other executed test packages passed; tests beyond the interruption
remain unverified. Host load averages were 50.23/24.46/18.94 before and
9.32/13.09/18.61 after. These unequal, changing loads prevent using the two full
runs as a stable normal-versus-race speed comparison.

Completed package times include CLI 45.729s, store 29.928s, adapter 13.699s,
server 5.003s, backend 3.752s and SDK 2.607s. Ranked completed leaf tests were:

| Test | Seconds |
|---|---:|
| `TestDTLSMalformedAndUnauthorizedRecordsDoNotRenewIdle` | 32.670 |
| `TestDTLSQuietWatchContiguousHeartbeatAndLeaseExpiry` | 16.360 |
| `TestMCPHTTPWatchRenewsAndResumesSealedCursor` | 16.320 |
| `TestGRPCQuietStreamTerminalAndNativeLeaseCleanup` | 16.270 |
| `TestRawQUICQuietDatagramWatchExpiresWithContiguousSequence` | 16.270 |
| `TestCoAPRevocationDeniesFreshWorkAndExpiresExistingLease` | 16.260 |
| `TestUDPQuietWatchHeartbeatCursorAndLeaseExpiry` | 16.250 |
| `TestRPCQuietStreamReturnsCorrelatedTerminalAndClosesLease` | 16.230 |
| `TestHTTPEventsRetainedPrefixResumeAndLeaseExpiry` | 15.170 |
| `TestUIThreadAndSearchPagesSurviveReconnect` | 14.030 |

## Build and startup decomposition

Supplemental measurements used the same executable source and warm Go build
cache, after both full baseline commands had terminated. These are individual
observations on a loaded host, not stable build-performance distributions.
Pinned native export took 0.033s, extraction 0.043s and build 0.272s (0.349s
total); the exported archive passed its expected SHA-256 check. Gateway builds
took 1.227s with race instrumentation and 1.645s without it. Fresh-process
`config validate --file examples/gateway/mcp-http.disabled.json`, after one
warmup, took race samples 1.040/1.042/1.029s and normal samples
0.0167/0.0170/0.0170s. This includes CLI validation and exit, not listener
readiness. Load averages changed from 7.16/11.28/17.06 to 8.71/11.48/17.07.

A temporary, removed integration probe separately timed lazy gateway build,
pinned export/build, then three independently owned real TLS fixture setups
through their ready signal. Setup includes native provisioning, certificates,
config, journal and service startup; teardown is excluded. It called the
existing `currentGatewayBinary`, `pinnedFixtureBinary` and
`newIsolatedGateway` helpers. Each command used
`go test -race=<mode> -json ./integration/gateway -run '^TestGatewayFixtureCostProbe$' -count=1 -timeout=180s`.
All four commands passed. The first fixture is shown separately because it
includes initial setup effects; the remaining samples are not full-suite
estimates.

| Parent / child mode | Gateway build | Pinned export/build | First ready | Later ready samples |
|---|---:|---:|---:|---:|
| Normal / race (baseline) | 2.307s | 0.763s | 1.084s | 0.212 / 0.229s |
| Normal / normal (candidate) | 1.720s | 0.460s | 0.734s | 0.053 / 0.054s |
| Race / race (baseline) | 2.428s | 0.707s | 0.868s | 0.216 / 0.278s |
| Race / race (candidate) | 1.487s | 0.474s | 0.849s | 0.197 / 0.203s |

Raw command JSON, stderr, source/build metadata and timing scripts for this
measurement session are retained locally in `/tmp/airc-roadmap-baseline`.
The full normal and race baseline captures are complete; their timeout results
leave full acceptance unfinished.

## Child race mode checkpoint

The gateway child now receives explicit `-race=true` or `-race=false` from the
parent test's build tag. Explicit false also overrides inherited `GOFLAGS=-race`.
The existing two-fixture acceptance test reads the child's actual build metadata
and asserts that it matches the parent, then retains its independent state and
native-message assertions.

Comparable before/after commands selected that unchanged acceptance behavior:
`go test -race=<mode> -json ./integration/gateway -run '^TestCurrentGatewayBinaryKeepsFixturesIsolated$' -count=4 -timeout=180s`.
All four command groups passed. The first repetition includes lazy build/setup
and was designated warmup; all samples are recorded here.

| Mode / helper | First repetition | Subsequent repetitions |
|---|---:|---:|
| Normal / baseline | 6.16s | 2.75 / 2.75 / 2.73s |
| Normal / candidate | 10.22s | 0.51 / 0.56 / 0.52s |
| Race / baseline | 6.92s | 2.94 / 3.01 / 2.95s |
| Race / candidate | 4.72s | 2.89 / 3.04 / 3.04s |

Normal warm median fell from 2.75s to 0.52s for this fixture. The slower candidate
first repetition reflects unequal initial compilation-cache state and prevents
claiming a build-time improvement. Race samples support retained behavior, not
a robust speedup; the before/after race child binaries had identical SHA-256.
Host load changed across these sequential runs (before normal 9.17/12.89/18.44;
before candidate race 12.33/12.95/18.10). These observations do not establish full-suite
speedup. An additional `GOFLAGS=-race go test -race=false` execution of the same
acceptance test passed, as did integration-package vet, formatting and diff checks.

## Pinned native build reuse decision

Retain the existing lazy, per-process pinned native build. The measured warm
export/extract/build cost was 0.349s; complete helper observations ranged
0.460–0.763s in the readiness probe. This cost does not justify a new durable
cross-run artifact cache and its additional input/integrity ownership.
`TestPinnedFixtureSharesOnlyImmutableBinary` passed in both recorded full
baseline runs (normal 0.04s, race 0.06s), verifying shared immutable executable
reuse alongside fresh accounts and history. No persistent cache is introduced.

## Shared native CLI executable

Native CLI subprocess tests and the CLI benchmark now lazily build one immutable
executable per test process. MCP, conversation and real PTY callers retain their
own servers, credentials, state and child lifetimes. The benchmark still honors
`AIRC_BENCH_BINARY`; no production CLI behavior or child build mode changes.
`TestMain` removes the shared executable directory after checks finish.

Before/after commands selected the existing three subprocess acceptance tests
with `-count=3`, `-timeout=180s`, JSON output and normal/race parent modes:
`TestConversationEffortThreeMCPCalls`, `TestMCPRealStdioToolsAndCancellation`,
and `TestUIRealPTYConversationAndInputSafety`. All four runs passed, including
the real Python PTY check. Samples include all repetitions, not just warm runs:

| Test | Normal before | Normal after | Race before | Race after |
|---|---|---|---|---|
| Conversation | 1.76 / 0.99 / 1.00s | 1.26 / 0.07 / 0.06s | 0.98 / 0.71 / 0.65s | 0.91 / 0.08 / 0.11s |
| MCP | 0.95 / 0.95 / 0.96s | 0.06 / 0.07 / 0.05s | 1.07 / 0.70 / 0.70s | 0.10 / 0.09 / 0.10s |
| PTY | 1.79 / 1.79 / 1.76s | 0.86 / 0.93 / 0.91s | 1.86 / 1.50 / 1.50s | 0.94 / 0.93 / 0.95s |

Selected command wall time was 13.46 → 6.00s normal and 11.98 → 8.49s race,
including Go compilation and process startup. Warm compilation cache and
sequential sample order constrain the claim to avoided repeated builds in these
fixtures; this is not a full-suite speed measurement. Pre-run host loads were
10.04/10.83/14.14, 11.01/11.00/14.14, 10.72/10.97/14.01 and
10.26/10.87/13.95 respectively (before normal/race, after normal/race).

The complete affected CLI package subsequently passed normal and race checks
with `-count=1 -timeout=180s`; the real CLI benchmark passed with
`-run '^$' -bench '^BenchmarkAgentCLI$' -benchtime=1x`. Package vet, formatting
and diff checks passed. The repository-wide final gate remains outstanding.

## Shared framed-client example

The real framed-client example is built lazily once per integration test
process, shared across TCP/TLS and Unix-stream invocations, and removed by the
existing process-level cleanup. Each invocation retains its own arguments,
signed/encrypted input, correlation validation and process lifetime. The build
finishes before its existing 20s/30s client execution deadline starts.

Before/after normal commands ran the unchanged
`TestUnixStreamFramingAndEncryptedProfileBoundary` with `-count=3 -timeout=180s`.
Both passed. Baseline samples were 8.50/5.31/5.32s; final candidate samples were
7.96/5.11/5.12s. These preserve the real five-second incomplete-frame closure
witness. Total command wall times were 21.12s and 21.93s respectively, so these
samples support modestly reduced repeated invocation cost, not total-command
speedup. Warm compilation caches and different initial test compilation cost
remain relevant; pre-run loads were 11.52/11.26/13.70 and 10.53/11.20/13.44.

Affected `^Test(FramedCLI|UnixStream)` checks subsequently passed normal and race
modes with `-count=1 -timeout=180s`, covering TCP, TLS, Unix streams and their
failure boundaries; integration vet, formatting and diff checks also passed.
These Darwin results do not certify Linux runtime behavior.
