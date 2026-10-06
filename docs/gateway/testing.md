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
go test ./... -count=1 -timeout=900s -parallel=3 -p=2
go test -race ./... -count=1 -timeout=1800s -p=1
go build ./cmd/...
test -z "$(gofmt -l .)"
go vet ./...
go run honnef.co/go/tools/cmd/staticcheck@latest ./...
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
measure package concurrency separately from within-package parallelism. The
[normal package comparison](package-concurrency-testing.md) supports `-p=2`
with `-parallel=3` on the measured host; race retains `-p=1` pending separate
evidence. Runtime
child instrumentation and fixed native deadlines are part of the workload.
Keep one genuine expiry/deadline witness wherever the test makes that claim.
Do not report speed improvement from a skipped assertion, missing race coverage,
failed run or unmatched workload. The final acceptance execution must be fresh;
earlier baseline or affected-test passes do not certify the final source tree.

## Normal baseline before test-speed changes

Fresh normal acceptance at `9b217bee14bdbb637645e589d426b28ad26884f6`
passed with `go test ./... -count=1 -timeout=900s -parallel=3 -p=2`.
Every package completed successfully; wall time was 536.67 seconds
(31.51 user, 22.72 system). Go was 1.27.1 on Darwin arm64, with warm
compilation caches and test-result caching disabled. No competing runtime
validation ran, and the source tree remained unchanged. Host load observed
near the start was 9.50/10.05/11.67. This is acceptance evidence, not a
controlled performance comparison. Output and terminal timing are retained
in `/tmp/airc-roadmap-baseline/acceptance-9b217be-normal.out` and
`acceptance-9b217be-normal.stderr`. Full race and the other required gates
remain outstanding for this source.

Fresh full race acceptance of the same executable/test source failed with
`go test -race ./... -count=1 -timeout=900s -p=1`. The integration package
timed out in 900.574 seconds while
`TestWebSocketCLIPreauthAndMissingPongDeadlines` had run for 12 seconds.
Its stack was at `websocket_lifecycle_test.go:43`, waiting on the explicit
remaining portion of its 14-second deadline witness. This is cumulative suite
timeout, not evidence that this test hung. The three parallel HTTP tests were
still waiting in `t.Parallel`; this failed run does not cover their execution.
Other packages passed. The command's terminal exit was 1, with 1013.28 seconds
wall time (165.13 user, 28.46 system). Documentation checkpoints changed no
executable source during the run. Output and terminal timing are retained in
`/tmp/airc-roadmap-baseline/acceptance-9b217be-race.out` and
`acceptance-9b217be-race.stderr`. Full race acceptance remains unsatisfied.

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

Wait policy and focused evidence: [CLI waits](cli-wait-testing.md) and
[raw collector waits](collector-wait-testing.md).
Slow-reader pressure evidence: [carrier checks](slow-reader-testing.md).

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

## Shared UDP-client example

The UDP example now also uses its own lazy immutable executable per integration
process. It shares the small build/cleanup helper with the framed example, with
separate per-example build state. Each UDP call still owns its encrypted input,
return binding and process. Its 15s execution deadline begins after lazy build.
Posting, receipt recovery, fresh default correlations and complete independently
observed native history assertions are unchanged.

Before/after `TestUDPExampleEncryptedPostReceiptAndNativeFact` commands used
normal mode, JSON output, `-count=3 -timeout=180s`, with warm compilation cache.
Both passed; samples were 3.43/0.61/0.73s before and 3.51/0.29/0.29s after.
Command wall time was 6.18s before and 7.24s after, including changed test
compilation and initial builds. These samples show avoided repeated example
builds, not overall command speedup. Pre-run loads were 11.17/11.40/13.22 and
12.23/11.66/13.26. Subsequent race acceptance exercised this UDP example plus
TCP/TLS framed shared operations and the Unix framing boundary; all passed,
as did integration vet, formatting and diff checks.

## UI bulk history seeding

`TestUIThreadAndSearchPagesSurviveReconnect` now seeds its 520-message cursor
retention eviction through one owned native ephemeral session. Each send waits
for native acceptance, and the session closes before the next UI query. CLI
root submission, UI commands, actual server restart, exact thread/search counts,
deduplication and visible retention-gap assertions remain intact.

Normal before/after JSON commands selected this test with `-count=3 -timeout=180s`.
Both passed. Samples were 6.39/6.53/6.49s before and 5.46/5.37/5.40s after;
command wall times were 20.20s and 18.00s. Pre-run loads were 11.61/11.61/13.11
and 11.50/11.59/13.04. These are fixture measurements with a warm compilation
cache, not full-suite estimates. The same test subsequently passed race mode
with `-count=3`, as did affected-package vet, formatting and diff checks.

## Cursor-recovery bulk seeding

The retained-inbox and expired-cursor recovery tests seed their 25 direct
messages and 15 eviction posts through a bounded owned native session, with
each acceptance confirmed before returning. Real CLI reads, initial cursor
creation, gap reporting, continuation, disabled-history failure and combined
send/check behavior remain unchanged.

Normal before/after commands selected the first-inbox and expired-cursor tests
with JSON output, `-count=5 -timeout=180s`. Both passed. First-inbox samples
were 0.05/0.06/0.05/0.04/0.04s before and five 0.01s samples after;
expired-cursor samples were 0.03/0.04/0.04/0.02/0.03s before and five 0.01s
samples after. JSON timings have coarse resolution at these small durations.
Command wall times were 1.06s before and 1.93s after, including changed test
compilation. Avoided seed connections do not establish command-level speedup.
Pre-run loads were 11.30/11.62/12.93 and 12.25/11.85/12.98, with warm compilation
cache. All four recovery-file tests subsequently passed race mode `-count=5`,
as did package vet, formatting and diff checks.

## Output-budget bulk seeding

The message-count and byte-budget fixtures now use confirmed native bulk
seeding. Alternating room order and complete message bodies are unchanged;
real CLI output limits, unread cursor progression, deduplication and output
failure assertions remain. The output-failure case retains its separate CLI
send.

Normal selected checks used JSON output, `-count=5 -timeout=180s`. Both before
and after passed. Count-budget samples were 0.02/0.02/0.01/0.02/0.01s before
and five 0.01s samples after. Byte-budget samples were five 0.01s values before
and 0.01/0.00/0.00/0.00/0.00s after; zero denotes rounded JSON elapsed time,
not zero work. These small observations cannot establish a robust speedup.
Command wall time was 0.86s before and 1.79s after, including test recompilation;
pre-run loads were 11.65/11.77/12.91 and 11.64/11.76/12.87. All three budget-file
tests passed race mode `-count=5`; package vet, formatting and diff checks passed.

## Owned native STATUS reader

Observer-count fixtures now explicitly own one non-observing native STATUS
client and pass it to count/wait helpers. The connection never joins or observes
a room. It permits two minutes of idle reading to survive existing quiet lease
proofs, while each STATUS response retains the five-second bound. Production
deadlines, observer thresholds and polling cadence are unchanged.

A temporary, removed real-fixture probe measured 100 STATUS snapshots per
fixture, five repetitions, with fixture startup excluded. The reused-reader
measurement includes its initial dial. Baseline samples were
17.940/17.476/16.616/23.160/18.354ms; reused-reader samples were
4.275/3.707/3.747/3.650/3.723ms. Both commands passed normal mode with
`-count=5 -timeout=180s`. These are small local protocol measurements, not
full-suite speed estimates. Raw JSON and pre-run load samples remain in
`/tmp/airc-roadmap-baseline/status-{before,after}*`.

The new real capacity witness performs 100 queries with one reader and an
independent probe, verifies exactly two native connections and zero observers,
then verifies the reader's close returns capacity to one connection. It passed
five repetitions. All 47 direct caller tests and the capacity witness passed
normal and race runs on unchanged Go source (48 top-level tests per command;
167.924s normal and 231.893s race package time). The indirect
`TestCoAPTCPAdaptiveObserveCancelDuringBlock2` caller's cancellation modes then
passed separately in both modes. Integration vet, formatting and diff checks
passed. Before/after probe loads were 9.40/11.22/12.56 and 9.68/10.78/12.25;
baseline source was `271104c`, with the candidate recorded in this checkpoint.
The complete repository gate remains outstanding.

## Receipt-only activity recovery wait

Only retry-policy activity recovery waits through the native two-second throttle
before starting delivery. Receipt-only cases keep their terminal no-next-action,
one-attempt, uncertain-acceptance and no-invented-activity assertions. Their
150ms negative signal check remains supplementary; it is not an independent
post-throttle no-retry proof. Retry still requires the actual second native
broadcast to match its acknowledgement and extend the original expiry.

Before/after normal commands selected `TestActivityMutationRecoveryAndReceiptOnly`
with JSON output, `-count=3 -timeout=180s`. Both passed. Typing receipt-only
samples fell from 2.30/2.31/2.31s to 0.21/0.21/0.21s; thinking receipt-only
fell from 2.31/2.30/2.31s to 0.20/0.20/0.20s. Retry samples retained the real
wait: typing before 2.76/2.18/2.19s, after 2.78/2.17/2.17s; thinking before
2.18/2.19/2.18s, after 2.18/2.20/2.19s. Parent samples were
9.54/8.97/8.98s before and 5.37/4.78/4.78s after. Command wall times were
29.81s and 17.75s, with warm compilation cache and pre-run loads
9.12/10.97/11.79 and 8.63/10.58/11.61. Race mode `-count=3`, integration vet,
formatting and diff checks subsequently passed. Production throttle and TTL
behavior are unchanged.

## Receipt-only reply-signal recovery wait

Reply-signal recovery also waits through the throttle only for retry policy.
Receipt-only retains terminal no-next-action, one-attempt uncertainty and
no-invented-ACK assertions; its short negative signal check is supplementary.
Actual retry set/cancel broadcasts, refreshed acknowledgement expiry and the
independent native WAITING state query remain unchanged. IPC replay/expiry
waits are a separate roadmap item.

Normal before/after JSON commands selected `TestReplySignalRecoveryAndReceiptOnly`
with `-count=3 -timeout=180s`. Both passed. Set receipt-only samples were
2.31/2.31/2.32s before and 0.20/0.23/0.21s after; cancel receipt-only samples
were 2.32/2.31/2.32s before and 0.21/0.21/0.20s after. Set retry samples were
3.17/2.19/2.20s before and 3.06/2.17/2.17s after; cancel retry samples were
2.20/2.20/2.20s before and 2.18/2.17/2.17s after. Parent samples were
10.00/9.01/9.04s before and 5.65/4.78/4.75s after. Command wall times were
30.80s and 19.62s, with warm compilation cache and pre-run loads
14.42/11.97/12.03 and 12.47/11.89/12.00. Race mode `-count=3`, integration vet,
formatting and diff checks passed. Native throttle/TTL policy is unchanged.

## Reply-signal IPC expiry and replay waits

Unix/stdio reply-signal acceptance retains the real one-second preparation TTL.
After stopping the stdio child, the test waits only for the acknowledged
remaining expiry (bounded to two seconds) and independently queries native
WAITING to prove expiry. Later fixed throttle waits are removed: the pinned
native cancel deletes both preparation and throttle state and always emits a
broadcast. Saved set/cancel acknowledgements and attempt counts remain exact;
an added WAITING comparison proves cancel replay preserves the independent
native preparation's ID, action, note and expiry.

Before/after normal JSON commands selected `TestReplySignalOverUnixAndStdio`
with `-count=3 -timeout=180s`. Both passed. Samples were 10.71/7.53/7.51s before
and 4.23/2.21/2.25s after; first samples include lazy fixture builds. Command
wall times were 27.46s and 11.46s, with warm compilation cache and pre-run loads
11.26/11.64/11.89 and 10.09/11.27/11.74. The same real Unix-stream/anonymous-pipe
check passed race mode `-count=3`; integration vet, formatting and diff checks
passed. Production TTL, throttle and replay behavior are unchanged.

## Poll recovery scheduling wait

Poll crash recovery starts its runner immediately and relies on the existing
journal due-time checks and bounded completion wait. The arbitrary 1.1s pre-run
sleep is removed; persisted scheduling and backoff are unchanged. Retry still
requires a second native broadcast, distinct retained poll IDs and matching
question; receipt-only retains one-attempt uncertainty and no invented ACK.

Normal before/after JSON commands selected
`TestPollMutationCreationRecoveryAndReceiptOnly` with `-count=3 -timeout=180s`.
Both passed. Retry samples were 1.85/1.17/1.18s before and 0.63/0.07/0.07s after;
receipt-only samples were 1.30/1.30/1.31s before and 0.20/0.20/0.20s after.
Parent samples were 3.15/2.48/2.48s before and 0.83/0.27/0.27s after. Command
wall times were 9.44s and 4.01s, with warm compilation cache and pre-run loads
9.10/10.81/11.52 and 8.21/10.39/11.34. Race mode `-count=3`, integration vet,
formatting and diff checks passed. This measures a fixture, not the full suite.

## Message recovery scheduling wait

Action, correction and retraction recovery now also starts delivery immediately
under the existing journal due-time boundary. Removing the 1.1s pre-run wait
does not change persisted scheduling or backoff. Actual retry broadcasts,
distinct message IDs and exact bodies remain required; receipt-only keeps
uncertainty, one attempt and no invented receipt.

Normal before/after JSON commands selected
`TestMessageMutationRecoveryAndReceiptOnly` with `-count=3 -timeout=180s`.
Both passed; policy/operation samples were:

| Case | Before | After |
|---|---|---|
| Action retry | 1.62 / 1.21 / 1.18s | 0.72 / 0.07 / 0.07s |
| Action receipt-only | 1.32 / 1.32 / 1.31s | 0.20 / 0.20 / 0.20s |
| Correction retry | 1.19 / 1.20 / 1.18s | 0.08 / 0.08 / 0.07s |
| Correction receipt-only | 1.32 / 1.31 / 1.30s | 0.20 / 0.20 / 0.19s |
| Retraction retry | 1.19 / 1.18 / 1.18s | 0.07 / 0.07 / 0.07s |
| Retraction receipt-only | 1.33 / 1.30 / 1.31s | 0.19 / 0.20 / 0.20s |

Parent samples were 7.97/7.51/7.46s before and 1.46/0.82/0.81s after. Command
wall times were 24.03s and 6.41s, with warm compilation cache and pre-run loads
7.55/9.84/11.06 and 10.64/10.19/11.11. Race mode `-count=3`, integration vet,
formatting and diff checks passed. These are fixture timings, not a full-suite
measurement.

### Pinned native fixture startup signal

At `8dfdae5`, the checksum-pinned daemon already emits JSON `server_started`
after installing its listener (`96f16c3:internal/server/server.go`). The fixture
now drains that existing signal into its private log, waits at most five seconds,
then performs one real Unix connection check. Process exit wakes readiness
immediately with the exit error and captured log. Cleanup still joins the owned
child and log reader, and checks that the socket no longer accepts connections.
The gateway subprocess helper already uses its existing ready record.

Comparable command: `go test -json ./integration/gateway -run
'^TestCurrentGatewayBinaryKeepsFixturesIsolated$' -count=3 -timeout=120s`.
Before parent-test seconds: 3.86, 0.56, 0.57; after: 3.16, 0.54, 0.52.
Package seconds: 5.361 versus 4.713; command wall seconds: 6.47 versus 7.67.
The first sample includes lazy executable builds. Host load was not captured;
these small differences establish no reliable total-command speed improvement.
Raw normal JSON/stderr and race/vet results are retained under
`/tmp/airc-roadmap-baseline/native-ready-*`. Three normal and three race runs
preserve independent daemon/account/journal ownership and real HTTP acceptance.

### Acknowledged AGENTS membership changes

After `5911ecb`, AGENTS presence following completed registration or JOIN and
absence following native OBSERVE numeric 765 use one independent native snapshot.
Pinned `commands.go` mutates membership/observer state before broadcasting JOIN
or issuing 765. Disconnect absence still uses bounded polling because local
client close does not acknowledge native removal. Existing exact channels,
hidden-agent exclusion, framed/WebSocket/HTTP snapshots, and disconnect checks
remain. Operator promotion already has ordered MODE query acknowledgements.

Command: `go test -json ./integration/gateway -run
'^TestAgentsRead(HTTPBearerSignedEncryptedAndMembership|FramedAndWebSocketMembership)$'
-count=3 -timeout=120s`. HTTP parent seconds before: 0.73, 0.11, 0.11;
after: 1.11, 0.13, 0.11. Framed/WebSocket parent seconds before: 2.35, 0.07,
0.07; after: 2.83, 0.11, 0.07. Package seconds: 3.867 versus 4.868; command
wall seconds: 5.85 versus 8.01. First samples include lazy builds, and host load
was not captured. There is no measured speed improvement; the change removes
redundant retry paths after causal fences without claiming faster startup.
Normal and race count-three checks and package vet pass. Raw evidence is in
`/tmp/airc-roadmap-baseline/membership-*`.

Stream terminal validity evidence continues in [stream-terminal-testing.md](stream-terminal-testing.md).

## Append-log integration checkpoint and permission fixtures

On Darwin arm64 with Go 1.27.1, source
`59a710b6c147d6b74616b384cc8c4ef12ea1a2ef` passed fresh full normal acceptance:
`go test ./... -count=1 -timeout=900s -parallel=3 -p=2`. The integration
package completed in 582.932 seconds; every package passed. Full command build,
repository vet/staticcheck, formatting and diff checks also passed. Test-result
caching was disabled; compilation caches were warm and owned fixture binaries
were rebuilt/shared only inside each test process. Initial host load averages
were 14.74/15.77/19.70. This is checkpoint evidence, not final acceptance of
future executable changes.

The same source's full race command with a 900-second package budget did not
complete: gateway integration timed out at 900.561 seconds during Unix-datagram
lease checks, leaving later transports untested. The command exited 1. All six
native append-log controls had passed earlier in that run. The verified orphan
gateway child left by the package timeout was stopped with SIGINT and joined.
Raw JSON and stderr are retained at
`/tmp/airc-gateway-race-59a710b.jsonl` and
`/tmp/airc-gateway-race-59a710b.stderr`. A longer complete run is required to
measure the current full race workload and select an adequate package budget;
this timeout is not a passing race gate.

That invocation also applied `umask 077` while creating private evidence files.
The inherited mask changed deliberately public mode-0644 fixture creation into
mode 0600, so the append-log unsafe temporary and file-mailbox public input
controls tested the wrong inputs and failed. Both fixtures now explicitly chmod
the created file to 0644. Their race controls passed with `-count=3` under both
022 and 077 masks (1.650 and 1.552 seconds). Create evidence files with explicit
private file modes without changing the test process's inherited umask.


The complete rerun at executable/test source
`4517d497f581bd8868b508f6a0b9f0fbf51bd0c4` passed with terminal exit 0:
`go test -race ./... -count=1 -timeout=1800s -p=1 -json`.
Gateway integration completed in 1,102.882 seconds; the full command recorded
32 package outcomes and 2,273 passing test/subtest cases without failures.
The run used the same Darwin arm64 Go 1.27.1 toolchain, warm compilation caches,
fresh test results and per-process rebuilt immutable fixture binaries. Evidence
files were created explicitly mode 0600 while retaining the inherited 022 umask.
Raw evidence is `/tmp/airc-gateway-race-4517d49.jsonl` and
`/tmp/airc-gateway-race-4517d49.stderr`.

The required full race package budget is now 1,800 seconds, allowing headroom
above the measured 1,102.882-second integration workload. This changes only the
full command's package watchdog; race instrumentation, individual fixture/test
deadlines, production deadlines and durability assertions remain unchanged.
There is no speed-improvement claim. Optional browser and native OSCORE probes
were skipped without their external fixtures; their independent recorded
qualification is not replaced by this full run. Final acceptance remains due
after remaining executable changes.


## Parent-owned FD integration checkpoint

At executable/test source `a15ec20b56e4ebc998431ac48f1b5690b899b1d3`, full
normal acceptance passed with terminal exit 0:
`go test ./... -count=1 -timeout=900s -parallel=3 -p=2 -json`.
The JSON package result records gateway integration at 595.587 seconds; the
whole command took 601.932 seconds, with 32 package outcomes and 2,344 passing
test/subtest cases without failures. Darwin arm64 used Go 1.27.1, warm
compilation caches, fresh test results and per-process rebuilt immutable
fixtures; initial load averages were 10.855/12.118/14.072.

The tracked Go/module source digest was
`bb9ec87b6208e16f94b35575ff9260157badf3547cff61f4e1ad35dd530912ca`.
The acceptance wrapper checks that digest before every phase, permitting only
documentation changes during this run. Private raw evidence is
`/tmp/airc-gateway-parent-fd-a15ec20-normal.stdout`, its `.stderr` counterpart,
and `/tmp/airc-gateway-parent-fd-a15ec20-metadata.json`. Evidence files use
explicit mode 0600 without changing the inherited test umask.

The same wrapper completed full race acceptance with terminal exit 0:
`go test -race ./... -count=1 -timeout=1800s -p=1 -json`.
Gateway integration took 1,153.865 seconds; the whole command took 1,292.589
seconds. Its 32 package outcomes comprise 24 passes and eight skips, with
2,344 passing test/subtest cases and no failures. Raw race evidence is
`/tmp/airc-gateway-parent-fd-a15ec20-race.stdout` and its `.stderr` counterpart.
Opt-in browser and native OSCORE fixture checks remain skipped where their
external fixtures were absent; this run does not qualify those boundaries.

Subsequent `go build ./cmd/...`, `gofmt -l .` (empty output), `go vet ./...`,
`go run honnef.co/go/tools/cmd/staticcheck@latest ./...` and `git diff --check`
each completed with terminal exit 0. Their private stdout/stderr files use the
same prefix and phase names; metadata records each phase's exit and duration.
The wrapper itself reported terminal exit 0. These results verify the
parent-FD checkpoint, not final acceptance after future executable changes.


## FIFO integration checkpoint

At executable/test source `8f1f9c30f95885bc2db9cd92a2648d49af483e1f`, the
unchanged-source wrapper completed with terminal exit 0. Darwin arm64 used
Go 1.27.1; initial load averages were 10.975/10.222/10.187.

- `go test ./... -count=1 -timeout=900s -parallel=3 -p=2 -json` exited 0
  in 619.237 seconds; gateway integration took 612.670 seconds.
- `go test -race ./... -count=1 -timeout=1800s -p=1 -json` exited 0
  in 1,326.133 seconds; gateway integration took 1,182.549 seconds.
- Each run recorded 2,399 passing test/subtest cases, no failures, and
  32 package outcomes comprising 24 passes and eight skips.
- Build, formatting (empty `gofmt -l .` output), vet, staticcheck and
  `git diff --check` subsequently each exited 0.

The SHA256 over sorted tracked Go/module paths and content, each NUL-separated,
was `23b4b60136eec6f1426d4bba3fde15efa609eabb0185df0392d46e87ed9ca445`.
The wrapper checked it before every phase and at completion; repository
executable/test/module source remained unchanged throughout. Private evidence
files use explicit mode 0600 without changing the inherited test umask:
`/tmp/airc-gateway-fifo-8f1f9c3-metadata.json`, the `normal` and `race`
`.stdout`/`.stderr` files, and subsequent phase files under that same prefix.

Opt-in browser and native OSCORE checks were skipped when their external
fixtures were absent. This is the FIFO integration checkpoint on Darwin,
not final acceptance after future executable changes or Linux native
qualification. Remaining acceptance stays in the roadmap.


## Cross-compiled native fixtures

Integration tests can use explicitly supplied Go executables on a host without
Go or Git. Set both `AGW_GATEWAY_BINARY` and `AGW_GATEWAY_SHA256` for the
current gateway, and both `AGW_NATIVE_BINARY` and `AGW_NATIVE_SHA256` for the
pinned native daemon. The real framed example uses `AGW_FRAMED_CLIENT_BINARY`
and `AGW_FRAMED_CLIENT_SHA256` with the same verification and private-copy
rules. Each path must be absolute and each digest must be a
complete SHA256. Partial or invalid selection fails; it never silently uses
a source build. With neither variable set for a fixture, its existing source
build and archive checks remain unchanged.

Before execution, each supplied binary is copied into an owned private root,
its digest checked, its Go program path and runtime OS/architecture checked,
and its mode set to 0500. Race-enabled tests require a race-enabled supplied
gateway. Copies are bounded at 128 MiB and owned roots are removed on failure
and at test-process exit. The fixture never executes the supplied path directly.

Digests establish artifact identity, not source provenance. The platform run
must separately record the current gateway source and verify/export the pinned
native baseline `96f16c3bbb8a3faac2e69684f591af63ceb58450`, whose Git tar
SHA256 is `4f25ee66941bd79c29f936b8deabc120dc0451e5d1b747afee4bbdf593b32829`,
before building the native daemon. Supplied-daemon logs explicitly label the
required baseline/archive and supplied digest; they do not claim that the
container itself verified the source archive.

Host cross-compilation uses `CGO_ENABLED=0 GOOS=linux GOARCH=arm64` with
`go build -p=1 -trimpath` for the gateway and pinned `aircd`, and
`go test -c -p=1 ./integration/gateway` for the test executable. This permits
real Linux native fixtures in the existing Ubuntu image without downloading
or installing a toolchain. It provides no Linux race instrumentation.
Use a disposable local-filesystem volume for `/tmp`: tmpfs is deliberately
rejected by the production journal filesystem policy. Containers retain a
read-only root, read-only artifact mount, no network and automatic removal
of the container and anonymous volume.

The fixture-copy/refusal tests passed under Darwin race detection (1.942s),
and the default source-built Darwin FIFO native acceptance still passed
under race detection (7.319s). Actual Linux supplied-fixture acceptance is
recorded in the FIFO specification.


## Linux portability integration checkpoint

At executable/test source `226bc94aad8b99d254fd6f0a6f2ee5b2a5aecabb`, the
unchanged-source wrapper completed with terminal exit 0. This covers the Linux
unnamed-socket fix, supplied native/example fixture support and distinct-inode
Unix-datagram test correction. Darwin ARM64 used Go 1.27.1; initial load
averages were 5.744/8.029/9.232.

- `go test ./... -count=1 -timeout=900s -parallel=3 -p=2 -json` exited 0
  in 614.487 seconds; gateway integration took 608.164 seconds.
- `go test -race ./... -count=1 -timeout=1800s -p=1 -json` exited 0
  in 1,317.327 seconds; gateway integration took 1,175.698 seconds.
- Each run recorded 2,406 passing test/subtest cases, no failures, and
  32 package outcomes comprising 24 passes and eight skips.
- Build, formatting (empty `gofmt -l .` output), vet, staticcheck and
  `git diff --check` subsequently each exited 0.

The SHA256 over sorted tracked Go/module paths and content, each NUL-separated,
was `06204a771ff269116132ed58a0337654586c7b84dfe4af1092e654bb28aa9f2c`.
The wrapper checked it before every phase and at completion; an independent
post-run digest check also matched. Repository executable/test/module source
remained unchanged, and the worktree was clean. Private evidence files use
mode 0600: `/tmp/airc-gateway-unixgram-226bc94-metadata.json`, the `normal`
and `race` `.stdout`/`.stderr` files, and subsequent phase files under the same
prefix. The wrapper's final terminal exit was 0.

Opt-in browser and native OSCORE checks were skipped where their external
fixtures were absent. Separate Linux native qualification remains in each
mode's specification; this full run establishes the Darwin integration
checkpoint and does not extend those platform claims. Final acceptance after
future executable changes remains in the roadmap.


## Live mailbox journal assertion

The offline-importer integration checkpoint at executable/test source
`3a685ee347c9e2f4835252b6294bc661c044904b` finished with terminal exit 1.
Full normal acceptance ran for 615.467 seconds and recorded 2,422 passing
test/subtest cases, 53 skipped cases and one failure:
`TestDirectoryMailboxNativeResultQuotaBackpressure`. Its read-only
`noMailboxJob` query returned `database is locked (261)` before it could
establish a job count. The package outcomes were 23 passes, eight skips and
one failure. Race and subsequent wrapper phases were not reached.

The unchanged-source digest was
`1ad4e06025514dd74176e0d1d47442d36d3f9ab8f36edf101430b934c4078018`.
Private evidence is `/tmp/airc-gateway-offline-3a685ee-metadata.json` and the
`normal.stdout`/`normal.stderr` files under that prefix. This failed run does
not replace the preceding successful full checkpoint.

[SQLite identifies error 261 as WAL recovery contention](https://www.sqlite.org/rescode.html#busy_recovery).
The separate live-journal inspection connection had no busy timeout. The
helper now retains `mode=ro`, sets a connection-local three-second busy
timeout and bounds its query with a four-second context. Every final query
error and every nonzero job count still fails. Production journal settings,
mailbox admission, delivery deadlines and durability are unchanged.

The corrected quota control passed ten consecutive normal runs (55.217s
package total) and ten consecutive race runs (72.774s package total), each
with terminal exit 0. Seven owner/authentication/size/inbox-quota controls
sharing the helper passed under race detection in 27.642s, terminal exit 0.
The two race selections ran concurrently with independent owned fixtures;
these are correctness checks, not a performance comparison. Integration vet
and staticcheck passed, and formatting/diff checks were clean.

Corrected Go/module source digest:
`c70058795b17c2bc7f3d616ca9e0b761c05c5a38248c5eeb4f43adc25f3df17f`.
Private raw evidence is `/tmp/airc-mailbox-read-contention-normal.jsonl`,
`/tmp/airc-mailbox-read-contention-race.jsonl` and
`/tmp/airc-mailbox-read-contention-boundaries.jsonl`. Fresh full acceptance
on the corrected source remains required.


## Offline importer integration checkpoint

At executable/test source `13c5db70bf5d8fb32e5379fc55bd6591d6f813c1`, the
unchanged-source wrapper completed with terminal exit 0. This covers the signed
offline importer and the bounded read-only mailbox journal assertion correction.
Darwin ARM64 used Go 1.27.1; initial load averages were 16.627/14.864/14.580.

- Full normal acceptance exited 0 in 612.187 seconds; gateway integration
  took 606.529 seconds.
- Full race acceptance exited 0 in 1,338.931 seconds; gateway integration
  took 1,192.558 seconds.
- Each run recorded 2,423 passing test/subtest cases, 53 skipped cases, no
  failures and 32 package outcomes: 24 passes and eight skips.
- Command builds, formatting (empty output), vet, staticcheck and diff checks
  subsequently each exited 0.

The commands and budgets were the same full acceptance commands documented
above, with JSON output, normal `-parallel=3 -p=2` and race `-p=1`. SHA256 over
sorted tracked Go/module paths and content, each NUL-separated:
`c70058795b17c2bc7f3d616ca9e0b761c05c5a38248c5eeb4f43adc25f3df17f`.
The wrapper checked it before every phase and at completion; an independent
post-run digest matched. Only documentation changed during the run. The
wrapper finished at `2026-10-06T15:35:52.509555Z`.

Private evidence files are `/tmp/airc-gateway-mailbox-read-13c5db7-metadata.json`
and phase `.stdout`/`.stderr` files under the same prefix. The earlier failed
normal run remains documented above rather than being replaced by these results.
Opt-in browser and native OSCORE fixtures were absent and skipped; their
roadmap requirements remain open. Separate Linux native evidence stays in each
mode's specification. Sequenced-packet draft overlays were tested independently
and were not part of this frozen-source checkpoint. Final acceptance after
remaining executable changes is still required.

## Sequenced-packet integration checkpoint

At executable/test source `96ac8345fc36584aa6a865a756cad4c8415b67ed`, the
fresh full acceptance wrapper completed on 2026-10-06 at
`16:36:21.567932 UTC` with terminal exit 0. Normal and race runs each recorded
2,427 passing test/subtest events, 53 skips and zero failures. Each recorded
32 package results: 24 passes, eight packages with no tests and zero failures.
Optional trusted-browser/native-worker fixtures remained unprovided; their
skips do not qualify those external paths.

| Phase | Terminal exit | Whole-command elapsed seconds |
|---|---:|---:|
| Full normal, `-timeout=900s -parallel=3 -p=2` | 0 | 612.653238 |
| Full race, `-timeout=1800s -p=1` | 0 | 1347.801229 |
| Command builds | 0 | 1.470585 |
| Formatting, empty output | 0 | 0.102477 |
| Vet | 0 | 0.716254 |
| Staticcheck | 0 | 3.440892 |
| Diff check | 0 | 0.047526 |

The integration package reported 605.757 seconds normal and 1199.781 seconds
race. This is acceptance evidence, not a performance comparison. Go was
1.27.1 on Darwin ARM64; initial load averages were approximately
12.290/13.384/13.584. The runner kept Go/test/module source unchanged through
all phases and independently verified its final digest:
`92430d1ec90977d963a273e88e11965003a876c4555dc412752e54a1bcc1b4f5`
(sorted tracked paths and contents, each NUL-separated).

The source includes sequenced-packet adapter, configuration/service integration
and Linux native test definitions. Darwin cannot execute the Linux mode;
[actual Linux adapter, service and native acceptance](unix-seqpacket.md) are
separate source-specific controls without Linux race instrumentation.
The [separate POSIX queue overlay](posix-mqueue.md#separate-backend-draft-controls)
was not applied to this source and is excluded from this checkpoint.

Private evidence prefix: `/tmp/airc-gateway-unixpacket-96ac834`, with
`-metadata.json`, `-counts.json`, and each phase's `-stdout`/`-stderr` files.
Every required phase reached terminal exit 0; no observation timeout was used
as a completion signal. Later executable changes still require the roadmap's
fresh final acceptance gate.
