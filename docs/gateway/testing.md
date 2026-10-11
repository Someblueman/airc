# Gateway test selection and acceptance

Run commands from the repository root. Tests use private state, scratch listeners
and owned child processes; they must not use the live daemon or default service
port. A sandbox that denies scratch socket binds cannot validate these fixtures.

On Linux, journal fixtures require ext4, XFS or Btrfs, just like the service.
If `/tmp` is tmpfs, set `TMPDIR` to a short, owner-only directory on a supported
filesystem before running the suite, for example a directory created with
`mktemp -d /var/tmp/agw.XXXXXX`. Its ancestors must not be group-writable except
for root-owned sticky directories. The socket fixtures honor this Linux setting;
Darwin keeps short `/tmp` paths because its default temporary paths can exceed
Unix socket limits. The scratch example also honors `TMPDIR`.

IPv6 multicast fixtures default to the loopback interface and `::1`, as tested on
Darwin. Linux loopback may not deliver multicast even with an explicit route.
For Linux qualification, use a private network namespace with loopback up and
a multicast-capable veth pair, then set `AGW_IPV6_MULTICAST_INTERFACE` and
`AGW_IPV6_MULTICAST_SOURCE` to one veth interface and its IPv6 unicast address.
Other transport fixtures continue using loopback. Warm build caches before
running the suite in the namespace, which has no external network access.

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
go test ./... -count=1 -timeout=1200s -parallel=3 -p=2
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
The full normal suite has a twenty-minute aggregate budget; an earlier local
integration run consumed 893 of its former 900 seconds. This leaves room for
the complete Linux fixtures without changing any operation or transport deadline.
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

## Dated checkpoints

The sections below are dated records. Those written before the
[identity removal](removed-identity.md) describe principals, signed and
encrypted envelopes and return routes, which no longer exist.

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
The [separate POSIX queue overlay](posix-mqueue.md#integrated-backend-controls)
was not applied to this source and is excluded from this checkpoint.

Private evidence prefix: `/tmp/airc-gateway-unixpacket-96ac834`, with
`-metadata.json`, `-counts.json`, and each phase's `-stdout`/`-stderr` files.
Every required phase reached terminal exit 0; no observation timeout was used
as a completion signal. Later executable changes still require the roadmap's
fresh final acceptance gate.

## POSIX ownership integration checkpoint

The full acceptance wrapper finished on 2026-10-06 at
`17:32:19.778786 UTC` with terminal exit 0. The frozen Go/test/module digest was
`fbbf3871c611124ff2cb42d0f577629f0cbece6c1bf28044b3e56cf137daf09d`
(sorted tracked paths and content, NUL-separated). Its base commit was
`a5a52d6`; the reviewed schema-12 ownership changes were staged during the gate.
Independent digest checks before/after the run matched. Normal and race each
recorded 2,436 passing tests/subtests, 53 skips and zero failures; each recorded
24 passing packages and eight packages without tests. Optional external/browser/
native-worker fixture skips do not qualify their outstanding roadmap paths.

| Phase | Terminal exit | Whole-command elapsed seconds |
|---|---:|---:|
| Full normal, `-timeout=900s -parallel=3 -p=2` | 0 | 618.785737 |
| Full race, `-timeout=1800s -p=1` | 0 | 1346.526186 |
| Command builds | 0 | 1.443632 |
| Formatting, empty output | 0 | 0.331399 |
| Vet | 0 | 1.703653 |
| Staticcheck | 0 | 2.792215 |
| Diff check | 0 | 0.028975 |

The integration package took 610.676 seconds normal and 1192.204 seconds race.
Go was 1.27.1 on Darwin ARM64; initial load averages were approximately
11.593/10.836/11.306. These are acceptance timings, not a performance comparison.
Evidence prefix: `/tmp/airc-gateway-posix-ownership-a5a52d6`, containing the
metadata, phase stdout/stderr, and independently parsed normal/race counts.

This source includes the POSIX queue backend, packet bridge, verified recovery
API and schema-12 journal ownership; configuration/service/native mode remain
unimplemented. Actual Linux controls are recorded in
[posix-mqueue.md](posix-mqueue.md), without Linux race instrumentation.
After the full gate finished, three poll acknowledgement fixture timestamps
were made explicitly UTC to fix independently reproduced pre-existing Linux
comparison failures. The resulting source digest is
`f09b491f3a6e8bd6a1cea2f8eb1d2ec54f939370bd66d37f43fd1602b50b2852`.
Those three test-only changes are excluded from the frozen full gate; their
integrated affected checks are recorded in the queue specification. A fresh
final gate after remaining implementation is still required by the roadmap.

## POSIX service integration checkpoint

Full normal/race acceptance and all required gates passed on frozen Go/test/
module source SHA256
`a56eede156dc5d1950a7861e2b89bf590f73113bb56aa3ee5448174c4d087e18`,
from base `ffb5fc973f7ad1a019cddfb2f896c4915e8298e1` plus reviewed service wiring.
The independent final source digest matched every phase's frozen digest.

| Gate | Exit | Elapsed seconds |
| --- | --- | --- |
| Full normal | 0 | 622.481239 |
| Full race | 0 | 1364.305196 |
| Command builds | 0 | 1.866145 |
| Formatting, empty output | 0 | 0.091390 |
| Vet | 0 | 0.690101 |
| Staticcheck | 0 | 1.908947 |
| Diff check | 0 | 0.008279 |

Both normal and race runs reported 2,452 test/subtest passes, 53 skips and zero
failures; 32 packages, 24 passing and eight without tests. Gateway integration
took 614.340 seconds normal and 1205.594 seconds race. The race run used the
calibrated 1,800-second package budget. Go was 1.27.1 on Darwin ARM64; initial
load averages were 14.2012/15.3379/14.2603. Execution started
2026-10-06T17:56:45.048946Z and finished 2026-10-06T18:29:56.808273Z.

This source includes enabled Linux POSIX queue configuration, CLI recognition,
service ownership and protected fixed-response packet dispatch. Actual Linux
startup and configuration controls are recorded in
[posix-mqueue.md](posix-mqueue.md#enabled-service-wiring), without Linux race
instrumentation. The eight independently reviewed native Linux qualification
test files remained in an isolated draft during this gate and are excluded.
The 53 opt-in/platform skips do not qualify any outstanding browser, worker or
physical path. Later implementation still requires the roadmap's final gate.

Private evidence prefix: `/tmp/airc-gateway-posix-service-ffb5fc9`, with immutable
phase output files, metadata and independent normal/race count summaries.
Terminal exit 0 was verified directly; no observation timeout was treated as
completion.

## BPv7 integration checkpoint

Full required acceptance passed at unchanged Go/test/module source
`f592b5875ff57a5c6a2c2192170fcd2b48fc654ff77b76ceb3e7d557b094a814`,
based on `73aa587` plus the reviewed BPv7 ingestion and qualification changes.
The digest uses sorted tracked Go/module paths and bytes, each NUL-separated;
it was verified before each phase and again independently after terminal exit.

| Gate | Terminal exit | Wall time |
|---|---:|---:|
| Full normal tests | 0 | 617.684s |
| Full race tests | 0 | 1,360.546s |
| Command builds | 0 | 2.106s |
| Formatting (empty output) | 0 | 0.441s |
| Vet | 0 | 0.799s |
| Staticcheck | 0 | 3.038s |
| Diff check | 0 | 0.022s |

Each test run had 2,518 test/subtest passes, 53 skips and zero failures;
33 packages produced terminal events (25 passed, eight had no tests).
Gateway integration took 611.948s normal and 1,209.707s race. Commands were the
standard acceptance commands above, with the existing calibrated 1,800-second
race package budget. Go was 1.27.1 on Darwin ARM64. Initial load averages were
13.5396/19.7163/19.3560. The gate ran from 2026-10-06T19:03:44.837785Z to
2026-10-06T19:36:50.230661Z.

The source includes the BPv7 decoder, provisioned file import, shared offline
admission, independent native BPv7 controls and eight earlier Linux native
POSIX queue test files. Linux native ingestion has separate digest-verified
runtime evidence in [the BPv7 specification](bundle-protocol.md); Darwin full
race execution does not supply Linux race evidence. The 53 opt-in/platform
skips do not qualify pending browser, worker or physical-path work. The final
roadmap gate remains for subsequent executable changes.

Private evidence prefix: `/tmp/airc-gateway-bpv7-v2-73aa587`, with phase output,
metadata and independent count summaries. The earlier v1 run was deliberately
stopped for a principal-test fixture correction and has terminal exit 1; it is
preserved as interrupted evidence, not counted as successful acceptance.


## MQTT integration checkpoint

Full acceptance passed on the unchanged Go/test/module source:
`aedc753b8b5b95a86858536cd9082de2b31d72dca40e89b1b475395565c5f323`.
The gate started from `816acfd420732e489a3cfa092fa90f0f8deed550` plus MQTT
service activation and native test changes. The sorted tracked Go/module paths
and contents were each NUL-separated for the digest, verified before each phase
and independently after terminal completion.

| Phase | Terminal exit | Elapsed |
|---|---:|---:|
| Full normal tests | 0 | 625.384s |
| Full race tests | 0 | 1,395.204s |
| Command builds | 0 | 2.391s |
| Formatting (empty output) | 0 | 0.414s |
| Vet | 0 | 1.057s |
| Staticcheck | 0 | 3.882s |
| Diff check | 0 | 0.026s |

Each test phase recorded 2,599 test/subtest passes, 53 skips and zero failures;
34 packages produced terminal events (26 passed, eight had no tests). Gateway
integration took 618.752s normal and 1,234.857s race. Commands were the standard
acceptance commands above, with the existing 1,800-second race package budget.
Go was 1.27.1 on Darwin ARM64. Initial load averages were
20.2100/19.5864/18.6265. The gate ran from 2026-10-06T20:06:20.179840Z to
2026-10-06T20:40:09.372287Z and finished with terminal exit 0.

The source includes the MQTT packet codec, TLS/configuration adapter, service
activation and independent CLI/native MQTT controls. Digest-verified Linux
ARM64 runtime qualification separately passed 20 cases with no skips/failures,
without Linux race instrumentation; see [MQTT evidence and exact scope](mqtt.md).
The 53 opt-in/platform skips do not qualify remaining browser, worker, physical
or remote paths. The final roadmap gate remains for subsequent executable
changes and completion of the overall roadmap.

Private evidence prefix: `/tmp/airc-gateway-mqtt-v1-816acfd`, with owner-only
phase output, metadata and independent normal/race count summaries. Linux
log/provenance prefix: `/tmp/airc-linux-native-159afc4/mqtt-ingress-v1`.


## MQTT-SN integration checkpoint

Full acceptance passed on unchanged Go/test/module source:
`45de8b5a6984a1ffee603b8a0dcd06d5ff47eb9e2ce247d354a56baf919111fc`.
The gate started from `4ef42e24d6f65d9595983136afeac599de2989b8` plus MQTT-SN
service activation and native tests. Sorted tracked Go/module paths and contents
were each NUL-separated for the digest, checked before every phase and after
terminal completion.

| Phase | Terminal exit | Elapsed |
|---|---:|---:|
| Full normal tests | 0 | 630.075s |
| Full race tests | 0 | 1,397.911s |
| Command builds | 0 | 2.210s |
| Formatting (empty output) | 0 | 0.456s |
| Vet | 0 | 0.990s |
| Staticcheck | 0 | 3.751s |
| Diff check | 0 | 0.021s |

Each test phase recorded 2,655 test/subtest passes, 53 skips and zero failures;
35 packages produced terminal events (27 passed, eight had no tests). Gateway
integration took 622.364s normal and 1,233.421s race. Commands were the standard
acceptance commands above, preserving the 1,800-second race package budget.
Go was 1.27.1 on Darwin ARM64; initial load was 14.9453/14.6514/14.1450.
The gate ran from 2026-10-06T21:11:16.490524Z to
2026-10-06T21:45:12.546938Z and finished with terminal exit 0.

The source includes MQTT-SN connectionless encrypted signed ingestion, bounded
predefined-topic authority, service shutdown and independent CLI/native tests.
Digest-verified Linux ARM64 runtime qualification separately passed 18 cases
without skips/failures or Linux race instrumentation. See [MQTT-SN evidence and
exact scope](mqtt-sn.md). UDP silence does not prove intake; deliberate relay
loss establishes pre-intake loss, and existing receipt controls establish durable
native acceptance before shutdown. No physical-radio qualification is claimed.
The 53 opt-in/platform skips do not qualify remaining browser, worker, physical
or remote paths. Final acceptance remains required after subsequent executable
changes and completion of the overall roadmap.

Private evidence prefix: `/tmp/airc-gateway-mqtt-sn-v1-4ef42e2`, with owner-only
phase outputs and metadata. Linux log/provenance prefix:
`/tmp/airc-linux-native-159afc4/mqtt-sn-ingress-v1`.

## AMQP integration checkpoint

Required acceptance passed on unchanged Go/test/module source
`eee1a61a721a5d0c3f1d2903b3f88e10640e3009a75df07cc3f20462a96d4013`.
The source began at `892326f5118251708879e6457f7d6e8a10bfc971` plus AMQP
service activation and independent native tests. Sorted tracked Go/module paths
and content were each NUL-separated; the digest was checked before every phase
and after terminal completion. Go was 1.27.1 on Darwin ARM64.

| Phase | Terminal exit | Elapsed |
|---|---:|---:|
| Full normal tests | 0 | 635.162s |
| Full race tests, second attempt | 0 | 1,430.023s |
| Command builds | 0 | 2.490s |
| Formatting (empty output) | 0 | 0.154s |
| Vet | 0 | 1.169s |
| Staticcheck | 0 | 1.267s |
| Diff check | 0 | 0.038s |

Each successful test phase recorded 2,877 test/subtest passes, 53 skips and zero
failures. All 36 packages terminated (28 passed, eight without tests). Gateway
integration took 627.875s normal and 1,262.264s race. Commands were the standard
acceptance commands above, retaining the 1,800-second race package budget.
Normal acceptance began at 2026-10-06T23:35:43.481218Z with load
27.1289/23.8901/17.9575. The successful race/build/static wrapper ran from
2026-10-07T00:10:28.745051Z to 2026-10-07T00:34:24.473867Z, initial load
33.5601/33.0669/31.2261. These are acceptance timings, not a speed comparison.

The first full race attempt exited 1 in 1,439.599s. Its existing CoAP TCP CSM
fixture gateway exited with SQLite `disk I/O error (8714)` after the first
subcase; three later subcases and their parent failed. It recorded 2,873 passes,
53 skips and four test failure events, with one failing integration package.
Five unchanged-source focused repetitions passed in 38.582s, and the same
test's four subcases and parent passed in the full rerun (parent 6.570s).
The cause of the original WAL-index memory access fault remains unresolved;
no SQLite policy, production deadline or test claim was changed to obtain the
passing run. The rerun references the still-valid normal result and preserves
the failed race evidence separately.

Independent AMQP qualification uses a separately encoded TLS/SASL client and
native observer: exact author/target/Unicode delivery, fragmented transfers,
whole bounded history/search/context, lost acknowledgement/result reconnect
and restart replay, durable-before-disposition locking control, authority
refusals and joined shutdown. Darwin race and digest-verified Linux ARM64
native cases passed; Linux has no race instrumentation or general-broker claim.
See [AMQP scope and binary provenance](amqp.md). The 53 skips do not qualify
remaining browser, worker, physical or remote paths. Final acceptance remains
required after subsequent executable changes and the overall roadmap.

Owner-only evidence prefixes: `/tmp/airc-gateway-amqp-v1-892326f` (successful
normal and failed first race), `/tmp/airc-gateway-amqp-v2-892326f` (successful
race and remaining gates). Linux fixture binaries are under
`/tmp/amqp-native-RDphog`.

### STOMP preauthentication fixture ownership

On 2026-10-07 (Europe/London), an affected adapter race rerun failed the
standalone preauthentication heartbeat writer join after 4.03s. The helper had
returned at its unchanged three-second production deadline, but a socket read
deadline can win before the context callback closes the socket. The real
connection owner closes after authentication returns; the standalone fixture
now does the same before reporting completion, and its cleanup explicitly
joins the client writer. No production deadline or assertion was relaxed.
The focused race control passed five repetitions in 16.696s; adapter vet,
staticcheck, formatting and diff checks passed. Subscription dispatch remains
an independent unfinished checkpoint; the failed broad run is not acceptance.

## STOMP integration checkpoint

On 2026-10-07 (Europe/London), STOMP service integration completed acceptance
with the independently implemented wire client and pinned native AIRC observer.
The exact tracked Go/test/module source SHA256 was
`e98747028c61da8c683e5569625f6d67b8735f5ca5d4c09ea2ca4ec2dbdf13da`;
the digest hashes sorted tracked Go/module paths and their contents, each
NUL-separated. It matched before every phase and again after terminal completion.
The host was Darwin ARM64 with Go 1.27.1. The runner started at
2026-10-07T01:20:25.604214Z and finished at 2026-10-07T01:55:50.751182Z;
initial load averages were 22.079 / 22.867 / 21.228.

| Gate | Terminal exit | Wall time | Recorded result |
|---|---:|---:|---|
| `go test ./... -count=1 -timeout=900s -parallel=3 -p=2 -json` | 0 | 633.972s | 3,036 passes, 53 opt-in/platform skips, no failures; 29 passed packages and eight without tests |
| `go test -race ./... -count=1 -timeout=1800s -p=1 -json` | 0 | 1,482.981s | Same pass/skip/package counts, no failures |
| `go build ./cmd/...` | 0 | 2.452s | Passed |
| `gofmt -l .` | 0 | 0.347s | No output |
| `go vet ./...` | 0 | 1.432s | Passed |
| `go run honnef.co/go/tools/cmd/staticcheck@latest ./...` | 0 | 3.117s | Passed |
| `git diff --check` | 0 | 0.012s | Passed |

The integration package passed in 625.325s normally and 1,259.801s under race
detection. STOMP's independent native controls passed all 20 tests (four top-level
and 16 subtests) without skips/failures in both full runs. Corrected focused
Darwin race controls also passed in 14.594s. On the same frozen source, actual
Linux ARM64 selected-profile execution passed all 20 tests without skips/failures
in 3.160s, using the existing pinned Ubuntu image, digest-verified read-only
fixtures, disabled network and disposable disk state. Linux evidence has no race
instrumentation. Exact binary/image digests and protocol limits are in
[stomp.md](stomp.md#service-and-native-evidence).

The preliminary source digest was
`1b124deef3d98413d4741098ffa951d55afa9f730d6d8d0321fb8099aea7415c`.
Its normal gate was explicitly interrupted after 100.145s (terminal exit 1)
when subagent review found that native refusal/terminal assertions admitted read
timeouts as closure. Those assertions now require EOF/reset and reject extra
bytes; the owned stopped processes were verified gone. That preliminary run is
not acceptance evidence. Its outputs remain under
`/tmp/airc-gateway-stomp-v1-70fc039`; the final successful runner metadata and
phase outputs are under `/tmp/airc-gateway-stomp-v2-70fc039`. The earlier AMQP
checkpoint's CoAP SQLite crash remains documented and unresolved; the same CSM
controls passed in this run without any SQLite/durability-policy change.
These are integration checkpoint results, not qualification of the skipped
browser/device/platform paths or completion of the remaining roadmap.

## TLS syslog integration checkpoint

The current service/native integration uses tracked Go/test/module source SHA256
`557f0a09ca391cc3aec1b8e22ab4d70587853ad6fef35cac02919a950177c172`,
hashing sorted tracked Go/module paths and contents, each NUL-separated. New Go
files were staged before freezing the digest. Executable/test/module sources remained unchanged throughout the run. The host is Darwin ARM64 with Go 1.27.1; the
runner started at 2026-10-07T02:24:11.263967Z, with load averages
11.266 / 12.489 / 13.263. Metadata and exact phase outputs are under
`/tmp/airc-gateway-syslog-v1-44cbe61`.

The full normal command
`go test ./... -count=1 -timeout=900s -parallel=3 -p=2 -json` exited 0 in
643.334742749983s: 3,110 test pass events, 53 opt-in/platform test skips and no
failures. Of 38 packages, 30 passed and eight had no tests. The integration
package passed in 629.684s, including all 12 syslog native pass events. The
source digest matched afterward. Full race acceptance exited 0 in
1496.1346880419878s with the same 3,110 passes, 53 skips and package counts;
its integration package passed in 1270.044s. Build (2.315918500011321s),
formatting (0.34877945901826024s, empty output), vet (1.996762500028126s),
staticcheck (3.9687717500492s) and diff (0.027985916996840388s) all exited 0.
The runner finished at 2026-10-07T03:00:00.219483Z with terminal exit 0 and
verified the source digest before each phase and after completion.

On the same frozen source, selected Darwin native race controls passed all 12
test events without skips/failures in 21.022s. Actual Linux ARM64 execution
passed the same 12 events in 3.4245815409813076s, without race instrumentation.
Linux used digest-verified cross-built executables in the existing Ubuntu image,
read-only host binds, disabled networking and disposable disk-backed temporary
state, with no toolchain installation or download. Exact binary/image digests,
selected profile boundaries and native baseline are in
[syslog.md](syslog.md#service-native-integration-evidence).
Linux provenance and log are under `/tmp/syslog-native-v1-44cbe61`.

The independent client encodes octet-counted records without the production
wire package; native AIRC observation verifies the fixed author, target and
exact body. An existing authenticated HTTPS receipt API separately fences
recorded durable completion, because syslog has no application ACK. Reconnect
and service/journal restart preserve retained content-addressed record identity
and one send attempt without duplicate native posts. Refusal controls leave no
journal job or native post; shared admission and joined shutdown/rebind are
exercised through the actual service. No reading, signed-origin freshness,
general RFC interoperability or Linux race claim is made for this selected
send-only profile.

Preliminary native fixtures used external live SQLite polling and encountered
`SQLITE_PROTOCOL (15)` or dispatching state; the initial poll also incorrectly
scanned nullable receipt facts as integers. Those runs did not qualify the mode.
The final fixture follows the existing MQTT-SN receipt-control API pattern and
reopens the journal only after joined shutdown. This is no production SQLite
fix or causal diagnosis. Resource-reserve and numeric-loopback endpoint fixture
errors were also corrected before final native qualification. Earlier AMQP
SQLite fault evidence remains independently documented and unresolved.


## Signed syslog integration checkpoint

The service/native integration is frozen at tracked Go/test/module source SHA-256
`1244fcc9ddd2d7c23bfc18cac6ab44180ae5d117521d20dfd6ec4bde58dd7f0c`,
using sorted tracked Go/module paths and contents, each NUL-separated. New Go
files were staged before freezing. The acceptance runner verifies the digest
before every phase and after termination; its metadata and exact outputs are
under `/tmp/airc-gateway-signed-syslog-v1-a538d8f`.

Full normal acceptance
`go test ./... -count=1 -timeout=900s -parallel=3 -p=2 -json` exited 0 in
652.8732414169936s: 3,160 test pass events, 53 opt-in/platform test skips and no
failures. Thirty packages passed; nine had no tests. The integration package
passed in 644.117s. Full race acceptance
`go test -race ./... -count=1 -timeout=1800s -p=1 -json` exited 0 in
1540.999248667038s with the same test and package counts. Its integration package
passed in 1299.266s. Build (2.206815999990795s), formatting
(0.37785466702189296s, empty output), vet (1.9557876249891706s), staticcheck
(4.080007167009171s) and diff (0.01861349999671802s) all exited 0. The runner
started at 2026-10-07T03:30:55.646739Z and finished at
2026-10-07T04:07:38.983143Z with terminal exit 0 on Darwin ARM64, Go 1.27.1.
Initial load averages were 9.900 / 12.188 / 13.237. The source digest matched
before each phase and after completion.

Selected Darwin native race controls passed in 38.706s. Actual Linux ARM64
execution of the same frozen native controls exited 0 in 22.966531249985565s,
with 10 pass events, no skips and no failures. Linux used the existing offline
Ubuntu image and digest-verified cross-built executables without installing or
downloading a toolchain. Exact binary/image provenance is under
`/tmp/signed-syslog-native-v1-a538d8f`; native evidence and the selected RFC 5848
profile are documented in
[signed-syslog.md](signed-syslog.md#service-native-integration-evidence).
No Linux race instrumentation or general RFC interoperability is claimed.


## SNMP integration checkpoint

The wired service/native integration is frozen at tracked Go/test/module SHA-256
`c7d0cd8e60d26b660b3cc9bb40a3ca3a32ddad490f3c35b34285f1e8d7f7f327`.
New Go files were staged before freezing; the digest uses sorted tracked paths
and content, each NUL-separated. Full normal acceptance exited 0 in
660.7417961250176s with 3,285 test passes, 53 skips and no failures; 31 packages
passed and ten had no tests. The integration package passed in 647.608s.
Full race acceptance exited 0 in 1553.3970378339873s with the same test and
package counts; the integration package passed in 1308.438s. Build
(1.6615794170065783s), formatting (0.32594016700750217s, empty output), vet
(1.3338104159920476s), staticcheck (3.8080497090122662s) and diff
(0.021804834017530084s) all exited 0. The runner started at
2026-10-07T04:51:34.898845Z and finished at 2026-10-07T05:28:36.756505Z
with terminal exit 0 on Darwin ARM64, Go 1.27.1. Initial load averages were
10.322 / 10.784 / 11.394.
The runner verifies the digest before every phase and after termination; exact
metadata and phase outputs are under `/tmp/airc-gateway-snmp-v1-b245138`.

Selected Darwin SNMP native race controls passed in 18.114s. Actual Linux ARM64
native execution of the same frozen source exited 0 in 6.567531833017711s: 13
passes, one explicitly unavailable external Net-SNMP client skip, no failures.
Independent UDP/native fixtures passed on Linux. The real installed Net-SNMP
client's native delivery/replay control passed on Darwin. Linux used the existing
offline Ubuntu image without installing software or enabling external networking,
and no Linux race instrumentation is claimed. Binary/image provenance is under
`/tmp/snmp-native-v1-b245138/provenance.json`. Gateway SHA-256 is
`35abefab282e3ac304f8654bbedb096d1a2f87bd1b351b07c7e713ce8f902eb6`;
test binary SHA-256 is
`feda64b22dc002228655fe0075e5b6548929d3ee81cff8706429e437d7e56090`.
The existing native fixture SHA-256 remains
`1f3aa4a566a23d4326e2a2b3ed81942b5384e17521dc1bf38572511778d7e85f`,
and the offline image ID is
`sha256:33ceb71981b602c1a7443a53469e4dba065f7503eab3078a2d7a57a2ab987517`
under Docker 29.7.2. The selected send-only profile and limitations are documented
in [snmp-v3.md](snmp-v3.md#service-native-integration-evidence).


## NATS service integration checkpoint

On 2026-10-07, configured NATS service activation passed full acceptance on
Darwin ARM64, Go 1.27.1. The frozen Go/module source SHA-256 is
`49b545538c82b310088c6030eaf2b1504f9b49406234b8f14a2a52a0901aca35`,
computed from sorted tracked Go/module paths and content, each NUL-separated.
Source baseline `0e4a69d` plus service activation matches that digest after
termination. The runner checks it before each phase and after termination.

| Gate | Exit | Seconds |
|---|---|---|
| Full normal tests | 0 | 655.391641 |
| Full race tests | 0 | 1587.890822 |
| Build | 0 | 2.481561 |
| Formatting, empty output | 0 | 0.218719 |
| Vet | 0 | 1.697698 |
| Staticcheck | 0 | 5.419080 |
| Diff check | 0 | 0.011999 |

Normal used `go test ./... -count=1 -timeout=900s -parallel=3 -p=2 -json`;
race used `go test -race ./... -count=1 -timeout=1800s -p=1 -json`. Both
recorded 3,419 test passes, 53 opt-in/platform skips, no failures, 32 passing
packages and ten without tests. Integration passed in 648.769s normal and
1300.410s race. Remaining commands were `go build ./...`, `gofmt -l .`,
`go vet ./...`, `staticcheck ./...` and `git diff --check`. All phases and the
runner terminated with exit 0; metadata/stdout/stderr use
`/tmp/airc-gateway-nats-service-v2-0e4a69d`.

The first full normal attempt exited 1 in 664.187479s: gRPC slow-reader native
watch cleanup exceeded its polling bound, and the MQTT future control's healthy
reconnect was refused. Logs remain under the `v1` prefix. Both exact tests then
passed five repetitions with race detection on unchanged source in 108.908s
(`/tmp/airc-gateway-nats-service-v1-focused-repeat.json`), and both passed in
fresh full normal/race runs. These passes do not establish a cause or fix for
the initial failures. The successful runner explicitly references the failed run.

The since-removed NATS specification recorded manual
TCP/TLS service dispatch, signed durable intake, startup refusal and joined cleanup.
A real NATS client and independent AIRC observer remain unqualified; this checkpoint
does not delete that roadmap item or the final gate after all remaining changes.


## Redis Pub/Sub service integration checkpoint

On 2026-10-07, configured Redis Pub/Sub service activation passed full acceptance
on Darwin ARM64, Go 1.27.1. Frozen Go/test/module source SHA-256:
`578c066b6a468e7aac33222b6d772a076efc5202dcfa2fb434e85710fd16b98c`.
The digest covers sorted tracked paths and content, each NUL-separated; baseline
`a632b88` plus staged service integration matches it after termination. The runner
checks the digest before every phase and after termination.

| Gate | Exit | Seconds |
|---|---|---|
| Full normal tests | 0 | 661.116342 |
| Full race tests | 0 | 1595.595165 |
| Build | 0 | 2.187153 |
| Formatting, empty output | 0 | 0.200867 |
| Vet | 0 | 1.320038 |
| Staticcheck | 0 | 6.335077 |
| Diff check | 0 | 0.011133 |

Normal used `go test ./... -count=1 -timeout=900s -parallel=3 -p=2 -json`;
race used `go test -race ./... -count=1 -timeout=1800s -p=1 -json`. Both recorded
3,534 test passes, 53 opt-in/platform skips, no failures, 33 passing packages and
ten packages without tests. Other commands were `go build ./...`, `gofmt -l .`,
`go vet ./...`, `staticcheck ./...` and `git diff --check`. All phases and the
runner terminated with exit 0 at 2026-10-07T08:46:33.822645Z. Metadata/stdout/stderr
remain under `/tmp/airc-gateway-redis-service-v1-a632b88-*`. Initial load was
19.7666/16.1274/13.3213; these are acceptance timings, not a speed comparison.

The [Redis specification](redis-pubsub.md#service-integration-checkpoint) records
broker-only scripted TLS service dispatch, signed durable intake, startup refusal
and joined cleanup. Real Redis client/server and independent native AIRC observer
qualification remains outstanding; this checkpoint does not complete that roadmap
item or final acceptance after all remaining executable changes.


## Streams retained Send recovery checkpoint

Required full acceptance passed on Darwin ARM64, Go 1.27.1, at frozen Go/module
source SHA-256 `b52f54ec86628e46dfeaffad2d5a2575ec9265115f6dc47283104a68f605777a`.
The digest covers sorted tracked paths and contents, each NUL-separated; baseline
`03adece` plus retained Send recovery matches after termination. The runner checked
that digest before each phase and after termination.

| Gate | Exit | Seconds |
|---|---|---|
| Normal | 0 | 650.645321 |
| Race | 0 | 1564.731237 |
| Build | 0 | 1.904756 |
| Formatting, empty output | 0 | 0.110977 |
| Vet | 0 | 0.914483 |
| Staticcheck | 0 | 3.782123 |
| Diff | 0 | 0.010735 |

Normal used `go test ./... -count=1 -timeout=900s -parallel=3 -p=2 -json`;
race used `go test -race ./... -count=1 -timeout=1800s -p=1 -json`. Each recorded
3,604 test passes, 53 opt-in/platform skips, no failures, 33 passing packages and
ten without tests. Remaining commands were `go build ./...`, `gofmt -l .`,
`go vet ./...`, `staticcheck ./...` and `git diff --check`. All phases and the
runner exited 0. Logs and metadata remain under
`/tmp/airc-gateway-streams-retained-v1-03adece-*`. Initial load was
9.0918/9.0596/9.0356; timings establish acceptance, not performance improvement.

This checkpoint covers lookup-only historical signed Send verification and
current-authority matching against retained durable intent, plus earlier Streams
prerequisites. Broker consumption, publication, acknowledgement/reconnect and
real Redis/native qualification remain unfinished. Final acceptance remains due
after remaining executable changes.


## Streams TLS command owner checkpoint

Required full acceptance passed on Darwin ARM64, Go 1.27.1, at frozen Go/module
source SHA-256 `d5aaa748824e307afa90a5a0da44c36f6525ef814aa4a1205713f326d3d24edf`.
The digest covers sorted tracked paths and contents, each NUL-separated; baseline
`34bcc83` plus the Streams TLS command owner matches after termination. The runner
checked that digest before every phase and after termination.

| Gate | Exit | Seconds |
|---|---|---|
| Normal | 0 | 646.681354 |
| Race | 0 | 1596.648218 |
| Build | 0 | 1.901748 |
| Formatting, empty output | 0 | 0.210920 |
| Vet | 0 | 0.999704 |
| Staticcheck | 0 | 3.657259 |
| Diff | 0 | 0.007843 |

Normal used `go test ./... -count=1 -timeout=900s -parallel=3 -p=2 -json`;
race used `go test -race ./... -count=1 -timeout=1800s -p=1 -json`. Each recorded
3,619 test passes, 53 opt-in/platform skips, no failures, 33 passing packages and
ten without tests. Remaining commands were `go build ./...`, `gofmt -l .`,
`go vet ./...`, `staticcheck ./...` and `git diff --check`. All phases and the
runner exited 0. Logs and metadata remain under
`/tmp/airc-gateway-streams-socket-v1-34bcc83-*`. Initial load was
5.2451/6.5137/6.6533; these timings establish acceptance, not performance.

This checkpoint covers verified TLS/ACL authentication, single-socket capacity,
bounded Streams commands and joined cancellation with scripted TLS peers. It does
not qualify a real Redis broker or native Send observation. Signed recovery and
publish-before-acknowledgement control, service integration and real Redis/native
qualification remain unfinished. Final acceptance remains due after remaining
executable changes.


## Streams recovery owner checkpoint

Required full acceptance passed on Darwin ARM64, Go 1.27.1, at frozen Go/module
source SHA-256 `97ca860db9ad6e4d3f142f1dfc6d7d5ef38f4edb70bb3104962f353b16488c8f`.
The digest covers sorted tracked paths and contents, each NUL-separated; baseline
`3fdf314` plus the serial recovery owner matches after termination. The runner
checked that digest before every phase and after termination.

| Gate | Exit | Seconds |
|---|---|---|
| Normal | 0 | 652.446769 |
| Race | 0 | 1637.959940 |
| Build | 0 | 2.902896 |
| Formatting, empty output | 0 | 0.203514 |
| Vet | 0 | 1.779343 |
| Staticcheck | 0 | 6.080372 |
| Diff | 0 | 0.018292 |

Normal used `go test ./... -count=1 -timeout=900s -parallel=3 -p=2 -json`;
race used `go test -race ./... -count=1 -timeout=1800s -p=1 -json`. Each recorded
3,641 test passes, 53 opt-in/platform skips, no failures, 33 passing packages and
ten without tests. Remaining commands were `go build ./...`, `gofmt -l .`,
`go vet ./...`, `staticcheck ./...` and `git diff --check`. All phases and the
runner exited 0. Logs and metadata remain under
`/tmp/airc-gateway-streams-owner-v1-3fdf314-*`. Initial load was
8.7085/9.4346/9.3838; these timings establish acceptance, not performance.

This checkpoint covers serial signed dispatch, retained expired Send lookup,
pending/new read fairness, result publication before ACK, uncertain publication
and ACK loss/reconnect, whole-result/failure retention and joined cancellation
with scripted TLS peers. It does not qualify a real Redis broker or native Send
observation. Service preparation/enabling and real Redis/native qualification
remain unfinished. Final acceptance remains due after remaining executable changes.


## Streams service checkpoint

Required full acceptance passed on Darwin ARM64, Go 1.27.1, at frozen Go/module
source SHA-256 `cf3b8e865b6e90aa51020157ce592a522cef9fc39f78981514cc9f1db113ad2d`.
The digest covers sorted tracked paths and contents, each NUL-separated; baseline
`0e72fac` plus service integration matches after termination. The runner checked
that digest before every phase and after termination.

| Gate | Exit | Seconds |
|---|---|---|
| Normal | 0 | 665.016805 |
| Race | 0 | 1648.332781 |
| Build | 0 | 2.431820 |
| Formatting, empty output | 0 | 0.288581 |
| Vet | 0 | 1.999274 |
| Staticcheck | 0 | 6.184207 |
| Diff | 0 | 0.014477 |

Normal used `go test ./... -count=1 -timeout=900s -parallel=3 -p=2 -json`;
race used `go test -race ./... -count=1 -timeout=1800s -p=1 -json`. Each recorded
3,650 test passes, 53 opt-in/platform skips, no failures, 33 passing packages and
ten without tests. Remaining commands were `go build ./...`, `gofmt -l .`,
`go vet ./...`, `staticcheck ./...` and `git diff --check`. All phases and the
runner exited 0. Logs and metadata remain under
`/tmp/airc-gateway-streams-service-v1-0e72fac-*`. Initial load was
15.4634/22.4951/20.7539; these timings establish acceptance, not performance.

This checkpoint enables service-owned Streams preparation, shared socket/request
capacity, readiness, signed durable result publication before ACK, password/route
snapshots and joined cancellation before password clearing and journal close.
Scripted TLS service tests establish these boundaries; real Redis client/server
and independent native observer qualification remain unfinished. Final acceptance
remains due after remaining executable changes.

## Kafka schema v15 checkpoint

Required acceptance passed on Darwin ARM64 with Go 1.27.1 at frozen
Go/test/module SHA-256
`8cb2c67c07773ee81f1af0c12e1b5ca2c0ce35712762822eb2aab74b7d12d9b4`.
The source starts at `1d108ffd3948d1f115aca2eed60e0493fe409035` plus the
additive Kafka migration and immutable partition provisioning changes. The
runner checked the digest before every phase and after completion. It finished
at `2026-10-07T22:22:29.934995+00:00` with terminal exit 0.

| Gate | Exit | Elapsed seconds |
| --- | --- | --- |
| Normal: `go test ./... -count=1 -timeout=900s -parallel=3 -p=2 -json` | 0 | 652.820937 |
| Race: `go test -race ./... -count=1 -timeout=1800s -p=1 -json` | 0 | 1684.855524 |
| `go build ./...` | 0 | 1.893265 |
| `gofmt -l .` (empty output) | 0 | 0.155862 |
| `go vet ./...` | 0 | 1.204993 |
| `staticcheck ./...` | 0 | 4.477075 |
| `git diff --check` | 0 | 0.011298 |

Each test suite recorded 3772 passes, 53 opt-in/platform skips and no failures;
34 packages passed and 10 had no tests. Initial host load was
10.145 / 11.352 / 11.664. Raw logs and metadata are retained under
`/tmp/airc-gateway-kafka-schema-v15-r2-1d108ff-*`, with runner
`/tmp/airc-gateway-kafka-schema-v15-r2-acceptance-runner.py`.

The first normal attempt on the same digest exited 1 in 680.251102 s, recording
3771 passes, 53 skips and one failed test: `TestHTTP3SignedAndEncryptedProfiles`
at `http3_detached_test.go:48`, whose client reported no recent network activity.
Its immediate unchanged-source focused rerun passed in 4.543 s. The fresh full
normal/race runs above passed. These observations do not establish a cause or
fix for the original timeout. Original logs remain under
`/tmp/airc-gateway-kafka-schema-v15-1d108ff-*`.

This checkpoint proves the additive journal migration and bounded immutable
position provisioning with existing durable state preserved. It also includes
the earlier Kafka wire and authenticated connection prerequisites. It does not
enable Kafka ingestion or qualify an actual producer/broker/native AIRC path.

## Kafka atomic reservation checkpoint

Required acceptance passed on Darwin ARM64, Go 1.27.1, at frozen Go/test/module
SHA-256 `deb242568d9c5654dc3ea59bbb4ca95de46e6253e0600889e27535367ee36c21`.
The source starts at `8dbde74e7331fb3d4167583c5207c87b641899b3` plus atomic Kafka
reservation, shared pending budgets and cleanup protection. All new source files
were staged before freezing. The runner checked the digest before every phase and
after completion, finishing at `2026-10-07T23:09:27.813432+00:00` with exit 0.

| Gate | Exit | Elapsed seconds |
| --- | --- | --- |
| Normal: `go test ./... -count=1 -timeout=900s -parallel=3 -p=2 -json` | 0 | 663.035992 |
| Race: `go test -race ./... -count=1 -timeout=1800s -p=1 -json` | 0 | 1673.458003 |
| `go build ./...` | 0 | 2.596813 |
| `gofmt -l .` (empty output) | 0 | 0.351646 |
| `go vet ./...` | 0 | 1.735452 |
| `staticcheck ./...` | 0 | 5.019814 |
| `git diff --check` | 0 | 0.018381 |

Each suite recorded 3784 passes, 53 opt-in/platform skips and no failures;
34 packages passed and 10 had no tests. Initial host load was
13.295 / 11.260 / 11.699. Logs and metadata remain under
`/tmp/airc-gateway-kafka-reservation-v1-8dbde74-*`; runner:
`/tmp/airc-gateway-kafka-reservation-v1-acceptance-runner.py`.

The complete store race suite also passed in 54.914 s. Tests cover atomic Send
rollback at capacity, restart/coalescing, changed intent and pending-record
conflicts, durable revocation, invalid scope/lease/closed state, unknown historical
Send refusal, retained-job cleanup protection, shared read charges across
partitions and both retained-row caps without eviction or advancement. This is
journal prerequisite evidence, not Kafka service activation or actual broker,
producer and native AIRC qualification. Those remain open in the roadmap.

## Kafka outcome and publication checkpoint

Required acceptance passed on Darwin ARM64, Go 1.27.1, at frozen Go/test/module
SHA-256 `ecb32490a2d16749fca59e1d1600280cd3a9c5ed81b70ee95ff263ff1c29c1ef`.
The source starts at `c5faaae8a41f5b2dd3282419ed6a600634074120` plus whole-outcome
retention and atomic publication advancement. All new source files were staged
before freezing. The runner checked the digest before every phase and after
completion, finishing at `2026-10-07T23:54:28.216310+00:00` with exit 0.

| Gate | Exit | Elapsed seconds |
| --- | --- | --- |
| Normal: `go test ./... -count=1 -timeout=900s -parallel=3 -p=2 -json` | 0 | 660.714261 |
| Race: `go test -race ./... -count=1 -timeout=1800s -p=1 -json` | 0 | 1670.093223 |
| `go build ./...` | 0 | 2.133982 |
| `gofmt -l .` (empty output) | 0 | 0.150134 |
| `go vet ./...` | 0 | 1.493152 |
| `staticcheck ./...` | 0 | 4.533726 |
| `git diff --check` | 0 | 0.025242 |

Each suite recorded 3787 passes, 53 opt-in/platform skips and no failures.
Initial host load was 9.861 / 11.092 / 11.396. Logs and metadata remain under
`/tmp/airc-gateway-kafka-outcome-v1-c5faaae-*`; runner:
`/tmp/airc-gateway-kafka-outcome-v1-acceptance-runner.py`.

Tests cover whole first-winner retention, concurrent identical read copies,
restart before/after publication, owned output, bounds and authority refusals,
timestamp/epoch/expected-position checks, pending-range refusal and transaction
rollback. Empty-range proof and validated broker publication remain trusted-owner
responsibilities; actual Fetch/service integration and producer/broker/native
qualification are unfinished. These journal tests do not qualify those paths.

## Kafka retention checkpoint

Required acceptance passed on Darwin ARM64, Go 1.27.1, at frozen Go/test/module
SHA-256 `78a107770a1d7d2f742718002e63307132c782b4e6b51424c08e3e981af0169c`.
The source starts at `5731c5c46b1d74a1d12abab964ef79078a06e5d4` plus Kafka
maintenance cleanup and its tests. All new source files were staged before
freezing. The runner checked the digest before every phase and after completion,
finishing at `2026-10-08T00:36:21.351160+00:00` with exit 0.

| Gate | Exit | Elapsed seconds |
| --- | --- | --- |
| Normal: `go test ./... -count=1 -timeout=900s -parallel=3 -p=2 -json` | 0 | 662.877071 |
| Race: `go test -race ./... -count=1 -timeout=1800s -p=1 -json` | 0 | 1667.806875 |
| `go build ./...` | 0 | 2.439924 |
| `gofmt -l .` (empty output) | 0 | 0.203823 |
| `go vet ./...` | 0 | 1.781910 |
| `staticcheck ./...` | 0 | 5.890178 |
| `git diff --check` | 0 | 0.015735 |

Each suite recorded 3789 passes, 53 opt-in/platform skips and no failures.
Initial host load was 11.967 / 11.443 / 11.236. Logs and metadata remain under
`/tmp/airc-gateway-kafka-retention-v1-5731c5c-*`; runner:
`/tmp/airc-gateway-kafka-retention-v1-acceptance-runner.py`.

Affected retention/maintenance race tests passed in 2.627 s; the complete store
race suite passed in 55.463 s. Tests cover stored retention after restart/config
change, pending shared outcome preservation, exact completion and inclusive
replay boundaries, permanent positions and failed-commit rollback with zero
claimed cleanup. This is journal evidence; signed Kafka dispatch, service
activation and actual producer/broker/native qualification remain unfinished.

## Kafka signed dispatcher checkpoint

Required acceptance passed on Darwin ARM64, Go 1.27.1, at frozen Go/test/module
SHA-256 `15e2722c7da5324988d14a8f1ba9d7b55b30981121072bfee1d0eb7a4f47bc2f`.
The source starts at `20679f00e973a08e052073e1d407cb3a48cf95c3` plus dispatcher
reservation bridges and signed-envelope tests. New source files were staged
before freezing. The runner checked the digest before every phase and after
completion, finishing at `2026-10-08T01:18:47.374720+00:00` with exit 0.

| Gate | Exit | Elapsed seconds |
| --- | --- | --- |
| Normal: `go test ./... -count=1 -timeout=900s -parallel=3 -p=2 -json` | 0 | 655.410163 |
| Race: `go test -race ./... -count=1 -timeout=1800s -p=1 -json` | 0 | 1655.980834 |
| `go build ./...` | 0 | 2.468904 |
| `gofmt -l .` (empty output) | 0 | 0.326422 |
| `go vet ./...` | 0 | 1.357481 |
| `staticcheck ./...` | 0 | 3.235816 |
| `git diff --check` | 0 | 0.017793 |

Each suite recorded 3796 passes, 53 opt-in/platform skips and no failures.
Initial host load was 10.533 / 10.343 / 10.119. Logs and metadata remain under
`/tmp/airc-gateway-kafka-dispatch-v1-20679f0-*`; runner:
`/tmp/airc-gateway-kafka-dispatch-v1-acceptance-runner.py`.

Focused adapter/dispatcher race checks passed in 2.497 s and 1.299 s. Signed
fixtures prove private dispatcher ownership, original key/intent preservation,
coalescing, read reservation without execution, retained Send across restart,
conflict/unknown/expired-read refusal, revocation and unchanged job leases and
positions. These are dispatcher/journal prerequisites. Whole result encoding,
ordered processing, service integration and actual broker/native qualification
remain unfinished.

## Kafka signed result checkpoint

Required acceptance passed on Darwin ARM64, Go 1.27.1, at frozen Go/test/module
SHA-256 `5bb5d505f93c24817b38b642b651a9aceb626e79a7fab289e2015a05a0951d6d`.
The source starts at `6f837a2db38db32887cca785745366aa11daa266` plus the whole signed result
encoder and canonical cache/read replay tests. New source files were staged
before freezing. The runner checked the digest before every phase and after
completion, finishing at `2026-10-08T02:03:53.997112+00:00` with exit 0.

| Gate | Exit | Elapsed seconds |
| --- | --- | --- |
| Normal: `go test ./... -count=1 -timeout=900s -parallel=3 -p=2 -json` | 0 | 661.976068 |
| Race: `go test -race ./... -count=1 -timeout=1800s -p=1 -json` | 0 | 1651.996948 |
| `go build ./...` | 0 | 2.478491 |
| `gofmt -l .` (empty output) | 0 | 0.219731 |
| `go vet ./...` | 0 | 1.502907 |
| `staticcheck ./...` | 0 | 4.069332 |
| `git diff --check` | 0 | 0.023161 |

Each suite recorded 3799 passes, 53 opt-in/platform skips and no failures.
Initial host load was 9.633 / 10.371 / 10.674. Logs and metadata remain under
`/tmp/airc-gateway-kafka-result-v1-6f837a2-*`; runner:
`/tmp/airc-gateway-kafka-result-v1-acceptance-runner.py`.

Focused Kafka encoder/cache/read race checks passed in 1.624 s; affected vet and
staticcheck passed. Fixtures cover exact record/route/identity scope, canonical
cache and original intent validation, whole operation-specific results, unknown
Send acceptance, complete history/search/context metadata and cursors, fresh
response signing and final compact-JWS size refusal. Ordered processing, service
integration and actual broker/native qualification remain unfinished.

## Kafka Fetch range prerequisite checks

The private range planner has no service caller and changes no journal write
path. Broader adapter, Kafka wire and journal normal/race checks and repository
build/static gates passed on Darwin ARM64, Go 1.27.1, at frozen Go/test/module
SHA-256 `4023f190a82f385bc7ed4489071268ca7db2c233f105cfd7d13a1d4633444422`, starting at
`53a87a8bcafe2e041769cd3ad1ee5bd1d76d07bf` plus the planner and tests.
New source was staged before freezing. The runner checked the digest before every
phase and after completion, finishing at `2026-10-08T02:15:20.183604+00:00`
with exit 0. Full integration acceptance remains required when the Kafka owner
is connected; these package checks do not replace that gate or qualify a broker.

| Gate | Exit | Elapsed seconds |
| --- | --- | --- |
| `go test ./internal/gateway/adapter ./internal/gateway/kafkawire ./internal/gateway/store -count=1 -timeout=900s -parallel=3 -p=2 -json` | 0 | 119.405195 |
| `go test -race ./internal/gateway/adapter ./internal/gateway/kafkawire ./internal/gateway/store -count=1 -timeout=1800s -p=1 -json` | 0 | 205.464560 |
| `go build ./...` | 0 | 2.510125 |
| `gofmt -l .` (empty output) | 0 | 0.112751 |
| `go vet ./...` | 0 | 0.904539 |
| `staticcheck ./...` | 0 | 4.588566 |
| `git diff --check` | 0 | 0.011105 |

Normal and race checks each recorded 1043 passes, four opt-in/platform skips and
no failures. Initial host load was 9.855 / 10.622 / 10.802. Logs and metadata
remain under `/tmp/airc-gateway-kafka-range-v1-53a87a8-*`; runner:
`/tmp/airc-gateway-kafka-range-v1-check-runner.py`. Focused range race tests passed
in 1.557 s before one additional compacted-remainder case; the broader checks
include that final case. Affected vet/staticcheck also passed.

Fixtures cover complete compacted coverage, owned snapshots, prior-record
filtering, unstable suffixes, empty Fetch without inferred progress, exact
position bounds, leader epochs and whole-plan refusal of malformed later data.
These are planner checks, not signed admission, advancement or native delivery.

## Kafka ordered processing prerequisite checks

The private processor has no service caller. All gateway-package normal/race
checks and repository build/static gates passed on Darwin ARM64, Go 1.27.1, at
frozen Go/test/module SHA-256
`542306f9423daa1edd462f9a06d9f3a8dcdb8c5f5a35a71b76855feb13486f57`. The source starts at
`40809ea8d842cecb054601736ae5c8f42ad3e93c` plus ordered record processing,
its signed/socket/journal/SDK tests and immutable verified replay-deadline access.
New source was staged before freezing. The runner checked the digest before every
phase and after completion, finishing at `2026-10-08T02:52:59.658238+00:00`
with exit 0. Full integration acceptance remains required when the Kafka service
owner is connected. These checks do not qualify an actual Kafka broker.

| Gate | Exit | Elapsed seconds |
| --- | --- | --- |
| `go test ./internal/gateway/... -count=1 -timeout=900s -parallel=3 -p=2 -json` | 0 | 123.706164 |
| `go test -race ./internal/gateway/... -count=1 -timeout=1800s -p=1 -json` | 0 | 276.473164 |
| `go build ./...` | 0 | 3.232559 |
| `gofmt -l .` (empty output) | 0 | 0.272753 |
| `go vet ./...` | 0 | 1.811092 |
| `staticcheck ./...` | 0 | 7.936029 |
| `git diff --check` | 0 | 0.011577 |

Normal and race each recorded 2497 passes, 21 opt-in/platform skips and no
failures. Initial host load was 11.287 / 11.329 / 10.656. Logs and metadata remain under
`/tmp/airc-gateway-kafka-record-v1-40809ea-*`; runner:
`/tmp/airc-gateway-kafka-record-v1-check-runner.py`. Focused Kafka adapter race
checks passed in 13.982 s; the accompanying dispatcher test-name selection had
no matches. Affected vet/staticcheck passed; broader checks include the complete
dispatcher package.

Fixtures cover signed original intent, outcome-before-publication, validated
all-replica replies, preserved completed prefixes, restart/reply-loss replay,
unchanged expired Send jobs, real bounded SDK history collection against a
scripted native peer, whole page/cursor retention, failure/capacity responses
without completion, corrupt caches, compacted/pending ranges, shared capacity,
signed replay deadlines and joined native cancellation. No actual broker or
independent native observer qualification is inferred from these scripted peers.

## Kafka service integration checkpoint

Full acceptance passed for the Kafka service integration based on `69cda5b`,
with the integration Go/test changes and unchanged executable/test/module source digest
`d5440272883d013df20699d6d6233ee05aa7dfbfc06c020f5c2480b71c4c3c49`.
The runner checks the sorted tracked Go/module path-and-content digest before
every phase and again at terminal completion.

Full normal acceptance completed with exit zero in 643.583853 seconds using
`go test ./... -count=1 -timeout=900s -parallel=3 -p=2 -json`.
It recorded 3881 test passes, 53 skips and no test or package failures.
Full race acceptance completed with exit zero in 1582.656208 seconds using
`go test -race ./... -count=1 -timeout=1800s -p=1 -json`, also recording 3881
passes, 53 skips and no test or package failures. Repository gates all exited
zero: build (1.491114s), formatting (0.148706s, no output), vet (1.038424s),
staticcheck (3.429416s) and diff (0.011655s). The runner reached terminal exit
zero and verified the unchanged source digest after the final gate.

The run used Go 1.27.1 on Darwin ARM64, starting at
2026-10-08T03:21:30.839386Z and finishing at 2026-10-08T03:58:43.796192Z.
Initial load averages were 3.7890625 / 11.2880859375 / 11.6220703125.
Focused Kafka service race tests previously passed in 3.137s. Service fixtures
cover failed local preparation, owned route/credential snapshots, blocked
TLS/authentication/Fetch cancellation, journal release, and signed original Send
through whole result publication to the next durable Fetch position. They verify
response signature/scope and original journal intent; they do not substitute for
an actual Kafka producer/broker and independent native observer.
Metadata and phase output use the prefix
`/tmp/airc-gateway-kafka-service-v1-69cda5b-`.
Real Kafka producer/broker/native qualification remains independently unfinished.

## ZeroMQ wire prerequisite checks

At `95fefc7`, the unused private ZMTP 3.1 codec passed normal tests (0.248s) and race tests
(1.325s), followed by repository-wide build, vet and staticcheck, codec gofmt
and `git diff --check`, all with terminal exit 0. Literal fixtures exercise
REQ/REP envelopes, short/long frame boundaries, oversized declarations, every
truncation of representative frames and greetings, owned buffers, metadata
uniqueness/count bounds and role refusal. No dependency was added.

These checks cover the wire prerequisite only. Service acceptance is recorded
below; actual ZeroMQ REQ/native AIRC qualification remains outstanding.
See the selected [profile](zeromq.md).

## ZeroMQ service integration checkpoint

Focused ZeroMQ adapter/config/service and wire race checks passed with terminal
exit 0: adapter 2.721s, config 1.921s, service 2.596s and wire 1.770s. Checks
exercise encrypted signed send/replay, plaintext and expired-envelope refusal,
conflicting signed IDs, durable revocation, global request/session saturation,
whole Unicode history/page/cursor/gap preservation, oversized read refusal and
joined cancellation during blocked greeting and reply writes. The actual service
Ready record, encrypted TCP dispatch, journal release and socket rebind are
covered. The selected [profile](zeromq.md) remains loopback-only.

Peers in these tests are scripted ZMTP wire clients and scripted native CHECK
output driving the real SDK collector. They do not qualify a native ZeroMQ REQ
client or an independent AIRC observer. Actual native qualification remains
a separate roadmap item.

Full unchanged-source acceptance passed with terminal exit 0. Normal and race
runs each recorded 3,909 passes, 53 opt-in/platform skips and no test/package
failures. Commands and elapsed seconds:

- Normal: `go test ./... -count=1 -timeout=900s -parallel=3 -p=2 -json` (645.388s).
- Race: `go test -race ./... -count=1 -timeout=1800s -p=1 -json` (1,583.596s).
- Build: `go build ./...` (1.433s).
- Formatting: `gofmt -l .`, empty output (0.119s).
- Vet: `go vet ./...` (1.163s).
- Staticcheck: `staticcheck ./...` (3.477s).
- Diff: `git diff --check` (0.010s).

The runner checked this Go/test/module source digest before every phase and
after the final gate; it was independently rechecked before the checkpoint:
`ad83847bdbd72a00c6b79e38fcf904a637355c46d6bf0a07ed4e9c87bc7a12a7`.
Digest inputs are sorted tracked Go/module paths and contents, each NUL-separated,
including the staged new source/tests over `95fefc7`. No dependencies changed.

The run used Go 1.27.1 on Darwin ARM64, starting at
2026-10-08T04:15:57.133397Z and finishing at 2026-10-08T04:53:12.829588Z.
Initial load averages were 3.03564453125 / 2.91845703125 / 2.95458984375.
Runner: `/tmp/airc-gateway-zeromq-service-v1-acceptance-runner.py`. Evidence
metadata and per-phase stdout/stderr use prefix
`/tmp/airc-gateway-zeromq-service-v1-95fefc7-`. These results establish the
service integration checkpoint, not actual ZeroMQ/native AIRC qualification.

## NNG wire prerequisite checks

At the wire prerequisite checkpoint, the unused private SP/TCP codec passed normal tests (0.249s) and race tests
(1.322s), repository-wide build, vet and staticcheck, codec gofmt (empty output)
and `git diff --check`, all with terminal exit 0. Literal fixtures cover peer
roles, reserved/version bytes, every representative truncation, whole-message
boundaries, owned buffers, terminal request identifiers and declared length
limits through uint64 overflow-sized inputs. No dependency was added.

These checks cover the codec prerequisite only. Full unchanged-source service
acceptance is recorded below; actual NNG REQ0/native AIRC qualification remains
outstanding.
See the selected [profile](nng.md).

### Shared local REP dispatch prerequisite

The ZeroMQ exchange now calls a shared local REP implementation for encrypted
signed authentication, shared request admission, original signed request identity,
three-second dispatch and reply deadlines, and complete version-1 JSON results.
The internal response type is `LocalREPResponse`; JSON fields and ZeroMQ framing
are unchanged. The reply encoder is the only transport-specific argument.

On 2026-10-08, existing ZeroMQ adapter, configuration and real local service
tests passed normally and with the race detector (`-run ZeroMQ -count=1`).
All `zmqwire` tests also passed normally and with the race detector. Repository
`go build ./...`, `go vet ./...`, `staticcheck ./...`, formatting and diff checks
passed. This is an extraction checkpoint; subsequent NNG integration and frozen
full-repository acceptance are recorded below.

### NNG service integration acceptance

The `nng` listener uses the bounded SP/TCP codec with the shared encrypted local
REP dispatch path. On 2026-10-08, NNG and ZeroMQ adapter, configuration and
service tests passed normally and under the race detector (`-run 'NNG|ZeroMQ'
-count=1`). All NNG and ZeroMQ wire tests passed normally and under the race
detector. Focused coverage includes original signed-ID replay across reconnects
and changed wire correlations, plaintext/expiry/authentication refusal, durable
conflict and revocation, shared capacity, malformed lengths and nonterminal
identifiers, complete bounded native SDK history pages, partial/blocked handshake
and reply cancellation, service Ready, journal release and listener rebinding.
Scripted SP/TCP clients and scripted native CHECK output are not native-client
qualification. Actual NNG client qualification remains on the roadmap.

Full unchanged-source acceptance completed with terminal exit 0 for every phase:

| Gate | Result | Elapsed seconds |
| --- | --- | ---: |
| `go test ./... -count=1 -timeout=900s -parallel=3 -p=2 -json` | 3,942 passing tests, 53 skips, no test/package failures | 645.723 |
| `go test -race ./... -count=1 -timeout=1800s -p=1 -json` | 3,942 passing tests, 53 skips, no test/package failures | 1,599.524 |
| `go build ./...` | passed | 2.209 |
| `gofmt -l .` | empty output | 0.114 |
| `go vet ./...` | passed | 0.676 |
| `staticcheck ./...` | passed | 2.030 |
| `git diff --check` | passed | 0.007 |

The runner verified the source digest before every phase and after the final
gate; it was independently rechecked before this checkpoint:
`5363e504a0935276cc3318236d0c2749d6648980fe0da8cf3938b3ade40043cc`.
Inputs are sorted tracked Go/module paths and contents, each NUL-separated,
including staged source/tests over `a7084da`. No dependencies changed.

Go 1.27.1 on Darwin ARM64, start 2026-10-08T05:05:00.338348Z, finish
2026-10-08T05:42:31.220195Z; initial load averages
4.75146484375 / 3.533203125 / 3.10546875. Runner:
`/tmp/airc-gateway-nng-service-v1-acceptance-runner.py`; metadata and per-phase
stdout/stderr prefix `/tmp/airc-gateway-nng-service-v1-a7084da-`.
The copied runner's final text label still says ZeroMQ; its metadata, source
digest, commands and terminal exit identify this NNG integration run. These
results establish service integration, not actual NNG/native AIRC qualification.

## IRC wire prerequisite checks

On 2026-10-08 the unused private IRCv3 codec passed normal tests (0.302s) and
race tests (1.283s), repository-wide `go build ./...`, `go vet ./...`,
`staticcheck ./...`, codec gofmt (empty output), and `git diff --check`, all
with terminal exit 0. Literal fixtures cover independent 8,191-byte tag and
512-byte message limits, maximum whole reader lines, overlong unterminated
input, every representative truncation, owned parsed strings, Unicode and
whitespace preservation, empty trailing text, 15-parameter limits, tag escape
edge cases, repeated tags, case sensitivity and server/client tag separation.
No dependencies changed. These checks establish a private wire prerequisite,
not authenticated bridge ingestion or native IRC/AIRC qualification. See the
[selected profile](irc-bridge.md); remaining bridge work stays on the roadmap.

The IRC encoder review correction preflights command/component lengths and the
complete wire size before uppercasing or concatenating. One-megabyte command,
parameter and trailing-body inputs are each refused with zero allocations;
maximum valid whole commands still pass. After the correction, focused normal
tests (0.302s), race tests (1.257s), all-repository build/vet/staticcheck, codec
formatting and diff checks passed with terminal exit 0.

## IRC provisioning prerequisite checks

The unused IRC bridge provisioning types and explicit validation passed focused
normal tests (0.309s) and race tests (1.292s) on 2026-10-08. Repository-wide
build, vet and staticcheck, changed-source formatting and diff checks passed
with terminal exit 0. Tests cover IPv4/IPv6 TLS endpoints; private-path syntax;
freshness and 128-grant limits; literal account/channel rules; case collisions;
bridge-account loops; revoked/unknown principals; exact target authorization;
operation subsets, duplication and cursor requirements. Validation performs no
I/O and does not authenticate a server or sender. The types are not registered
in configuration JSON or service startup. Server-session authentication, service
integration/full acceptance and native IRC/AIRC qualification remain open.

## IRC registration prerequisite checks

The unused private capability/SASL registration state machine passed normal
tests (0.281s), race tests (1.261s), all-repository build/vet/staticcheck,
changed-source gofmt (empty output) and diff checks with terminal exit 0 on
2026-10-08. Literal fixtures cover required capability acknowledgements, wrong
account/nickname, out-of-order and failed exchanges, logout, early traffic,
user-prefixed control replies, PING, 128-message registration limits, owned
credential copies, complete base64 data, 400-byte chunk/terminator boundaries,
credential clearing, and readiness only after account confirmation plus SASL
success and welcome. Account confirmation and SASL success may arrive in either
order. Capability removal invalidates readiness. These are protocol-state
checks, not a TLS server-session proof or actual IRC/native qualification.

## IRC TLS session prerequisite checks

On 2026-10-08 the unused private IRC TLS session owner passed focused normal
tests (3.506s), race tests (4.556s), all-repository build/vet/staticcheck,
changed-source gofmt (empty output) and diff checks, all with terminal exit 0.
Real isolated TCP/TLS fixtures verify the complete CAP/SASL/welcome exchange,
certificate name and trust-root refusal, TLS 1.2 refusal, wrong account refusal,
credential/configuration snapshots, credential-memory clearing, idempotent close
and cancellation joining during blocked TLS and pending registration. Repeated
PING traffic cannot renew the complete three-second registration deadline.
The tests exercise the real TLS stack with a scripted IRC peer; they do not
establish actual IRC server/client/native AIRC qualification or event authority.
Server-session event proofs and service integration/full acceptance remain open.

## IRC event authentication prerequisite checks

On 2026-10-08 the unused event/session prerequisite passed focused IRC auth
normal tests (3.808s), race tests (4.580s), wire tests (0.238s), configuration
tests (0.703s), repository build, vet, staticcheck, gofmt and diff checks.
Real TCP/TLS scripted events verify exact account/room mapping, refusal of
client-only account tags and bridge echoes, immutable whole Unicode content,
stable replay ID/fingerprint/deadline despite nickname changes, and capability
removal closing readiness. Boundary tests cover invalid/future/expired timestamps,
missing/invalid event IDs, malformed prefixes, CTCP/blank bodies, changed-content
fingerprints, network-scoped IDs, nil proofs and verifier authority mismatch or
revocation. These checks establish opaque session event provenance, not durable
ingestion, bounded read replies, service/full acceptance or native qualification.

## IRC journal v16 initial acceptance failure

The initial frozen-source run used source SHA-256
`4f7f619c70d2e5fde074dfb767e66b859d265cb89b11c3c0f33669b0800dc6c9`
over `8aada13528eaf695a70a61b959ca15f926ff4177`, with staged Go files included.
Normal acceptance passed in 655.138s: 4,009 passed, 53 skipped, zero failed.
Race acceptance failed in 1,195.058s: 3,778 passed, 53 skipped, 177 failed.
The later build/format/vet/staticcheck/diff phases did not run. The runner returned
terminal exit 1 at 2026-10-08T06:48:11.281628 UTC; metadata and output remain at
`/tmp/airc-gateway-irc-journal-v16-8aada13-*`, with runner
`/tmp/airc-gateway-irc-journal-v16-acceptance-runner.py`.

All failed tests were in `integration/gateway`, beginning with
`TestRoomUpdateAppliesRetentionQuota`; journal opening reported
`journal exceeds configured page capacity` before service readiness.
That first case passed in an isolated race run with unchanged source (7.515s).
This does not establish the cause or erase the full-run failure. The capacity
check precedes schema migration, and the original error concealed whether
SQLite returned an error or a mismatched page count. Diagnostic changes now
preserve that distinction while retaining the exact bound. A regression check
refuses an existing oversized journal, reports requested/actual pages, releases
ownership and preserves its contents. Full acceptance remains required before
this journal checkpoint can complete.

The second frozen-source run used
`538d73f125ca8df31eaecd71dfbd7c0f3a3cf3179f36242bf07605adf4c8c923`.
Normal acceptance passed in 645.753s: 4,010 passed, 53 skipped, zero failed.
Race ended with exit 1 in 1,388.811s: 1,073 passed, 14 skipped, one failed test
(`TestDTLSNativeSendAndReads`, handshake deadline), plus 31 package build failures.
The compiler could not open previously referenced standard-library/module
artifacts under `/Users/sws/Library/Caches/go-build`: files were missing. The
later packages therefore did not run; the counts are not full race coverage.
The DTLS case passed in isolation with the same binary/deadlines (7.736s).
Neither result proves the cause of that handshake timeout. No journal-capacity
failure recurred in this run. Metadata/output remain at
`/tmp/airc-gateway-irc-journal-v16-r2-8aada13-*`, runner
`/tmp/airc-gateway-irc-journal-v16-r2-acceptance-runner.py`, terminal UTC
2026-10-08T07:25:11.522674. Build/format/vet/staticcheck/diff phases did not run.
The next race/static run will use a private build cache and reuse this successful
normal result only while the source digest remains identical.

The private-cache third race run retained the same source digest but was
explicitly stopped with SIGINT to its verified owned process group (87717).
The tool returned terminal exit 130, and a subsequent process inventory showed
that group had joined. The original runner metadata is not a successful or
completed gate; its operator stop is recorded separately at
`/tmp/airc-gateway-irc-journal-v16-r3-8aada13-operator-stop.json`.
Observed counts before stopping were 492 passed, 8 skipped, and two failed test
entries for one case (`TestAgentsReadUnixAndTLSFramed/tls` and its parent).
The TLS client received EOF. No package build failure had occurred.

Inspection found that `framedFixtureAddress` opened its returned client before
`framedExample` could compile and execute its owned client. The returned client
was left idle during cold compilation, despite the production idle bound.
The fixture now runs the complete example discovery check before opening that
client. Certificate/name refusals, authenticated native send/read assertions,
production deadlines and race detection are preserved. This fixture correction
requires its own focused checks and a new full journal acceptance digest.

The framed fixture correction (`95b83f9`) passed focused normal (25.367s) and race
(43.071s) integration checks from a new private cache
`/tmp/airc-gateway-framed-fix-cache.YHpAPO`, using
`-run '^TestAgentsReadUnixAndTLSFramed$/^tls$|^TestFramed(IPv4|TLS)AuthenticatedSendAndBoundedResume$'`
with count 1 and timeout 300s. Repository build, vet, staticcheck, fixture gofmt
and diff checks also returned zero. The complete example is still validated
before the returned native-test client is opened; this is a fixture-lifecycle
checkpoint, not full IRC journal acceptance.

### IRC journal v16 acceptance

The unchanged Go/module source SHA-256
`a3e9ea9ce06e43d36c78e4947b59314570701938f0357f49394339b8061b9b2a`
passed full checkpoint acceptance on Darwin ARM64, Go 1.27.1, from
2026-10-08 07:42:36 UTC to 08:21:54 UTC. The reviewed framed fixture
checkpoint was `95b83f9084c0dd9a7f7641440cec34183cfb8e18`; journal changes
were staged throughout. Both normal and race suites reported 4,010 passing
tests, 53 skips, zero test failures and zero package build failures.

Commands and terminal exits: `go test ./... -count=1 -timeout=900s -parallel=3
-p=2 -json` (651.220 seconds, exit 0), `go test -race ./... -count=1
-timeout=1800s -p=1 -json` (1700.088 seconds, exit 0), `go build ./...`,
`gofmt -l .` (empty output), `go vet ./...`, `staticcheck ./...`, and
`git diff --check` (all exit 0). Source digest checks before every phase and
after the final phase confirmed unchanged source. The runner used private
`GOCACHE=/tmp/airc-gateway-framed-fix-cache.YHpAPO`; exact metadata and
phase output are retained under
`/tmp/airc-gateway-irc-journal-v16-r4-95b83f9-*`. Its terminal exit was 0.

This verifies schema v16 migration, atomic IRC event/send admission, precise
original freshness, global event fingerprint binding, current authorization,
concurrent replay, rollback, capacity and expiry boundaries, and the existing
repository checks. It does not establish IRC service ingestion, bounded read
replies or real IRC server/client qualification; those remain on the roadmap.

### IRC dispatcher prerequisite verification

On 2026-10-08 the unused verifier-owned IRC dispatcher entry point passed
`go test ./internal/gateway/dispatch -run IRC -count=1` (0.361 seconds) and
the same command with `-race` (1.666 seconds). A real certificate-verified
TCP/TLS registration and public event reader supplied the opaque proof. Checks
cover nil/foreign/missing verifier or journal, zero proof, unconfigured Send,
whole Unicode/trailing-space intent, stable replay, exact expiry and durable
revocation. The first test attempt lacked ReceiptGet permission for the journal
observer and was corrected; route proof authority remains Send only.

Go/module source digest:
`8dcf4572e83ec323bbf64f74511accae4a7ae220ffdd2f9fbb8ea362ee0a2aab`.
`go build ./...`, `go vet ./...`, `staticcheck ./...`, formatting of changed
Go files and `git diff --check` also passed, terminal exit 0. These focused
checks do not constitute full acceptance for this newer source. Service
integration, bounded replies and real IRC/native qualification remain open.

### IRC provisioned-room joining prerequisite verification

On 2026-10-08 source SHA-256
`33a7689fbb2e4ae64c5451beaaa24c89f8234886d8a4cea4fbb4b441e7e1551d`
passed `go test ./internal/gateway/auth -run IRC -count=1` (6.616 seconds)
and the same command with `-race` (7.807 seconds). Real verified TCP/TLS
fixtures verify own JOIN confirmation, both trailing and ordinary room forms,
multiple fixed rooms, shared-room deduplication, invite-only refusal, another
user's JOIN, premature messages and the control-line budget. Foreign confirmation
reaches the original absolute deadline without establishing membership.
All-repository build, vet and staticcheck, changed-file formatting and diff
checks passed, terminal exit 0. This is an unused session prerequisite; full
newer-source acceptance and IRC service/read/native qualification remain open.

The review of membership checkpoint `10a8fbc` found Unicode case-fold aliases
could count as own membership. Received nicknames and rooms now require the
selected ASCII profile before case comparison. Actual TLS fixtures with
configured `keeper` / `#keeper` and received Kelvin-sign lookalikes verify
neither establishes membership; valid confirmation and prior boundaries remain
covered. Final `go test ./internal/gateway/auth -run IRC -count=1` passed
(12.645 seconds), race passed (13.801 seconds), and repository build, vet,
staticcheck, changed-file formatting and diff checks all passed, terminal exit 0.
The preliminary lowercase comparison failed staticcheck SA6005; the final
ASCII-gated comparison passes. Full service/native acceptance remains pending.

### IRC ingestion owner prerequisite verification

On 2026-10-08 Go/module source SHA-256
`0608c7a14d8e8634748bb183745900481693b83b8e6acb48454778cc47fa2998`
passed `go test ./internal/gateway/adapter ./internal/gateway/auth -run IRC
-count=1` (adapter 0.912 seconds, auth 12.982 seconds) and the same command
with `-race` (adapter 2.779 seconds, auth 13.852 seconds). Repository build,
vet and staticcheck, changed-file formatting and diff checks also passed,
terminal exit 0. Actual verified TCP/TLS scripted peers cover registration and
room joining, unmapped-publication refusal followed by valid whole Unicode
sends, same-event replay with a changed nickname, post-replay PING/PONG,
request capacity loss before intake, preserving foreign capacity, failed
registration reconnect backoff and joined cancellation. The scripted peer
is not real IRC server/client qualification or independent native delivery.

This owner is not yet called by the service. Full newer-source acceptance,
service configuration/ownership integration, bounded read replies and native
qualification remain open. No reply disclosure policy was selected by this
prerequisite and no AIRC result is sent to an IRC room or nickname.

### IRC service integration preflight

On 2026-10-08 Go/module source SHA-256
`860b9267ccb5c9f86ca7c0b9826030ece28a0703cd001e21f1f3764351cbf2db`
passed selected service/config checks and real native AIRC integration. Final
`go test ./internal/gateway/config ./internal/gateway/service -run IRC -count=1`
reported config 0.363 seconds and service 0.583 seconds; race reported
1.345 and 2.253 seconds. Repository build, vet, staticcheck, whole-tree
formatting (empty output) and diff checks passed, terminal exit 0.

`go test ./integration/gateway -run '^TestIRCServiceNative' -count=1
-timeout=300s` passed in 3.707 seconds; race passed in 6.941 seconds.
The scripted verified IRC TCP/TLS peer feeds the actual gateway CLI, real
isolated AIRC server and independent native observer. Exact author/target/whole
Unicode and trailing-space body, stable replay with nickname changes and gateway
restart, later native fences, single-send retained results and joined shutdown
with journal release were verified. This does not qualify a real IRC server/client
or bounded IRC replies. Configuration tests cover JSON parsing, collection bounds,
duplicate bridge IDs/endpoints and refusal of enabled unimplemented reads; file
checks cover private-password permission, missing roots and aliased references.
Startup refusal tests verify bad credential content/trust emits no READY and
releases the journal.

The initial service test incorrectly passed the newline-terminated credential
file as a bearer token and now uses `auth.ReadToken`. The initial CLI fixture
serialized absent listeners as null, which the established parser refuses;
IRC-only configuration uses `listeners: []`. Both corrected fixtures passed
the final checks above. Full unchanged-source acceptance is recorded below.

The first full IRC service gate on this exact source ran from
2026-10-08 08:52:54 UTC to 09:03:56 UTC. Normal acceptance ended after
662.117 seconds with exit 1: 4,038 passes, 53 skips and two failed test
entries (the raw QUIC streams handshake case and its parent), with zero
package build failures. `TestRawQUICOwnerHandshakeAdmissionAndJoinedShutdown/streams`
failed at `quic_test.go:52` with `context deadline exceeded` during its original
three-second initial handshake. The datagram case passed. Race and later
phases did not run. Exact metadata/output remain under
`/tmp/airc-gateway-irc-service-v1-94533a0-*`; terminal exit was 1.

The unchanged-source isolated command
`go test ./internal/gateway/service -run '^TestRawQUICOwnerHandshakeAdmissionAndJoinedShutdown$'
-count=1 -timeout=60s` then passed (0.498 seconds); its race variant also
passed (1.662 seconds), combined terminal exit 0. Neither the QUIC test nor
production deadlines were changed. The full-run failure cause remains unproven;
this isolated pass does not replace full acceptance. The second full gate used
the same source digest, commands and private cache with separate artifacts.

### IRC service integration acceptance

The second full gate ran from 2026-10-08 09:05:17 UTC to 09:45:24 UTC on
unchanged Go/test/module SHA-256
`860b9267ccb5c9f86ca7c0b9826030ece28a0703cd001e21f1f3764351cbf2db`.
The runner verified the sorted tracked path/content NUL-separated digest before
every phase and after all checks. Go was 1.27.1 on Darwin ARM64, using private
cache `/tmp/airc-gateway-framed-fix-cache.YHpAPO`.

| Gate | Terminal exit | Elapsed seconds |
| --- | --- | --- |
| `go test ./... -count=1 -timeout=900s -parallel=3 -p=2 -json` | 0 | 657.740 |
| `go test -race ./... -count=1 -timeout=1800s -p=1 -json` | 0 | 1742.343 |
| `go build ./...` | 0 | 2.459 |
| `gofmt -l .` (empty output) | 0 | 0.407 |
| `go vet ./...` | 0 | 0.888 |
| `staticcheck ./...` | 0 | 2.074 |
| `git diff --check` | 0 | 0.032 |

Both test runs recorded 4,040 passes, 53 skips, zero failed tests and zero
package/build failures. Whole-run terminal exit was 0. Metadata and separate
phase stdout/stderr are retained under
`/tmp/airc-gateway-irc-service-v1-r2-94533a0-*`. These checks establish service
integration and the selected native AIRC delivery fixture, not real IRC
server/client qualification or a bounded read reply path.

### XMPP standalone XML codec prerequisite

On 2026-10-08 Go/test/module SHA-256
`f35ee967dbc1d7ed5714f2c9a448a6a234e5ee1e4dad00d93bb76fdd97a10360`
passed `go test ./internal/gateway/xmppwire -count=1` (0.316 seconds)
and its race variant (1.315 seconds). Repository build, vet, staticcheck,
changed-package formatting (empty output) and diff checks passed; combined
terminal exit was 0. The digest uses sorted tracked Go/module paths and contents,
each NUL-separated, including staged new source files.

Tests preserve namespace identity, complete Unicode/escaped/CDATA text and
trailing whitespace; refuse unqualified/unbound namespaces, duplicate expanded
attributes, malformed XML, DTD/entity directives, comments, processing
instructions, multiple roots, invalid UTF-8/XML characters and byte/depth/token/
attribute exhaustion; and verify whole encoding, round trip and cyclic/oversized
input refusal. The initial encoder test caught forbidden-character replacement
by Go's XML encoder; explicit character validation now refuses that alteration.

This unused codec is not XMPP authentication or service/native acceptance.
Full newer-source acceptance and all remaining XMPP integration/qualification
items remain open; see [the profile](xmpp-bridge.md).

The codec commit review found reserved namespace aliases/default bindings were
not refused by Go's decoder or the initial profile checks. The correction rejects
all bindings of the XMLNS namespace URI, and bindings of the XML namespace URI
except the exact permitted `xmlns:xml` declaration. Refusal vectors cover both
prefixed and default forms for each reserved URI. Focused normal (0.287 seconds)
and race (1.289 seconds) tests, repository build/vet/staticcheck, formatting and
diff checks passed, combined terminal exit 0. See the
[W3C reserved namespace rule](https://www.w3.org/TR/xml-names/#ns-decl).

### XMPP stream framing prerequisite

On 2026-10-08 Go/test/module SHA-256
`8890283fff86af5a1f9c686762aa9471da43e372169da8d7e5ebd8b55e2b7c21`
passed `go test ./internal/gateway/xmppwire -count=1` (0.293 seconds)
and its race variant (1.331 seconds). Repository build, vet, staticcheck,
changed-package formatting (empty output) and diff checks passed; combined
terminal exit was 0. Shared validation keeps existing standalone codec tests.
Stream tests verify inherited namespaces and whole bodies, multiple children,
explicit closure, preservation of buffered bytes across restart with old
namespace removal, truncated input, forbidden directives/bindings, byte and
depth exhaustion, terminal refusal, and XML declaration version/encoding/
standalone restrictions. This private prerequisite does not establish
XMPP authentication, native delivery or real client/server qualification.
Full newer-source acceptance remains required at service integration.

### XMPP canonical address prerequisite

On 2026-10-08 the unused private XMPP address validators passed focused normal
(0.292 seconds) and race (1.395 seconds) codec tests, plus repository build,
vet, staticcheck, formatting and diff checks, combined terminal exit 0.
Cases cover canonical account/room addresses, exact resource preservation,
local/domain label length boundaries, empty/duplicate separators, case aliases,
Unicode names, malformed DNS labels and resource injection. No authentication,
service or native qualification is claimed; remaining integration stays open.

### XMPP provisioning prerequisite

On 2026-10-08 `go test ./internal/gateway/config -run XMPP -count=1`
passed in 0.309 seconds; its race variant passed in 1.287 seconds. Repository
build, vet, staticcheck, changed-file formatting and diff checks passed,
combined terminal exit 0. Tests cover fixed endpoint/TLS/path refusals,
canonical account/resource, exact author-domain distinction, duplicate and
bridge-account grants, revoked/unknown principals, target/operation authority,
cursor-key requirements and exact 128/129-grant and 1/300-second boundaries.
The private types are unused; service, credential ownership, authenticated
membership, bounded replies and native XMPP qualification remain open.

### XMPP authentication wire prerequisite

On 2026-10-08 focused `xmppwire` normal tests passed in 0.304 seconds and race
in 1.316 seconds; repository build, vet, staticcheck, formatting and diff checks
passed, combined terminal exit 0. Tests decode the actual PLAIN wire, verify
stream/header parsing and exact account/resource binding, and refuse absent or
ambiguous PLAIN, wrong namespaces, invalid/oversized credentials, SASL challenge/
failure/extra success content, changed IQ ID/type/sender/account/resource,
duplicate JID and request-ID injection. These private wire helpers are unused;
actual verified TLS/session ownership, membership and native delivery remain
open. No authentication or transport acceptance claim follows from these tests.

### XMPP verified session prerequisite

On 2026-10-08 Go/test/module SHA-256
`7f4fd6e364f1e6621b454268b3975398de599881eef05e4c57f72935a8df7503`
passed focused auth XMPP normal tests in 3.420 seconds and race in 4.445 seconds;
wire normal/race tests passed in 0.139/1.300 seconds. Repository build, vet,
staticcheck, changed-source formatting and diff checks passed; combined terminal
exit was 0. Real TCP/TLS scripted peers exercise registration, SNI, exact binding,
TLS 1.2/unknown-root/SASL-failure/wrong-account refusals, cancellation after
entering blocked TLS and SASL I/O, the original three-second initial deadline,
joined cleanup and credential clearing. The peer is scripted, not real XMPP
server/client qualification. This unused session does not ingest native messages
or expose event proofs. Membership, service integration, bounded replies and
full unchanged-source integration acceptance remain open.

### XMPP MUC wire prerequisite

On 2026-10-08 focused `xmppwire` normal tests passed in 0.272 seconds and race
in 1.330 seconds; repository build, vet, staticcheck, changed-file formatting
and diff checks passed, combined terminal exit 0. Tests parse the actual join
wire and verify historical stanzas are disabled, then exercise exact self and
non-anonymous confirmation and refusal of missing statuses, changed resource,
error presence, visitor/outcast role, wrong real JID, duplicate identity/status,
newly created rooms and wrong namespaces. This is an unused wire prerequisite;
verified-session room ownership, roster/author mapping, loop prevention and
native qualification remain open.

### XMPP verified room join prerequisite

On 2026-10-08 Go/test/module SHA-256
`a12066f1b3a70d90462bd566085296bd8520b7385b5a66d357b5c5e4950103bc`
passed focused auth XMPP normal tests in 6.514 seconds and race in 7.613 seconds.
Repository build, vet, staticcheck, changed-source formatting and diff checks
passed; combined terminal exit was 0. Real TCP/TLS peers verify two provisioned
rooms with duplicate-room grants, exact own confirmation, anonymous/assigned-
nickname/error/early-message refusals, foreign presence waiting until the
unchanged join deadline, 513-stanza exhaustion, closed sockets after failure and
joined fixture cleanup. These are scripted peers, not native XMPP server/client
qualification. Initial roster/author tracking, event admission, service and
bounded reply paths remain open.

The room-owner review found socket deadlines alone did not refuse confirmation
whose buffered parsing completed after expiry. The correction retains the
absolute join deadline, checks it after every decoded stanza and again before
clearing it on success. A real authenticated TCP/TLS fixture delays delivery
from a completed confirmation read past three seconds; its regression verifies
refusal and socket closure. A synthetic stream root supplies the already
established inherited namespace solely to isolate the buffered-read boundary;
this is not additional authentication or native qualification evidence.
Focused XMPP auth normal (9.605 seconds) and race (10.868 seconds), repository
build/vet/staticcheck, formatting and diff checks passed, combined terminal
exit 0. The production three-second budget was not changed.

### XMPP occupant presence wire prerequisite

On 2026-10-08 focused `xmppwire` normal tests passed in 0.304 seconds and race
in 1.313 seconds; repository build, vet, staticcheck, formatting and diff checks
passed, combined terminal exit 0. Tests verify exact full real-JID arrivals and
old-address departure without trust transfer, and refuse missing/noncanonical
real JIDs, wrong rooms/recipients, error presence, duplicate items, anonymous
room transitions and self-presence in the other-occupant parser. These wire
checks are not retained session author proofs or native qualification evidence.

### XMPP verified initial roster prerequisite

On 2026-10-08 focused auth XMPP normal tests passed in 9.655 seconds and race
in 10.982 seconds; repository build, vet, staticcheck, changed-source formatting
and diff checks passed, combined terminal exit 0. A real authenticated TCP/TLS
peer sends canonical real-JID presence before own confirmation; the session
retains that initial authorized identity and confirms the provisioned room.
Serial state tests cover unmapped/bridge-account reassignment clearing old trust,
exact departure, anonymous refusal, the 4,096-entry cap, refresh at capacity and
released capacity. The previous foreign-presence fixture lacked a real JID and
incorrectly carried self status; it now sends valid unmapped presence, preserving
the actual three-second deadline and 513-stanza exhaustion witnesses. Initial
normal acceptance failed that obsolete fixture before correction; final checks
above passed. Post-join event proofs, service/native admission and real XMPP
server/client qualification remain open.

### XMPP room discovery wire prerequisite

On 2026-10-08 focused wire normal tests passed in 0.299 seconds and race in
1.380 seconds; repository build, vet, staticcheck, formatting and diff checks
passed, combined terminal exit 0. Tests parse the actual query and verify exact
room/ID/recipient correlation and selected conference/non-anonymous/stable-ID
features, with error/identity/namespace/correlation/duplicate/contradiction/
malformed-feature refusals. These private wire helpers remain unused by the
verified session and do not qualify message provenance or native delivery.

### XMPP authenticated room discovery prerequisite

On 2026-10-08 focused auth XMPP normal tests passed in 9.797 seconds and race
in 11.138 seconds; repository build, vet, staticcheck, formatting and diff checks
passed, combined terminal exit 0. Actual TCP/TLS peers now answer a freshly
correlated room discovery before every membership request. Valid multi-room
joins and initial rosters passed; wrong ID/room, anonymous mode, missing stable-ID
features and error replies fail with no retained room/roster authority and a
closed session. The existing buffered-input expiry regression now delays the
actual discovery reply, preserving the absolute three-second refusal boundary.
Discovery replies count toward the unchanged shared 512-stanza join budget.
These scripted peers do not qualify a real XMPP server/client or native event
admission. Message provenance, freshness and event proof remain open.

### XMPP whole group-message wire prerequisite

On 2026-10-08 focused wire normal tests passed in 0.286 seconds and race in
1.309 seconds; repository build, vet, staticcheck, formatting and diff checks
passed, combined terminal exit 0. Cases preserve whole Unicode/escaped body and
trailing spaces and opaque case-sensitive room ID, ignore client-origin IDs as
identity, and refuse wrong type/room/recipient/ID authority, empty/duplicate IDs,
multiple bodies, nested body markup and blank text. These private parsed data
are not authenticated event/freshness proofs. Freshness admission remains pending
the owner's signed-envelope versus authoritative archive/server policy choice;
raw XMPP delay timestamps do not establish that property.

### XMPP closed-session room verification guard

On 2026-10-08 focused auth XMPP normal tests passed in 9.985 seconds and
race in 11.476 seconds on the final unchanged executable source. Repository
build, vet, staticcheck, XMPP formatting and diff checks passed. An atomic
closed state and synchronous parent-context check now guard discovery and
room confirmation, including after parsing already-buffered replies. Tests
exercise manual close, parent cancellation, and actual authenticated discovery
bytes delivered after cancellation, with joined fixture cleanup. These private
session prerequisites do not establish original-message freshness, durable
admission, service ownership or real XMPP server/client qualification; those
remain pending. No full acceptance run is claimed for this checkpoint.

### XMPP bounded verified message prerequisite

On 2026-10-08 the final unchanged executable source passed focused auth XMPP
normal tests in 9.904 seconds and race in 11.232 seconds, repository build, vet,
staticcheck, XMPP formatting and diff checks. Actual authenticated TCP/TLS
scripted peers verify current mapped author/room, whole Unicode bodies with
leading/trailing spaces, immutable grants, stable room-scoped IDs across
nickname changes, zero/nil refusal, unmapped replacement and bridge-account
echo refusal, stanza exhaustion, own membership loss and cancellation. The
initial test build used a nonexistent operation constant and was corrected
before these final gates. This private prerequisite does not claim original
event freshness, durable admission, full acceptance or real XMPP server/client
and independent native-observer qualification.

### Matrix identifier prerequisite

On 2026-10-08 private Matrix identifier normal tests passed in 0.286 seconds
and race in 1.269 seconds. Repository build, vet, staticcheck and diff checks
passed; new source was gofmt formatted. Tests cover DNS/IP/port user IDs, byte
limits, opaque legacy/domainless and Unicode IDs, alias rejection and invalid
controls, whitespace, UTF-8, localparts and server names. This unused parser
prerequisite does not claim authentication, service integration, full
acceptance or real Matrix homeserver/client/native-observer qualification.

Review of the identifier checkpoint found that leading zeros could allow a port
longer than Matrix's five-digit grammar. The corrected source adds an explicit
length bound and DNS/IPv6 refusal vectors. Focused normal tests passed in 0.284
seconds and race in 1.271 seconds; repository build, vet, staticcheck, formatting
and diff checks passed again. The original checkpoint's gates did not detect
this syntax defect; the correction preserves all numeric and opaque-ID bounds.

### Matrix provisioning configuration prerequisite

On 2026-10-08 final unchanged private Matrix provisioning source passed focused
configuration normal tests in 0.258 seconds and race in 1.279 seconds, plus
repository build, vet, staticcheck, formatting and diff checks. Tests cover
fixed dial/TLS/path requirements, exact account/author/room grants, loops,
duplicate pairs/operations, missing and revoked principals, denied targets and
operations, cursor requirements, case-distinct rooms, freshness and grant
bounds. An initial staticcheck failure for capitalized new error strings was
corrected before these gates. This unused prerequisite does not claim private
token-file validation, actual authentication, full acceptance or native mode
qualification.

### Matrix private owner preparation prerequisite

On 2026-10-08 focused Matrix auth normal tests passed in 0.367 seconds and
race in 1.359 seconds; repository build, vet, staticcheck and diff checks passed.
New files were gofmt formatted. Tests exercise verifier-owned grants, retained
token clearing, malformed/empty/oversized token refusal and public-file refusal.
No HTTP authentication, full acceptance or real-client qualification is claimed.

### Matrix fixed-endpoint account verification prerequisite

On 2026-10-08 the final unchanged executable source passed focused Matrix auth
normal tests in 3.539 seconds and race in 4.574 seconds. Repository build, vet,
staticcheck, Matrix formatting and diff gates passed. Actual local TCP/TLS HTTP
peers exercise exact path, SNI and header-token use without query parameters;
wrong account, guest, duplicate JSON, oversized response, redirect, denial and
compression refusal; untrusted CA, closed credentials, missing context, blocked
request cancellation and the absolute three-second stalled-response deadline.
Fixture HTTP handlers and servers join cleanup. An initial staticcheck finding
for a literal nil context in the intentional refusal test was corrected before
these gates. These scripted homeserver peers do not qualify real Matrix clients,
room/event intake, full acceptance or native AIRC observation.

### Matrix authenticated joined-room prerequisite

On 2026-10-08 final unchanged executable source passed focused Matrix auth
normal tests in 6.666 seconds and race in 7.931 seconds, plus repository build,
vet, staticcheck and diff checks. New and changed files were gofmt formatted.
Actual TLS HTTP fixtures check account-before-room ordering and unchanged
header-token/query boundaries, exact joined room, missing/case/alias/duplicate
and null refusal, 256/257 room bounds and account refusal before room contact.
Two deliberately slow requests verify one shared absolute deadline rather than
renewed request budgets. Prior HTTP boundary tests run in the same selection.
This private check has no service or event intake caller; full acceptance,
real Matrix homeserver/client and independent native qualification remain open.

### Matrix whole plain-event wire prerequisite

On 2026-10-08 focused Matrix wire normal tests passed in 0.315 seconds and
race in 1.265 seconds; repository build, vet, staticcheck and diff checks passed.
New files were gofmt formatted. Tests preserve whole Unicode/escaped bodies,
spaces and newlines, exact opaque IDs and explicit/implicit room context; they
refuse room mismatch/alias, bad sender/ID, malformed timestamps, encrypted,
notice, state, redaction, edit and relational events, null metadata, blank/NUL
and nested bodies, duplicate JSON, invalid Unicode, trailing data and oversize
input. Parsed metadata is not a proof. No authenticated event reader, service,
full acceptance or real Matrix/native-observer qualification is claimed.

### Matrix verified exact-event proof prerequisite

On 2026-10-08 final unchanged executable source passed focused Matrix auth
normal tests in 6.781 seconds and race in 8.245 seconds. Repository build, vet,
staticcheck, Matrix formatting and diff checks passed. Actual authenticated TLS
HTTP peers cover account/membership/event retrieval, exact escaped Unicode and
slash-bearing IDs, whole Unicode bodies, immutable grants, mapped native target,
network-scoped global event identity, zero proof, unprovisioned room before
contact, unmapped/bridge-account refusal, wrong/missing room, wrong ID, redaction
and cancellation during event retrieval with joined fixture cleanup. Prior
account and room boundaries run in the same selection. Initial staticcheck
failed on a capitalized new error string; it was corrected before final gates.
This prerequisite has no timeline poller, journal or service consumer and does
not claim freshness, full acceptance or real Matrix/native qualification.

### Matrix timeline wire prerequisite

On 2026-10-08 the final unchanged executable source passed shared JSON normal
tests in 0.206 seconds/race in 1.172 seconds and Matrix wire normal in 0.342
seconds/race in 1.451 seconds. Repository build, vet, staticcheck, formatting
and diff checks passed. Tests exercise opaque cursors, exact ordered message
references, explicit limited-history gaps/departures, empty pages, 256/257 room
and event limits, nested sync metadata, alias/mismatch/duplicate/null refusal,
missing gap tokens, oversized input and ambiguous membership. Shared JSON tests
retain the eight-level default and enforce explicit 16-level/17-level boundaries.
This parser has no authenticated poller or cursor persistence consumer; no full
acceptance, durable progression or real Matrix/native qualification is claimed.

### Matrix authenticated timeline polling prerequisite

On 2026-10-08 final unchanged executable source passed focused Matrix auth
normal tests in 6.925 seconds and race in 8.595 seconds, repository build, vet,
staticcheck, Matrix formatting and diff checks. Actual TLS HTTP peers check
account/membership/sync ownership, canonical fixed room filtering and escaped
Unicode/slash/ampersand cursor values without query injection, immutable pages
and loss diagnostics, gap/departure refusal with no usable cursor, unprovisioned
references and nonprogressing event pages, empty initial/idle pages and blocked
sync cancellation with joined cleanup. All prior Matrix auth boundaries remain
in the selected checks. This private poller has no cursor persistence, journal
or service consumer; no full acceptance or real Matrix/native qualification is
claimed.

### Matrix originating-event freshness prerequisite

On 2026-10-08 final executable source passed focused Matrix auth normal tests
in 6.861 seconds and race tests in 8.839 seconds. Repository build, vet,
staticcheck, affected Go formatting and diff checks passed. Actual authenticated
TLS event retrieval refuses stale and future origin timestamps. Verified proofs
retain the original timestamp plus configured freshness as a fixed replay
deadline. Content comparisons cover exact body, author, room, timestamp and
native target changes while preserving global event identity; zero proofs
supply no freshness or fingerprint authority. The configured homeserver is the
timestamp trust boundary, including imported/application-service events. These
checks do not establish durable admission, full repository acceptance or real
Matrix/native qualification.

### Matrix journal schema 17 acceptance

The staged executable/test source digest is
`8f6e0ce0d80ac90254de24a019bee774123581258308677ee398024c7ea82d4c`.
Focused store/dispatch normal suites passed in 14.729/0.822 seconds; their
Matrix/migration race suites passed in 13.813/2.045 seconds. A later migration
fixture additionally checks preservation of an existing IRC replay binding and
passed its focused normal test in 0.432 seconds. The first run of that added
fixture incorrectly used a short generic request ID in the 64-character IRC
replay table; it failed its constraint before full acceptance started. The
fixture now uses a valid fixed 64-character replay ID.

Full normal acceptance passed with exit 0 in 669.918 seconds, with 4,258 pass
entries, 63 skips and no test/package/build failures. Command:
`go test ./... -count=1 -timeout=900s -parallel=3 -p=2 -json`.
The same frozen-source race run passed in 1,789.944 seconds, also with 4,258
pass entries, 63 skips and no test/package/build failures. Command:
`go test -race ./... -count=1 -timeout=1800s -p=1 -json`.
Build, whole-repository formatting, vet, staticcheck and diff gates all exited 0.
The runner checked source equality before each phase and at completion, then
returned terminal exit 0. UTC interval: 2026-10-08 11:33:45.873696 through
12:14:55.621386. Go 1.27.1, Darwin ARM64; shared existing cache
`/tmp/airc-gateway-framed-fix-cache.YHpAPO`. Runner and artifacts are
`/tmp/airc-gateway-matrix-journal-v17-acceptance-runner.py` and
`/tmp/airc-gateway-matrix-journal-v17-9f1206b-*`. These checks do not establish
Matrix service integration or real homeserver/client/native qualification.

### Matrix history wire prerequisite

On 2026-10-08 final executable source passed Matrix wire normal tests in 0.251
seconds and race tests in 1.353 seconds. Repository build, vet, staticcheck,
affected Go formatting and diff gates passed. Tests check exact opaque cursors,
server event order, empty continued/terminal pages, exact room binding, global
event duplication, missing/null/invalid fields, duplicate JSON keys, trailing
data, oversize input and 256/257-event limits. This private unused parser does
not establish authenticated recovery, durable cursor progression, full
repository acceptance or real Matrix/native qualification.

### Matrix authenticated history retrieval prerequisite

On 2026-10-08 final executable source passed focused Matrix auth normal tests
in 6.977 seconds and race tests in 8.862 seconds. Repository build, vet,
staticcheck, affected Go formatting and diff checks passed. Actual TLS fixtures
verify exact opaque cursors and request query, initial/continued/terminal pages,
changed start/room and nonprogressing-page refusal, immutable references, unknown
room refusal before contact, zero proofs, unsafe query refusal, joined request
cancellation and closed-owner refusal. Existing Matrix auth boundaries remain
covered. This private unused reader does not establish durable recovery, service
integration, full repository acceptance or real homeserver/client/native
qualification.

### Darwin System V queue prerequisite checks

On 2026-10-08 final executable source passed the native queue package normal
tests in 0.506 seconds and race tests in 2.445 seconds, plus cgo-disabled normal
tests in 0.446 seconds. Repository build, vet, staticcheck, affected Go formatting
and diff gates passed. Real kernel queues exercise exclusive create, metadata
ABI, verified peer opening, whole packets, short/oversize refusal without
consumption, full/empty deadlines, cancellation, concurrent close joining,
permission-change refusal, removed-identifier replacement refusal and cleanup.
A real separate Go child producer sends a whole 1,000-byte message; race runs
retain child instrumentation. No live AIRC endpoint or host IPC setting is used.

The Linux ARM64 queue tests were cross-compiled with cgo disabled and passed
with terminal exit 0 in cached Ubuntu image
`sha256:33ceb71981b602c1a7443a53469e4dba065f7503eab3078a2d7a57a2ab987517`,
using no network, read-only root/artifact mount, dropped capabilities and
no-new-privileges. The same actual-kernel whole-packet, pressure, cancellation,
close, permissions, replacement and separate-child tests run on both platforms.
Linux race instrumentation is not verified by this cgo-disabled fixture.
The private layer has no service caller; these checks do not establish full
repository acceptance, authenticated application intake, durable crash recovery
or native AIRC delivery.

The shared Darwin/Linux ARM64 queue source passed final Darwin normal tests in
0.480 seconds, race tests in 2.469 seconds and cgo-disabled tests in 0.455
seconds on 2026-10-08. Repository build, vet, staticcheck, affected formatting
and diff checks passed with terminal exit 0. This replaces the earlier
Linux unsupported-only prerequisite evidence; full service acceptance remains open.

The System V packet prerequisite also exercises the shared POSIX packet deadline
and shutdown code. Final affected Darwin normal/race tests passed in
0.841/1.941 seconds. The Linux ARM64 adapter binary ran
`-test.run='Test(SystemVQueue|POSIXQueuePackets)' -test.v -test.count=1`
in the same network-disabled, read-only, capability-free cached Ubuntu fixture;
all selected actual-kernel tests passed with terminal exit 0. Repository build,
vet, staticcheck, affected formatting and diff gates passed. Full service and
native AIRC acceptance remain open; Linux race execution is not claimed.

The System V recovery prerequisite passed final Darwin normal/race/cgo-disabled
queue tests in 0.610/2.590/0.645 seconds; epoch tests passed in
0.177/1.177/0.241 seconds. The actual Linux ARM64 queue binary passed all its
selected native tests, including preserved whole-packet recovery after killing
a real child owner. The adapter binary also passed
`-test.run='Test(POSIXQueueRecovery|SystemVQueuePackets|POSIXQueuePackets)'`
in the cached isolated Ubuntu fixture. Build, vet, staticcheck, affected
formatting and diff gates passed with terminal exit 0. These checks cover
private kernel recovery and existing POSIX recovery; durable ownership and
service/native AIRC integration remain unqualified.

Schema 18 System V ownership affected tests passed normal/race in
1.333/7.632 seconds, including migration and existing POSIX ownership controls.
Repository build, vet, staticcheck, affected formatting and diff checks passed.
Fresh full normal/race acceptance passed on the frozen source as recorded below.

### System V ownership schema 18 acceptance

Full normal/race acceptance and required static gates passed on 2026-10-08 at
frozen Go/test/assembly/module source SHA256
`f3985dc5091ec7161f65d67ff96183c622bee5f6ea2a553d93c9939a5e258a93`.
The digest includes tracked `.s` assembly as well as Go and module files. Source
matched before every phase and at completion. The run used Go 1.27.1 Darwin ARM64
and the existing cache `/tmp/airc-gateway-framed-fix-cache.YHpAPO`.

Normal `go test ./... -count=1 -timeout=900s -parallel=3 -p=2 -json` passed
with exit 0 in 665.218 seconds. Race
`go test -race ./... -count=1 -timeout=1800s -p=1 -json` passed with exit 0
in 1,816.612 seconds. Each recorded 4,293 pass entries and 65 helper/opt-in/platform
skips, no test/package/build failures, no malformed JSON lines and no data-race
warnings. The timeout applies per package; the race suite's total elapsed time
is not a package timeout. Build, whole-repository formatting, vet, staticcheck
and diff gates each exited 0. The runner's terminal exit was 0.

Metadata, exact commands, per-phase elapsed times, source patch and normal/race
JSON streams are under
`/tmp/airc-gateway-system-v-ownership-v18-d40a1bc-20261008T130842-*`.
The frozen run started at `2026-10-08T13:08:42.332331+00:00` and finished at
`2026-10-08T13:50:13.449573+00:00`. Runner:
`/tmp/airc-gateway-system-v-ownership-v18-acceptance-runner.py`.

This checkpoint adds bounded durable System V ownership and additive migration
from schema 17. It also certifies the current repository source containing the
private queue, packet and kernel recovery layers; actual Linux kernel evidence
remains separately recorded without Linux race instrumentation. No configured
System V listener, authenticated System V/native AIRC delivery or service-level
crash/replay qualification is established by these passes. Those tasks remain
open in the roadmap.

### Journal-scoped System V ingress prerequisite

The private ingress helper composes exclusive queue creation with schema 18
ownership registration, exact kernel-epoch restart recovery and guarded record
replacement. Unknown queues and changed queue lifecycles are refused without
removal. A real SQLite trigger failure confirms that failed registration removes
only the newly created ingress; cancellation before creation leaves no queue.
Reopening the actual journal preserves a queued whole 1000-byte packet and
recovers its recorded queue identity.

Affected Darwin ARM64 normal/race tests passed in 0.594/2.816 seconds. The
cgo-disabled Linux ARM64 service test binary passed all six selected
`TestSystemVIngress` tests in the cached Ubuntu fixture with network disabled,
read-only root and executable, all capabilities dropped and no-new-privileges.
Its temporary journal used an automatically removed anonymous local Docker
volume: tmpfs was rejected by the existing journal filesystem policy. Build,
vet, staticcheck, affected formatting and diff checks passed with terminal exit 0.
Linux race execution is not claimed.

This unused helper does not activate configured listeners, authenticated native
AIRC delivery or service-process crash/replay qualification. Those remain open.
The preceding full acceptance hash covers the schema 18 checkpoint, before this
helper was added; it is not full acceptance of these new tests or helper.

### System V configuration prerequisite

The unused private validator requires canonical positive decimal int32 keys,
distinct ingress keys, responses outside every configured ingress key, fixed
protected per-principal routes and ingress/response credentials. It refuses
POSIX depth, TLS and browser origins, and enabled unsupported platforms. Public
`Config.Validate` still refuses enabled System V listeners until service wiring
is implemented. Tests cover canonical boundary keys, malformed spellings,
route collisions and missing or ambiguous protection.

Affected configuration normal/race tests passed in 2.459/2.723 seconds on Darwin
ARM64; build, vet, staticcheck, affected formatting and diff checks passed with
terminal exit 0. The Linux ARM64 cgo-disabled configuration binary also passed
all selected `TestSystemVQueue` cases in the cached isolated Ubuntu fixture
before the final error-message capitalization correction; logic was unchanged.
No Linux race or service activation acceptance is claimed.

### System V packet-owner prerequisite

The private owner builds the existing protected packet driver, parses configured
response keys, verifies all provisioned response queues before creating ingress,
and transfers queue handles to the joined packet shutdown implementation. Actual
kernel tests confirm that a missing response creates neither ingress nor an
ownership record, whole ingress/response fragments retain their fixed routing,
shutdown ends a blocked read, owned ingress is removed and the external response
queue retains its exact lifecycle.

Selected System V configuration/ingress/owner tests passed normal/race on Darwin
ARM64: configuration 0.345/1.384 seconds and service 0.663/3.772 seconds. Existing
Unix datagram owner tests passed normal/race in 0.373/1.487 seconds. Full affected
configuration tests passed normal/race in 0.560/2.377 seconds. The cgo-disabled
Linux ARM64 service binary passed all selected System V ingress/owner and
existing POSIX owner tests in the cached isolated Ubuntu fixture with a local
anonymous journal volume. Build, vet, staticcheck, affected formatting and diff
checks passed with terminal exit 0. Linux race execution is not claimed.

Public configuration and Run do not yet activate the owner. These checks do not
establish authenticated AIRC delivery, protected whole reads or service-process
crash/replay qualification; those remain open in the roadmap.

### System V service activation acceptance

Public configuration accepts `system_v_queue` on Darwin/Linux ARM64 and Run
uses the verified packet owner. Configuration tests reject malformed keys,
ingress/response collisions, missing protection, ambiguous routes and unsupported
platforms through public validation. Actual service tests confirm absent response
queues prevent readiness and ingress creation; two complete startup/shutdown
cycles remove owned ingress, retain the response lifecycle and release journal
ownership for reopening. Final selected Darwin service normal/race tests passed
in 0.711/4.288 seconds. The cgo-disabled Linux ARM64 service binary passed all
nine selected `TestSystemV` tests in the cached isolated Ubuntu fixture with an
automatically removed local journal volume. Linux race execution is not claimed.

Fresh full normal/race acceptance and required static gates passed on 2026-10-08
at frozen Go/test/assembly/module source SHA256
`c4b7fff462a61d231295dc7d905584c3bca70253311718c72a8cf349513ee436`.
The source digest matched before every phase, at completion and on independent
verification after terminal exit. Go 1.27.1 Darwin ARM64 used existing cache
`/tmp/airc-gateway-framed-fix-cache.YHpAPO`.

Normal `go test ./... -count=1 -timeout=900s -parallel=3 -p=2 -json` passed
with exit 0 in 666.122 seconds. Race
`go test -race ./... -count=1 -timeout=1800s -p=1 -json` passed with exit 0
in 1,779.030 seconds. Each recorded 4,321 pass entries and 65 helper/opt-in/platform
skips, no test/package/build failures, no malformed JSON and no data-race warnings.
Build, whole-repository formatting, vet, staticcheck and diff gates each exited 0.
The runner's terminal exit was 0.

Metadata, source patch, exact commands and full JSON streams are under
`/tmp/airc-gateway-system-v-service-activation-e8fa062-20261008T141705-*`.
The run started at `2026-10-08T14:17:05.388571+00:00` and finished at
`2026-10-08T14:57:57.781264+00:00`. Runner:
`/tmp/airc-gateway-system-v-service-activation-acceptance-runner.py`.

This checkpoint establishes configuration and service lifecycle integration.
It does not establish authenticated native AIRC delivery, complete protected
reads or killed-service crash/replay qualification. Those remain open in the
roadmap; the Linux kernel/owner/startup checks supply their own narrower evidence.

### System V native watch and Darwin permission acceptance

The actual whole-history/live-watch fixture exposed XNU's transient `MSG_LOCKED`
mode bit during concurrent queue I/O. Darwin now checks only the low nine access
permission bits for exact `0600`; UID, creator, key, capacity and lifecycle checks
remain intact. The actual failing mode was `01600`, whose access permissions
were still `0600`. Native queue normal/race controls passed in 0.484/2.488 seconds;
two whole-watch normal runs passed in 6.943 seconds and race in 10.065 seconds.
Actual Linux ARM64 whole-watch qualification passed in 0.28 seconds, using the
unchanged Linux implementation and checksum-verified native/gateway fixtures.

Fresh full acceptance passed at frozen Go/test/assembly/module source SHA256
`b5c7bf727399566e8fea67bbd4e6e18e7217a260e0b02120e4f8a34bd0d482b8`.
Go 1.27.1 Darwin ARM64 used existing cache
`/tmp/airc-gateway-framed-fix-cache.YHpAPO`. The exact normal command
`go test ./... -count=1 -timeout=900s -parallel=3 -p=2 -json` passed in
663.470 seconds; `go test -race ./... -count=1 -timeout=1800s -p=1 -json`
passed in 1,763.313 seconds. Each recorded 4,332 pass entries and 65 skips, with
no test/package/build failures, malformed JSON or data-race warnings; stderr
was empty. Build, whole-repository formatting, vet, staticcheck and diff checks
all exited 0. The runner's observed terminal exit was 0. The source digest matched
before every phase, at completion and on independent terminal verification.

Metadata, exact source patch, commands and JSON streams are under
`/tmp/airc-gateway-system-v-darwin-lock-bit-857402f-20261008T151141-*`; runner:
`/tmp/airc-gateway-system-v-darwin-lock-bit-acceptance-runner.py`. UTC interval:
`2026-10-08T15:11:42.054167+00:00` to `2026-10-08T15:52:18.421987+00:00`.
This includes native send/cursor/replay, refusal and whole-watch tests; it does
not complete queue pressure, replacement or killed-service recovery qualification.
Linux race execution is not claimed.

### System V native qualification acceptance

Fresh full normal/race and required static acceptance passed on reviewed revision
`7721a3fd3692815a78a525f40a112b3421a5aeb1`, frozen Go/test/assembly/module SHA256
`c391b10e09e1ba6d4d5201c2eb323ed3573fd38416aca811556a2cc1d15b434c`.
Go 1.27.1 Darwin ARM64 used existing cache
`/tmp/airc-gateway-framed-fix-cache.YHpAPO`. Normal
`go test ./... -count=1 -timeout=900s -parallel=3 -p=2 -json` passed in
665.250 seconds; race
`go test -race ./... -count=1 -timeout=1800s -p=1 -json` passed in
1,747.036 seconds. Each recorded 4,337 pass entries and 65 skips with no
test/package/build failures, malformed JSON or race warnings; stderr was empty.
Build, whole-repository formatting, vet, staticcheck and diff gates exited 0.
The observed runner terminal exit was 0 and independent terminal source digest
matched all phase guards. UTC interval: `2026-10-08T16:15:02.243059+00:00` to
`2026-10-08T16:55:20.433389+00:00`. Commands, metadata, source patch and streams:
`/tmp/airc-gateway-system-v-native-qualification-7721a3f-20261008T161502-*`;
runner `/tmp/airc-gateway-system-v-native-qualification-acceptance-runner.py`.

The integrated Linux ARM64 native binary passed all eight actual System V
qualification tests: accepted crash replay (0.52 s), eight refusal subcases
(1.31 s), uncertain dequeue retry (0.34 s), Unicode/cursor/replay (0.56 s),
pending-native recovery (0.31 s), pressure (4.15 s), replacement (0.06 s), and
whole history/watch shutdown (0.19 s). Digest-verified existing gateway/native
artifacts ran in the cached isolated Ubuntu fixture with network disabled,
read-only artifacts/root and automatically removed local journal volume.
Output/provenance: `/tmp/airc-system-v-native-93a280d/integrated.log` and
`integrated-provenance.json`. Linux race and additional architectures are not
claimed. [The specification](system-v-queue.md) records exact supported boundaries.

## IPv4 multicast service activation acceptance

Full acceptance on base `80448fd93acde1ddae3c207489b2be8001003d2b` plus
the retained activation patch passed on Go 1.27.1, Darwin ARM64. The sorted
Go/test/assembly/module source SHA256 was
`40d2ff20574764db50d89eea9ae3887b5fc6ca63ab4dc868c7067e38398d03af`;
it remained unchanged before each phase and after terminal completion.
The warm compilation cache was `/tmp/airc-gateway-framed-fix-cache.YHpAPO`;
test-result caching was disabled. The run began at
2026-10-08T17:20:24.703779Z and ended at 18:02:35.057999Z.

`go test ./... -count=1 -timeout=900s -parallel=3 -p=2 -json` passed in
682.035s. `go test -race ./... -count=1 -timeout=1800s -p=1 -json` passed
in 1835.917s. Each reported 4,366 pass entries and 65 helper/opt-in/platform
skips, with no failure/build-failure events, malformed JSON or race warnings;
both stderr files were empty. Counts include package outcomes. These are
acceptance measurements, not a controlled speed comparison.

`go build ./...` (2.574s), `gofmt -l .` (0.451s, empty output),
`go vet ./...` (2.139s), `staticcheck ./...` (6.027s) and `git diff --check`
(0.019s) all returned zero, followed by runner terminal exit zero.
Raw stdout/stderr, metadata, command arguments and the source patch are retained
under `/tmp/airc-gateway-ipv4-multicast-activation-80448fd-20261008T172024`.

The [multicast specification](multicast-ipv4.md#service-activation-acceptance)
records actual Darwin/Linux ARM64 startup, encrypted fixed-mailbox capabilities
and joined ownership evidence. This activates the service without claiming
multicast native message delivery, whole history/cursor replies, replay or
pressure qualification. Those remain on the roadmap. Existing carrier tests
continue to pass; the skips do not qualify untested platforms or physical paths.

## IPv4 multicast native qualification acceptance

Full acceptance on committed source `79149e23859872b896fd865de2b0e000c214538d`
passed on Go 1.27.1, Darwin ARM64. The sorted tracked Go/test/assembly/module
source SHA256 was
`f44ed61d390e975fa3835108d1966385028ac765933201337a347cead7f7c337`,
unchanged before every phase and independently verified after terminal exit
zero. The source patch was empty. The warm cache was
`/tmp/airc-gateway-framed-fix-cache.YHpAPO`; test-result caching was disabled.
The run began 2026-10-08T18:20:26.724576Z and ended 19:01:43.183754Z.

`go test ./... -count=1 -timeout=900s -parallel=3 -p=2 -json` passed in
675.092s; `go test -race ./... -count=1 -timeout=1800s -p=1 -json` passed
in 1793.995s. Each recorded 4,381 pass entries and 65 helper/opt-in/platform
skips, including package outcomes. Neither contained failure/build-failure
events, malformed JSON or race warnings; both stderr files were empty.
These timings are acceptance measurements, not a controlled speed comparison.

`go build ./...` (2.681s), `gofmt -l .` (0.465s, empty output),
`go vet ./...` (0.965s), `staticcheck ./...` (2.045s) and `git diff --check`
(0.032s) all returned zero. Raw output, metadata and command provenance are
under `/tmp/airc-gateway-ipv4-multicast-qualification-79149e2-20261008T182026`.

[The specification](multicast-ipv4.md#integrated-native-qualification) ties
this source gate to actual native Darwin/Linux ARM64 multicast send, cursor,
replay, refusal, fixed reply routing, capacity and shutdown evidence. Linux
race, other architectures, routed multicast, IPv6 multicast and broadcast
are not qualified by this checkpoint.

## UDP-Lite activation acceptance: AMQP replay correction

The first activation acceptance run on `f449623` plus the pending activation
source exited 1 in the normal phase after 679.437 seconds. Its private logs,
metadata and complete tracked/untracked Go source snapshot are under
`/tmp/airc-gateway-udp-lite-activation-f449623-20261008T193329`. Race, build and
static phases did not run; this attempt does not qualify service activation.

`TestAMQPListenerDurableDispatchSignedResponseAndSettlement` regenerated signed
request timestamps for its identical replay. Crossing a second boundary changes
the durable request fingerprint and correctly produces a conflict rejection.
The correction reuses the exact signed bytes for the replay on a new wire
delivery, while retaining a separately signed changed-message conflict case.
The corrected socket test passed 20 normal repetitions (1.211 seconds) and ten
race repetitions (4.127 seconds); adapter vet, staticcheck and diff checks passed.
Full activation acceptance must be rerun after this correction.

## UDP-Lite service activation acceptance

After the AMQP replay correction, the frozen source at `2078cde` plus pending
activation changes passed all phases on Go 1.27.1 Darwin ARM64. The source digest
was `8fb90d170d67bd0a674d626679caf007607e07b2f3c2ed547b9b772239463b7e`,
covering sorted tracked and nonignored untracked Go/assembly/module paths and
contents, each NUL-separated. This includes the pending IPv6/broadcast socket
files; those modes are not qualified by their presence in this acceptance.
The complete source snapshot, patch, metadata and phase logs are under
`/tmp/airc-gateway-udp-lite-activation-2078cde-20261008T194617`.

Normal acceptance took 663.184 seconds and race acceptance 1817.947 seconds.
Each recorded 4,468 passing test/package events and 68 skips, with zero failures,
build failures, malformed events or race warnings and empty stderr. Build,
formatting, vet, staticcheck and diff checks all exited zero. The source digest
was independently recomputed after terminal exit zero and matched. Actual Linux
startup/configuration evidence is in [UDP-Lite](udp-lite.md); Linux race and
complete native mode qualification are not claimed.

## UDP-Lite qualification acceptance: IRC receipt boundary correction

The first final qualification run on `746f677` completed normal acceptance
(exit zero, 654.893 seconds) but failed race acceptance (1765.269 seconds).
`TestIRCServiceNativeWholeSendReplayAndJoinedShutdown` interrupted the gateway
after native observation, before its accepted receipt had necessarily reached
the journal; the fence job remained durable in `retry_wait`. No data-race warning
was reported. Build/static phases did not run. This attempt does not qualify
UDP-Lite; logs, metadata and source snapshot remain under
`/tmp/airc-gateway-udp-lite-qualification-746f677-20261008T210317`.

The corrected IRC control uses a canonical private receipt-only Unix listener
and existing length-prefixed authenticated framing to wait up to five seconds
for durable accepted receipts, exact native message IDs and one send attempt
before SIGINT. Post-shutdown journal and retained-replay assertions remain.
Concurrent read-only SQLite polling was removed after race tests exposed
locking errors. The final control passed five normal repetitions (7.044 seconds)
and five race repetitions (21.120 seconds), integration vet, staticcheck and
diff checks. Full qualification acceptance must be rerun after this correction.

The review of `608b4dc` found that shared frame helpers reset their deadlines,
so the five-second bound was not absolute. The correction caps both helper
deadlines at the receipt control's original deadline and rejects responses
arriving after it. Five normal repetitions passed (6.065 seconds), five race
repetitions passed (19.784 seconds), and integration vet/staticcheck/diff passed.
The newly started qualification run under
`/tmp/airc-gateway-udp-lite-qualification-608b4dc-20261008T214942` was deliberately
stopped with SIGTERM (exit 143) for this review correction; it supplies no
acceptance claim. Full qualification acceptance still requires a fresh run.

## IPv6 multicast, SSH and browser checkpoint: syslog receipt admission

Full normal acceptance on clean source `3359263` exited 1 after 678.46 seconds
on Go 1.27.1 Darwin ARM64. The sole failing test was
`TestSyslogNativeSharedSocketAdmissionAndJoinedShutdown`: after closing its
sender, its HTTPS receipt control received EOF while the server could still own
the single shared socket slot. The other test/package pass events totalled
4,495; 72 helper, platform and opt-in events skipped. Race acceptance was not
started. This failed checkpoint does not establish full acceptance of the newly
qualified modes. Output is under `/tmp/airc-roadmap-3359263/normal.jsonl` and
`normal.stderr`; compilation caches were warm and test-result caching disabled.

Thirty focused repetitions of the unchanged test passed, so the failure was
intermittent. The receipt helper now tolerates only pre-response EOF or reset
while the slot is released, with one absolute five-second context covering all
HTTP attempts and receipt checks. Authentication, response decoding, exact
native message ID, one send attempt, durability and joined shutdown remain
required; production admission and deadlines are unchanged.

The correction passed 30 normal repetitions (11.627 seconds), 20 race
repetitions (36.285 seconds), and the syslog/signed-syslog integration family
under race detection (44.831 seconds). Integration vet, staticcheck, formatting
and diff checks passed. Their raw output is in the same temporary directory.
Full normal/race acceptance still requires a fresh run after integration.

## UART and accumulated connection qualification checkpoint

On 2026-10-09, source `749f3c9` plus the UART changes passed the full normal
suite in 675.957 seconds and the full race suite in 1818.557 seconds on Go
1.27.1 Darwin ARM64. The normal command was
`go test ./... -count=1 -timeout=900s -parallel=3 -p=2 -json`; race used
`go test -race ./... -count=1 -timeout=1800s -p=1 -json`. Build, formatting,
vet, staticcheck and diff checks all exited zero. The coordinator finished
with terminal exit zero at 04:21:00 UTC.

Each suite recorded 4,510 passing test/package events and 74 skips: 42 packages
passed and 11 had no runnable tests. Neither run contained failed or malformed
events, race warnings or stderr output. This checkpoint also covers the
previously committed IPv6 multicast, SSH forwarding, trusted WebRTC browser,
SOCKS/HTTP CONNECT and syslog receipt-helper changes. It does not qualify
unavailable platforms or external paths by implication.

The unchanged Go/assembly/module source digest was
`32a0a05f2a16070d6391192a77864ccf380a720027a31a572234830392337277`,
independently recomputed after terminal completion. The complete snapshot hash
was `6e1e14e01c102aba4347d39611e593a699a6812861c17bff449df334bde80cd5`.
Metadata, source snapshot and phase logs remain under
`/tmp/airc-gateway-uart-checkpoint-749f3c9-20261009T033917`.
Compilation caches were warm; test-result caching was disabled. These timings
are acceptance evidence, not a speed comparison. Actual Darwin/Linux PTY
qualification and physical-link limits are recorded in [UART](uart.md).

## Shared-memory checkpoint: gRPC observer polling correction

The first shared-memory full normal run on `8e3db2a` plus pending changes
terminated with exit 1 after 683.619 seconds on 2026-10-09. The sole failing
test was `TestGRPCSlowReaderWriteDeadlineReleasesNativeWatch`: its generic
four-second observer wait expired after actual HTTP/2 pressure at 16 native
posts. The suite recorded 4,566 passing test/package events and 74 skips, with
no malformed events or stderr output. Race and later gates were not started.
This failed run does not qualify shared memory.

Source digest was
`9d3ea42020dfa7e28890e6697351c8cc955f7b6a6cda971d7b4c458e8a0b3e23`;
snapshot, metadata and logs remain under
`/tmp/airc-gateway-shared-memory-checkpoint-8e3db2a-20261009T043200`.
The [gRPC polling correction](slow-reader-testing.md#grpc-observer-polling-boundary-correction)
uses the test's existing eight-second overall bound and preserves production
deadlines. Focused normal/race repetitions and integration static checks passed;
full acceptance must be rerun after this correction.

## Shared-memory acceptance checkpoint

On 2026-10-09, `0607fa7` plus the shared-memory implementation passed full normal
acceptance in 678.893 seconds and full race acceptance in 1849.889 seconds on Go
1.27.1 Darwin ARM64. Commands were
`go test ./... -count=1 -timeout=900s -parallel=3 -p=2 -json` and
`go test -race ./... -count=1 -timeout=1800s -p=1 -json`.
Build, formatting, vet, staticcheck and diff checks all exited zero. The runner
terminated with exit zero at 05:29:19 UTC.

Both suites recorded 4,525 passing tests, 43 passing packages and 74 skipped
test/package events, including 11 packages without runnable tests. There were
no failures, malformed events, race warnings or stderr output. The corrected
gRPC slow-reader check passed in both suites; the earlier failed attempt and
its focused diagnosis remain recorded above.

The source digest was
`1fc6f4fceca27eb6b1e2dfe822032a42994480cc261d807d249c2736c8a49c53`,
covering sorted tracked and nonignored untracked Go, assembly, C, header and
module paths and contents, each NUL-separated. The source archive hash was
`8738c4a395ad25f8d03125a4de86674505057d6df30555a1902acedace51b030`.
Both hashes were independently recomputed after terminal completion and matched.
Source snapshot, metadata, phase logs and verified summary remain under
`/tmp/airc-gateway-shared-memory-checkpoint-0607fa7-20261009T044700`.

Compilation caches were warm and test-result caching was disabled. These are
acceptance timings, not a speed comparison. Actual Darwin/Linux native mode
qualification and its process-crash/cooperative-peer limits are recorded in
[shared memory](shared-memory.md#native-qualification); no Linux race or reboot
durability claim follows from the host full suite.

## Mapped-file checkpoint stopped for alias-validation fixes

The mapped-file queue and refactored shared-memory queue passed their native
Darwin ARM64 race family (35.996 seconds) and the actual Linux ARM64 family
(5.670 seconds including container startup). Their source SHA-256 is
`2247d1ad25b094b4d0ff39e51c6fd50b0ec1d96197a356d77db501ca8071b339`.
The native contract, cases, platform limits and logs are recorded in
[mapped-file.md](mapped-file.md#native-qualification).

Full normal acceptance of that same source passed on 2026-10-09 in 702.562
seconds using `go test ./... -count=1 -timeout=900s -parallel=3 -p=2 -json`.
It emitted 4636 pass events and 74 skips, with no failures, malformed events
or stderr output. Full race acceptance was deliberately interrupted with exit 130 after an
independent reproducer exposed a path-exemption bug. Queue-owned paths could
exempt a different connector reference to the same resolved object. A second
reproducer exposed case-aliased ancestor containment on APFS. These are
configuration defects found by review; the normal suite did not cover them.
This stopped checkpoint does not certify the corrected source.

The retained source snapshot SHA-256 is
`712b6ba54aeebc2f9a460dbaf2e66b74bd15b2267f5613bd6bd5f574cc182eec`.
Commands, phase logs, exact source archive and terminal metadata use prefix
`/tmp/airc-gateway-mapped-file-checkpoint-2afddf5-20261009T054122`.

A second checkpoint at source SHA-256
`b2d7e2258b0cf8126cd9f6980bf934d8f8a64f8e7fc28c524c3b7228fbea30a1`
passed full normal acceptance in 690.872 seconds. Its race phase was also
interrupted with exit 130 after review reproduced a configured file-response
hard link to a queue file outside the queue directory. Runtime already refused
the linked file, but configuration validation needed the same identity check.
This stopped checkpoint does not certify the final fix. Its logs and abort
record use prefix
`/tmp/airc-gateway-mapped-file-checkpoint-2afddf5-20261009T061632`;
its source snapshot SHA-256 is
`0e8c777df87e0e46d078a20277d2758167d3c6dd4cf647ccf4e39f5a528f2ecd`.

The first full run of the final corrected source
`542d8a9a8d0aac29c2d2c950b786feb2465d65b8ce469d7b401b71ec21d1231e`
exited 1 after 708.410 seconds in normal acceptance. Its sole failing test was
`TestRawQUICUniStreamRoundTrip`: initial QUIC handshake timed out before the
test sent any request. The unchanged test then passed 30 normal repetitions
and 10 race repetitions. No causal diagnosis follows from those passes, and
no test or production deadline was changed. The failed full-run prefix is
`/tmp/airc-gateway-mapped-file-checkpoint-2afddf5-20261009T065101`;
focused logs are `/tmp/airc-mapped-file-quic-handshake-repeat.jsonl` and
`/tmp/airc-mapped-file-quic-handshake-race-repeat.jsonl`.

## Mapped-file acceptance checkpoint

On 2026-10-09, the corrected mapped-file implementation and existing
shared-memory path passed full acceptance on Darwin ARM64 with Go 1.27.1.
Executable and test source remained unchanged throughout the run.

| Gate | Terminal exit | Seconds |
| --- | ---: | ---: |
| `go test ./... -count=1 -timeout=900s -parallel=3 -p=2 -json` | 0 | 690.374 |
| `go test -race ./... -count=1 -timeout=1800s -p=1 -json` | 0 | 1856.631 |
| `go build ./cmd/...` | 0 | 1.846 |
| `gofmt -l .` (empty output) | 0 | 0.468 |
| `go vet ./...` | 0 | 1.607 |
| `go run honnef.co/go/tools/cmd/staticcheck@latest ./...` | 0 | 5.086 |
| `git diff --check` | 0 | 0.024 |

Each test suite emitted 4651 pass events: 4608 test/subtest cases and 43
packages. Each had 74 skip events, including 11 packages with no runnable tests.
There were no failure events, malformed JSON events, race warnings or stderr
output. The final metadata records exit 0 at `2026-10-09T07:45:39.709323+00:00`.

The source SHA-256 is
`542d8a9a8d0aac29c2d2c950b786feb2465d65b8ce469d7b401b71ec21d1231e`;
the saved source archive SHA-256 is
`8ae77c550201e5cc8ac2009ec5b8b001d7480939e101f2c5b2010ed666ce6f65`.
Both the archive content and current files were independently checked against
the source digest. Commands, logs, metadata and the verified summary use prefix
`/tmp/airc-gateway-mapped-file-checkpoint-2afddf5-20261009T070302`.

Compilation caches were warm and test-result caching was disabled. These are
acceptance timings, not a speed comparison. The earlier QUIC handshake failure
and focused reruns remain recorded above; the unchanged test passed in both
full suites here. Actual Darwin/Linux native qualification, supported filesystems
and cooperative process-crash limits are recorded in
[mapped-file.md](mapped-file.md#native-qualification). Linux race and host-reboot
durability are not claimed.

## Ordered SCTP acceptance checkpoint

On 2026-10-09, ordered SCTP and the shared connection response-protection helper
passed full acceptance on Darwin ARM64 with Go 1.27.1. Executable and test source
remained unchanged across the native Linux qualification and the full host run.

| Gate | Terminal exit | Seconds |
| --- | ---: | ---: |
| `go test ./... -count=1 -timeout=900s -parallel=3 -p=2 -json` | 0 | 694.881 |
| `go test -race ./... -count=1 -timeout=1800s -p=1 -json` | 0 | 1903.980 |
| `go build ./cmd/...` | 0 | 2.713 |
| `gofmt -l .` (empty output) | 0 | 0.508 |
| `go vet ./...` | 0 | 2.708 |
| `go run honnef.co/go/tools/cmd/staticcheck@latest ./...` | 0 | 4.844 |
| `git diff --check` | 0 | 0.023 |

Each suite emitted 4687 pass events: 4644 test/subtest cases and 43 packages.
Each had 76 skip events, including 11 packages without runnable tests. Neither
suite had a failure event, malformed JSON event, race warning or stderr output.
Final metadata records exit 0 at `2026-10-09T08:52:36.052575+00:00`.

The executable/test source SHA-256 is
`250ffc97b924c441f5b298fbe5e21e367ca9d1c1b1570b56f9d1eb33367fc207`;
the saved source archive SHA-256 is
`76b32466c318cbd215e7d18082c793004b54bb8c1baa696e8938dea36bfb0a8c`.
Current files, archive contents and native qualification metadata were
independently checked against the same source digest. Commands, logs, metadata
and verified summary use prefix
`/tmp/airc-gateway-sctp-checkpoint-fe66710-20261009T080904`.

The [native qualification](sctp.md#native-qualification) independently ran 66
Linux ARM64 adapter/config/service/CLI/native cases with no skips, including
both IP families and actual blocked-write deadline/close behavior. Targeted
Linux vet and a Windows AMD64 unavailable-target build also passed. These
are acceptance results, not a performance comparison. Linux race detection,
other runtime targets, unordered messages and partial reliability remain
outside this checkpoint.

## Unordered SCTP acceptance checkpoint

On 2026-10-09, unordered SCTP passed full acceptance on Darwin ARM64 with
Go 1.27.1 after native Linux ARM64 qualification. Executable and test source
remained unchanged throughout the final native and host checks.

| Gate | Terminal exit | Seconds |
| --- | ---: | ---: |
| `go test ./... -count=1 -timeout=900s -parallel=3 -p=2 -json` | 0 | 692.063 |
| `go test -race ./... -count=1 -timeout=1800s -p=1 -json` | 0 | 1832.046 |
| `go build ./cmd/...` | 0 | 2.152 |
| `gofmt -l .` (empty output) | 0 | 0.295 |
| `go vet ./...` | 0 | 1.841 |
| `go run honnef.co/go/tools/cmd/staticcheck@latest ./...` | 0 | 4.813 |
| `git diff --check` | 0 | 0.026 |

Each suite emitted 4691 pass events: 4648 test/subtest cases and 43 packages.
Each had 76 skip events, including 11 packages without runnable tests. Neither
suite had a failure event, malformed JSON event, race warning or stderr output.
Final metadata records exit 0 at `2026-10-09T10:34:46.915348+00:00`.

The executable/test source SHA-256 is
`ba84e727f59d5480723cbcd5d8328a06ed37156495585bead91ecdf9240d6371`;
the saved source archive SHA-256 is
`12b0bae5c1b40863344b2391850e613686509a6837b953fc47e0351135cc2467`.
All 2000 archived source files were compared with current files. The source
digest also matches the independently verified native qualification. Commands,
logs, metadata and verified summary use prefix
`/tmp/airc-gateway-sctp-unordered-checkpoint-4e6f544-20261009T095232`.

The [native qualification](sctp.md#unordered-mode) passed 95 Linux ARM64
adapter/config/service/CLI/native cases without skips. It covers both delivery
modes and IP families, pipelined requests matched without assuming arrival
order, complete cursor reads, large replies, replay and shutdown. Unordered
requests bind correlation to the exact encrypted envelope; swapped labels on
two valid encrypted requests are refused before intake. A quiet native watch
checks bounded sequence reconstruction, cursor preservation and lease expiry.
The local fixture does not force network reordering. Targeted Linux vet and
staticcheck, focused host race tests and a Windows AMD64 unavailable-target
build also passed. Linux race detection and Windows runtime behavior are not
claimed.

An earlier checkpoint, before the envelope-binding change, passed normal tests
but failed the race suite with one timeout in the unchanged
`TestMQTTSNPreIntakeLossNativeReplayAndShutdown`, waiting for a native observer
event. Its evidence remains under
`/tmp/airc-gateway-sctp-unordered-checkpoint-4e6f544-20261009T090644`.
Three isolated race retries passed without changes in 14.951 seconds; logs are
`/tmp/airc-sctp-unordered-mqttsn-race-retry.json` and its `.stderr` companion.
The timeout's cause was not established. No MQTT-SN implementation or test was
changed, and that case also passed in the final full normal and race runs above.
The final checkpoint reran both suites after the separate SCTP correlation fix;
the earlier run is not counted as final acceptance.

## Ordered SCTP correlation acceptance checkpoint

On 2026-10-09, the ordered correlation migration passed full acceptance on
Darwin ARM64 with Go 1.27.1 after native Linux ARM64 qualification. Executable
and test source remained unchanged throughout the native and host checks.

| Gate | Terminal exit | Seconds |
| --- | ---: | ---: |
| `go test ./... -count=1 -timeout=900s -parallel=3 -p=2 -json` | 0 | 690.519 |
| `go test -race ./... -count=1 -timeout=1800s -p=1 -json` | 0 | 1774.770 |
| `go build ./cmd/...` | 0 | 1.307 |
| `gofmt -l .` (empty output) | 0 | 0.136 |
| `go vet ./...` | 0 | 1.002 |
| `go run honnef.co/go/tools/cmd/staticcheck@latest ./...` | 0 | 2.472 |
| `git diff --check` | 0 | 0.012 |

Each suite emitted 4693 pass events: 4650 test/subtest cases and 43 packages.
Each had 76 skip events, including 11 packages without runnable tests. Neither
suite had a failure event, malformed JSON event, race warning or stderr output.
Both ran the ordered and unordered application rejection cases. Final metadata
records exit 0 at `2026-10-09T11:24:48.257455+00:00`.

The executable/test source SHA-256 is
`588c08a11c40f429482f9fcb8aed8d406169c77bab445f40f44aa82fd05588eb`;
the saved source archive SHA-256 is
`825ae57a050c6631c7b5c26806f9e45298b5ff38027670887595b29659ad7fe8`.
All 2000 archived source files were compared with current files and matched
the native qualification source digest. Commands, logs, metadata and verified
summary use prefix
`/tmp/airc-gateway-sctp-ordered-correlation-checkpoint-afb73eb-20261009T104336`.

The [native qualification](sctp.md#ordered-correlation-migration) passed 99
Linux ARM64 adapter/config/service/CLI/native cases without skips. It covers
valid sends and complete cursor reads with the migrated client contract, plus
swapped envelope hashes and legacy opaque labels rejected before intake in
both modes. Existing replay, protected refusals, record bounds, shutdown and
unordered watch checks also passed. Focused host race tests and targeted Linux
vet/staticcheck passed. Linux race detection and other runtime targets are not
claimed. These results are acceptance evidence, not a performance comparison.

## SCTP timed-reliability acceptance checkpoint

On 2026-10-09, `sctp_pr_ttl` passed native Linux ARM64 qualification and full
Darwin ARM64 acceptance with Go 1.27.1. Executable and test source remained
unchanged throughout the final native and host checks. Compilation caches were
warm and test-result caching was disabled.

| Gate | Terminal exit | Seconds |
| --- | ---: | ---: |
| `go test ./... -count=1 -timeout=900s -parallel=3 -p=2 -json` | 0 | 680.921 |
| `go test -race ./... -count=1 -timeout=1800s -p=1 -json` | 0 | 1733.827 |
| `go build ./cmd/...` | 0 | 1.772 |
| `gofmt -l .` (empty output) | 0 | 0.149 |
| `go vet ./...` | 0 | 0.902 |
| `go run honnef.co/go/tools/cmd/staticcheck@latest ./...` | 0 | 2.644 |
| `git diff --check` | 0 | 0.017 |

Each suite emitted 4696 pass events: 4653 test/subtest cases and 43 packages.
Each had 76 skip events, including 11 packages without runnable tests. Neither
had a failure event, malformed JSON, race warning or stderr output. The final
metadata records exit zero at `2026-10-09T12:36:01.489135+00:00`.

The executable/test source SHA-256 is
`b492a44d13f52d8377dbd7aff72f405bdb89ddf37c9ac240829c9ae0af31d373`;
the saved archive SHA-256 is
`5d398ad1c75f3bc5d78afea31b72102644d4c7213a6e28d2c6cc4625131d385b`.
All 2010 archived source files matched the current tree and native qualification
digest. Commands, terminal output, metadata and verified summaries use prefix
`/tmp/airc-gateway-sctp-pr-ttl-checkpoint-f37f000-20261009T115540`.

The [native qualification](sctp-pr-ttl.md#native-qualification) passed all 120
adapter/configuration/service/CLI/gateway cases without skips. It proves actual
16 KiB request expiry before intake, exact-envelope retry with one native send,
production reply-writer expiry, whole-record recovery, PR-disabled peer refusal,
partial-delivery abort without journal admission, IPv4/IPv6 whole cursor reads,
large protected replies, replay and joined shutdown. Quiet watches require all
protected sequences before accepting their terminal record. Final native
artifacts are in `/tmp/airc-sctp-pr-ttl-linux-native-final-20261009/`.
Targeted Linux vet/staticcheck and focused host race checks also passed. Linux
race detection and untested targets remain unclaimed.

The initial independent native run exposed a test assumption: the pre-intake
expiry test required every filler reply even when an expected partial-delivery
abort closed the association. The corrected bounded drain authenticates every
available reply and retains the zero-journal, zero-native-post and exact-retry
assertions. Repetition then exposed a real listener exit on `EINTR`; accept now
retries that interruption. The final gateway passed a deterministic regression
and 20 consecutive native expiry/retry runs. The initial log, diagnostic repeat
and successful repetition are retained as `native.log`,
`preintake-diagnostic-repeat.log` and `preintake-20.log` under
`/tmp/airc-sctp-pr-ttl-linux-native-20261009/`.
These timings record acceptance, not a performance comparison.

## Mach IPC acceptance checkpoint

On 2026-10-09, `mach_ipc` passed native Darwin ARM64 qualification and full
repository acceptance on macOS 26.6.2 / Darwin 25.6.0 with Go 1.27.1 and the
macOS 27.0 SDK. Executable and test source remained unchanged throughout the
full gate. Compilation caches were warm and test-result caching was disabled.

| Gate | Terminal exit | Seconds |
| --- | ---: | ---: |
| `go test ./... -count=1 -timeout=900s -parallel=3 -p=2 -json` | 0 | 675.861 |
| `go test -race ./... -count=1 -timeout=1800s -p=1 -json` | 0 | 1882.538 |
| `go build ./cmd/...` | 0 | 2.310 |
| `gofmt -l .` (empty output) | 0 | 0.392 |
| `go vet ./...` | 0 | 2.138 |
| `staticcheck ./...` (2026.2.1 / 0.8.1) | 0 | 7.069 |
| `git diff --check` | 0 | 0.012 |

Each suite emitted 4,756 pass events: 4,712 test/subtest cases and 44 packages.
Each had 76 skip events, including 11 packages without runnable tests. Neither
had a failure event, malformed JSON, race warning or stderr output. All remaining
gate stages produced empty stdout/stderr. Final metadata records exit zero at
`2026-10-09T13:53:02.012128+00:00`.

The executable/test source SHA-256 is
`e9a1d7653aa0b7f67b064ade96691f1a275e4abeb85c3e03ddc446faaf2af968`;
the saved archive SHA-256 is
`42bd56819a13052ea5a1a8884bd8d0d93884b86355c22ea25b6d7d8fc46fcdcd`.
All 2,034 archived source files matched the current tree after the gate.
Commands, terminal output, metadata and independently verified summaries use
prefix `/tmp/airc-gateway-mach-ipc-checkpoint-09447da-20261009T131010`.

The [native qualification](mach-ipc.md#native-qualification) uses a separate
Apple SDK C client built with strict warnings, ASan and UBSan. Both full suites
passed actual native AIRC delivery, complete cursor reads, a protected reply
larger than 16 KiB, exact-envelope retry after losing a reply, caller/authentication
refusals without intake, capacity refusal followed by same-frame recovery, and
joined shutdown. Native package checks also cover audited child UID/PID, exact
payload limits, oversized/complex messages and port-right cleanup. Other
platforms, foreign-UID processes, sandboxed apps, cross-login namespaces and XPC
remain outside this qualification. These timings record acceptance, not a
performance comparison.

## XPC acceptance checkpoint

On 2026-10-09, the Darwin ARM64 app-bundled XPC bridge passed native qualification
and full repository acceptance with Go 1.27.1. The actual C client and embedded
service were compiled with strict warnings, ASan and UBSan; sanitizer recovery
was disabled. Executable, test and build-script source remained unchanged
throughout the full gate. Compilation caches were warm and test-result caching
was disabled.

| Gate | Terminal exit | Seconds |
| --- | ---: | ---: |
| `go test ./... -count=1 -timeout=900s -parallel=3 -p=2 -json` | 0 | 730.576 |
| `go test -race ./... -count=1 -timeout=1800s -p=1 -json` | 0 | 1896.540 |
| `go build ./cmd/...` | 0 | 2.320 |
| `gofmt -l .` (empty output) | 0 | 0.209 |
| `go vet ./...` | 0 | 0.922 |
| `staticcheck ./...` | 0 | 4.666 |
| `git diff --check` | 0 | 0.017 |

Each suite emitted 4,777 pass events: 4,733 test/subtest cases and 44 packages.
Each had 76 skip events, including 11 packages without runnable tests. Neither
had a failure event, malformed JSON, race warning or stderr output. All remaining
gate stages produced empty stdout/stderr. Terminal exit zero was recorded at
`2026-10-09T15:16:47.599873+00:00`.

The executable/test/build-script source SHA-256 is
`ff2e01abf5efe2fde167df37550a1a5ea5097245b2f34ace0365f819dc598110`;
the saved archive SHA-256 is
`1f1c7d8cb19fdc6241bdeea3f3f54cb45d464dffba5a331b402045c6f21d462c`.
All 2,051 archived Go, assembly, C, header, shell and module files matched the
current tree after the gate. Commands, terminal output, metadata and independent
verification use prefix
`/tmp/airc-gateway-xpc-checkpoint-857ffc2-20261009T143250`.

All 21 XPC test/subtest cases passed in both full suites. They exercise actual
native author/room/Unicode delivery, complete cursor history, a protected reply
larger than 16 KiB, retry after a discarded receipt with one native send,
authentication and mapping refusals without intake, SDK dictionary/size bounds,
parent descriptor preservation, client timeout, and joined gateway/service
shutdown. A real four-second queue-full timeout first reproduced a leaked
dead-name right; the corrected failed-send cleanup leaves port names and original
service references unchanged. The initial failure is preserved at
`/tmp/airc-xpc-mach-prefx-failure.log`. Shell syntax, shellcheck, normal bundle
signing and refusal to overwrite an existing bundle also passed.

The [native qualification boundary](xpc.md#native-qualification) records the
same-UID profile and the Go/Darwin stopped pending-signal limitation, reproduced
without Mach code. These timings record acceptance, not a performance comparison.

## D-Bus acceptance checkpoint

On 2026-10-09, the Darwin ARM64 private D-Bus peer bridge passed native
qualification and full repository acceptance with Go 1.27.1. The native C client
and server use GLib 2.88.3 and were built with Apple Clang 21, strict C11 warnings,
ASan and UBSan on macOS 26.6.2 with the macOS 27 SDK. Sanitizer recovery was
disabled and GLib warnings were fatal. Executable, test and build-script source
remained unchanged throughout the full gate. Compilation caches were warm and
test-result caching was disabled.

| Gate | Terminal exit | Seconds |
| --- | ---: | ---: |
| `go test ./... -count=1 -timeout=900s -parallel=3 -p=2 -json` | 0 | 789.534 |
| `go test -race ./... -count=1 -timeout=1800s -p=1 -json` | 0 | 2029.667 |
| `go build ./cmd/...` | 0 | 2.764 |
| `gofmt -l .` (empty output) | 0 | 0.425 |
| `go vet ./...` | 0 | 2.038 |
| `staticcheck ./...` | 0 | 5.213 |
| `git diff --check` | 0 | 0.023 |

Each suite emitted 4,808 pass events: 4,764 test/subtest cases and 44 packages.
Each had 76 skip events, including 11 packages without runnable tests. All 31
D-Bus cases and all 21 XPC cases passed in each suite, with no D-Bus skips.
Neither suite had a failure event, malformed JSON, race or sanitizer warning,
or stderr output. All remaining gate stages produced empty stdout/stderr.
The runner reached terminal exit zero at `2026-10-09T16:47:33.364525+00:00`.
Staged and unstaged whitespace checks also passed before committing.

The executable/test/build-script source SHA-256 is
`99ed6ff8f726b5ef3b47073fc73cfacbdbf0f2b50d9e927abf757d48c36ce1f6`;
the saved archive SHA-256 is
`9584f9872805cf9e664b62c95412c18a95263df994e9190d5f09450cbeeb0e54`.
All 2,065 archived Go, assembly, C, header, shell and module files matched the
current tree byte for byte after the gate. Commands, terminal output, metadata
and independent verification use prefix
`/tmp/airc-gateway-dbus-checkpoint-1648938-20261009T160021`.

An earlier full invocation at prefix
`/tmp/airc-gateway-dbus-checkpoint-1648938-20261009T154709` stopped after normal
tests, exit 1 in 782.502 seconds. Its only failing test was the unchanged
`cmd/airc-load` case `TestAgentsExerciseRealChatAndCancel`: 16 scheduled posts,
15 attempted/succeeded posts and one missed scheduling slot. All 31 D-Bus cases
passed. Five exact targeted reruns passed in 3.677 seconds without changing
source or assertions (`/tmp/airc-dbus-load-test-recheck.log`), followed by the
fresh full normal/race gate above. The original failure remains recorded.

The [native qualification](dbus.md#native-qualification) checks independent
AIRC author/room/Unicode delivery, whole cursor history through the empty page,
a protected reply exceeding 16 KiB and exact-frame retry after a discarded
receipt with one native send. Refusal cases cover authentication, freshness,
mapping, SDK signatures/arguments, bounds and file descriptors. Real five-second
authentication and thirty-second authenticated-idle expiry, private-path and
replacement controls, and joined signal/connection shutdown also passed.

A native Mach receiver witnessed an actual pending request before the client
and bridge were terminated. This first reproduced a fatal invalid poll
descriptor when listener close preceded canceled-accept completion. The fixed
server waits for its asynchronous accept to complete before closing the listener,
then joins the pending Mach worker and connection. Failure and successful
regression logs are `/tmp/airc-dbus-pending-mach-before-fix.log` and
`/tmp/airc-dbus-pending-mach-after-fix.log`.

The focused 31-case race run also passed in 89.729 seconds with no skips or
stderr (`/tmp/airc-dbus-focused-final-race.jsonl`). Strict normal native builds,
Clang static analysis, shell syntax and shellcheck passed. CLI checks cover the
eight-second process deadline, interruption, inherited descriptor flags and
bounded input. This qualifies the Darwin ARM64 same-UID private peer profile;
shared buses, foreign-UID processes, Linux and independently installed service
profiles remain outside it. These timings record acceptance, not performance.

## QR image-sequence acceptance checkpoint

On 2026-10-09, the Darwin ARM64 QR image-sequence profile passed native
qualification and full repository acceptance with Go 1.27.1. The native codec
uses CoreImage and Vision, built with Apple Swift 6.4, warnings as errors and
AddressSanitizer on macOS 26.6.2 with the macOS 27 SDK. Executable, test and
build-script source remained unchanged throughout the full gate. Compilation
caches were warm and test-result caching was disabled.

| Gate | Terminal exit | Seconds |
| --- | ---: | ---: |
| `go test ./... -count=1 -timeout=900s -parallel=3 -p=2 -json` | 0 | 867.556 |
| `go test -race ./... -count=1 -timeout=1800s -p=1 -json` | 0 | 2054.914 |
| `go build ./cmd/...` | 0 | 2.398 |
| `gofmt -l .` (empty output) | 0 | 0.413 |
| `go vet ./...` | 0 | 1.612 |
| `staticcheck ./...` | 0 | 5.595 |
| `git diff --check` | 0 | 0.012 |

Each suite emitted 4,815 pass events: 4,771 test/subtest cases and 44 packages.
Each had 76 skip events, including 11 packages without runnable tests. All seven
QR cases, 31 D-Bus cases and 21 XPC cases passed in each suite. Neither suite
had a failure event, malformed JSON, race or sanitizer warning, or stderr output.
All remaining gate stages produced empty stdout/stderr. The runner reached
terminal exit zero at `2026-10-09T17:56:17.843266+00:00`. Staged and unstaged
whitespace checks also passed before committing.

The executable/test/build-script source SHA-256 is
`0d03c48e20dad071bacd3668f0eb063c035912e937ae4907149b1eb78d7c02b8`;
the saved archive SHA-256 is
`6016be99acff6fd7e1852e0cee6041c6ca7c37603fa4b3e7599058ec212727f5`.
All 2,072 archived Go, assembly, C, header, Swift, shell and module files matched
the current tree byte for byte after the gate. Commands, terminal output,
metadata and independent verification use prefix
`/tmp/airc-gateway-qr-checkpoint-6fb2a40-20261009T170723`.

The [native qualification](qr-sequence.md#native-qualification) checks real PNG
encoding and decoding followed by the actual `airc-gateway import-bundle` CLI.
An independent native AIRC observer verifies exact author, room and Unicode/large
bodies. Reversed image order preserves the complete signed bundle. Fresh-process
decode/reimport retains original receipts, expiry and one send attempt without
duplicate native messages. Expired and signature-tampered envelopes fail before
journal creation; a fresh signed control succeeds. Journal ownership is released
after delivery workers join.

Codec controls cover the exact 65,536-byte/128-image limit, arbitrary binary
bytes, missing/duplicate/mixed fragments, independently rendered wrong-digest
QR symbols, malformed/oversized images, excessive dimensions/image count,
symlinks, FIFOs, existing-output preservation and protected publication parents.
A valid one-frame APNG first reproduced acceptance by the image-count-only
check. Checking animation keys in ImageIO's container PNG dictionary now refuses
that same image without output; before/after binaries, hashes and results are
recorded in `/tmp/airc-qr-review.z6XeTm/apng-regression.json`.

A real blocked diagnostic pipe verifies that the codec's thirty-second alarm
terminates the process before the external supervisor deadline. The focused
seven-case race run also passed in 76.062 seconds with no skips or diagnostics
(`/tmp/airc-qr-focused-final-race.jsonl`). Strict normal native builds, shell
syntax and shellcheck passed. This qualifies send-only local image files and
native software clients on Darwin ARM64; Linux, cameras and physical optical
delivery remain outside the evidence. These timings record acceptance, not
performance.

## ICMPv6 acceptance checkpoint

On 2026-10-09, the Darwin ARM64 ICMPv6 paired-endpoint profile passed native
qualification and full repository acceptance with Go 1.27.1 on macOS 26.6.2.
Executable, test and build-script source remained unchanged throughout the
full gate. Compilation caches were warm and test-result caching was disabled.

| Gate | Terminal exit | Seconds |
| --- | ---: | ---: |
| `go test ./... -count=1 -timeout=900s -parallel=3 -p=2 -json` | 0 | 853.032 |
| `go test -race ./... -count=1 -timeout=1800s -p=1 -json` | 0 | 2043.204 |
| `go build ./cmd/...` | 0 | 2.095 |
| `gofmt -l .` (empty output) | 0 | 0.329 |
| `go vet ./...` | 0 | 1.411 |
| `staticcheck ./...` | 0 | 7.518 |
| `git diff --check HEAD` | 0 | 0.032 |

Each suite emitted 4,864 pass events: 4,820 test/subtest cases and 44 packages.
Each had 76 skip events, including 11 packages without runnable tests. All 49
ICMPv6 cases, seven QR cases, 31 D-Bus cases and 21 XPC cases passed in each
suite. Neither suite had a failure event, malformed JSON, race or sanitizer
warning, or stderr output. The other gate stages produced empty stdout/stderr.
The runner reached terminal exit zero at `2026-10-09T19:16:01.013534+00:00`.

The executable/test/build-script source SHA-256 is
`f66ef2819ba6dd9b790dbde5a7138a679e968c88bf8ef9937f910b88c99c1e7f`;
the saved archive SHA-256 is
`50f413ad6f77871922065cbedbb031eb2977b9cefa3c32888447cc2d96c6c207`.
All 2,084 archived Go, assembly, C, header, Swift, shell and module files matched
the current tree byte for byte after the gate. Commands, terminal output,
metadata and independent verification use prefix
`/tmp/airc-gateway-icmpv6-checkpoint-b2f15f1-20261009T182732`.

The [native qualification](icmp-v6.md#native-qualification) uses an independent
ICMPv6 client and actual gateway process with a native AIRC observer. It checks
exact author, room and Unicode/large bodies, discarded-receipt replay with one
native send, a protected history response larger than 16 KiB, and every
limit-one cursor page through the empty caught-up boundary. Authentication,
freshness, profile, channel, oversized and incomplete-fragment refusals produce
no journal job or native message. Retrying the exact retained incomplete
envelope succeeds once. SIGINT joins each gateway process.

Native socket tests exercise maximum payloads in both directions, a 64 KiB
response's complete fragments, a queued maximum request, metadata and size
refusals, and close interrupting a read. Startup rollback releases journal
ownership. Review corrected an omitted ICMP-header allowance and accidental
application of the request-only parser to larger response fragments.

Large native requests initially lost fragments with Darwin's default 8 KiB
ICMP receive queue. In a separate controlled sixteen-packet burst, all three
default-buffer trials received seven packets, while all three 256 KiB trials
received all sixteen. The gateway now sets and verifies that per-socket queue
and filters kernel Echo Replies; the independent client verifies its receive
queue too. Large native send and whole-history acceptance then passed without
fragment pacing. Probe source/results are `/tmp/icmp6_buffer_ab.go` and
`/tmp/icmp6_buffer_ab.log`. This establishes bounded local capacity, not general
network reliability or throughput.

The final focused native integration race run passed both cases in 12.058
seconds. Linux ARM64 cross-compilation passed for the unsupported-platform
path; no Linux runtime support is claimed. This qualifies Darwin ARM64 loopback
paired endpoints, with no permission changes. IPv4 ICMP, remote peers, carriers,
mode-specific restart replay and a separate streaming-watch exercise remain
outside this evidence. These timings record acceptance, not performance.

## Simplification and VPS acceptance (2026-10-10)

Kafka configuration, runtime, wire codecs and tests were removed while retaining
migration 15 and historical journal evidence. Shared native-read fixtures,
HTTP-only fixtures, signed dispatch and journal migration selection were
simplified. Remote qualification also corrected fixture filesystem, correlation,
platform and startup-order assumptions, a CoAP cancellation/finished-write race,
and an untyped disconnected-client error that prevented CLI wait recovery.

Final race acceptance used Go 1.27.1 on macOS 26.6.2 ARM64 and Ubuntu 26.04 AMD64
(kernel 7.0.0-28-generic) on `vps-2bb89225.vps.ovh.ca`. Both hosts matched all
2,580 files in the frozen manifest, SHA-256
`7ca362dd4993d1d43f901a4f1eccfb28a9f4ea7beefca9eef78f3fca6a64dbc9`.
Only documentation changed after these checks. Compilation caches were warm;
`-count=1` disabled test-result caching. Durations below are execution records,
not performance comparisons.

| Final check | Darwin | VPS Linux |
| --- | --- | --- |
| `go test -race ./... -count=1 -timeout=1800s -p=1 -json` | Pass, 2168.472s | Pass, 2283.025s |
| `go build ./cmd/...` | Pass, 2.702s | Pass, 6.771s |
| `go vet ./...` | Pass, 2.506s | Pass, 34.214s |
| `staticcheck ./...` (2026.2.1 / 0.8.1) | Exit 1: 35 existing findings | Exit 1: 62 existing findings |

Every final race run passed all 44 runnable packages with zero failure events.
All staticcheck findings matched the baseline production scan at
`52abd6c760f7188a92b206d3ee75b60718287aa0`; there were no new diagnostics. These
include error capitalization, platform-specific unused declarations, one string
search suggestion and intentional lock/join barriers. They were not suppressed
or changed as part of this task. Formatting and whitespace checks passed.

The preceding full normal command was
`go test ./... -count=1 -timeout=1200s -parallel=3 -p=2 -json`.
Darwin passed in 891.106s. Linux finished in 714.073s with two fixture failures:
OSCORE installed a pipe deadline after an expected immediate close, and the
multi-transport fixture released ports before selecting all of its addresses.
Only those two test files changed afterwards. On the final source, the entire
OSCORE package passed normally on both hosts (2.694s / 2.623s), and focused
reaction, encrypted-watch, profile and presence integration checks passed
(15.233s / 11.912s). The full race passes above include both corrected fixtures.
The final full normal command was not repeated after these fixture-only repairs.

Linux journal fixtures used an owner-only ext4 directory through `TMPDIR`.
Private network namespaces supplied loopback and an IPv4/IPv6 multicast-capable
veth pair; broadcast and SSH local/reverse/SOCKS opt-ins ran there. No host routes
or firewall rules changed. The three foreign-UID Unix stream, Unix packet and
POSIX queue checks additionally passed as isolated privileged race tests.
The installed local mitmdump HTTP CONNECT opt-in also passed with race detection
in 5.474s; mitmdump was unavailable on the VPS.

A fresh gateway binary on the VPS (SHA-256
`79787b612d2ee6bb23342940f22304c16677cf76687b9603853539396774c0ff`)
accepted two laptop HTTPS requests through an SSH forward using verified TLS 1.3.
An independent ordinary `airc history` call confirmed exact IDs, author, target,
Unicode and newline bodies. Limit-one cursor pages, caught-up reads, replay with
one send attempt, unauthenticated 401 and conflicting-body 409 all passed.
Both owned services exited zero, their port closed, and the existing VPS services
remained unchanged. This does not qualify direct public ingress through the host
firewall.

Browser probes, unavailable native OSCORE/Net-SNMP dependencies, physical devices,
external providers and unsupported platform modes remain outside this evidence.
Soak and latency opt-ins were not run. Helper/platform skips are retained in the
logs; the privileged checks above cover three otherwise skipped Linux cases.

Local evidence is under `/tmp/airc-gateway-simplify/`, with remote final logs copied
into `vps-final/`. VPS originals are under
`/home/ubuntu/gateway-simplify-20261010/`. `final-*` contains final race/build/vet/
staticcheck and focused normal results; `normal-before-fixture-repairs/` preserves
the complete normal runs. `acceptance-manifest.json`, baseline lint comparisons,
`privileged-peer-race*`, and `public-probe-reconnect/` identify the source and
additional checks. Earlier cancelled or failed attempts are retained separately
and are not final acceptance evidence.


## Main consolidation acceptance (2026-10-10)

Five reviewed checkpoint commits removed Kafka ingress and consolidated fixtures,
restored clean platform lint, reduced history copying and ID-decoding allocations,
and repaired CoAP cancellation so it cannot interrupt an unrelated physical write.
Migration 15 and historical Kafka journal evidence remain unchanged. The merge
also retains main's shared-helper and disconnected-command recovery changes.

Final executable source was frozen at Git tree
`4c72e21730f7443a52f69b352c3ae4ca154d81ef`; only these evidence documents changed
after acceptance. Checks used Go 1.27.1 on Darwin ARM64 with warm compilation
caches and `-count=1` to disable test-result caching.

| Check | Result |
| --- | --- |
| `go test ./... -count=1 -timeout=1200s -parallel=3 -p=2 -json` | Pass, 906.07s wall time |
| `go test -race ./... -count=1 -timeout=1800s -p=1 -json` | Pass, 2183.25s wall time; no race reports |
| `go build ./cmd/...`, `go vet ./...` | Pass |
| `staticcheck ./...` (2026.2.1), Darwin ARM64 and Linux AMD64 | Pass, no diagnostics |
| Parser `FuzzParse`, 20s, two workers | Pass, 1,210,545 executions |
| Formatting and Git whitespace checks | Pass |

Both complete test runs passed all 44 runnable packages with zero failure events
and 65 helper/platform/opt-in test skips. Linux staticcheck is a cross-platform
analysis; no new Linux runtime or VPS qualification was performed in this
consolidation. The preceding VPS checkpoint above remains historical evidence.

Alternating baseline/candidate runs of the existing 10,000-record history
benchmarks on an Apple M4 measured median recent-channel time falling from
4756.5ns to 3371.5ns (29%) and old-cursor time from 7369ns to 3212.5ns (56%), with
unchanged query allocation counts. ID validation now allocates zero bytes;
a 50-message gateway history page falls from 381 to 330 allocations. Inbox and
page latency measurements were noisy and do not establish a reliable speedup.
These local measurements do not establish deployed throughput or latency.

Raw merged acceptance logs, source identity and paired history measurements are
under `/tmp/airc-health-consolidation/`; gateway allocation comparisons are in
`/private/tmp/gateway-runtime-baseline.txt` and
`/private/tmp/gateway-runtime-candidate.txt`. Existing physical-device,
native-worker, browser and provider qualification limits remain in the mode
specifications and roadmap.


## CLI and server refactor acceptance (2026-10-10)

The previously uncommitted main refactor is recorded in `054b9ff`: shared CLI/MCP
command dispatch and reconnect supervision, concrete validation/state helpers,
cohesive server and SDK modules, and shared native test fixtures. Public APIs,
wire formats and persistence behavior were preserved. Review found and repaired
a buffered-reply regression: a completed response or rejection must be consumed
before reporting the peer's subsequent disconnect. A real TCP regression test
failed before the repair and passed afterwards with race detection.

The merge retains the published gateway implementation, history pointer scan,
immutable CLI executable fixture, native seed/readiness barriers and Unix socket
fixtures. Final checks used Go 1.27.1 on Darwin ARM64 and frozen Git tree
`ca985d9749f9c1479dfaa34c6b5678332dacf00d`. Compilation caches were warm;
`-count=1` disabled test-result caching. Only these evidence documents changed
after acceptance.

| Check | Result |
| --- | --- |
| `go test ./... -count=1 -timeout=1200s -parallel=3 -p=2 -json` | Pass, 892.83s wall time |
| `go test -race ./... -count=1 -timeout=1800s -p=1 -json` | Pass, 2170.27s wall time; no race reports |
| Build, vet and full Darwin/Linux AMD64 staticcheck | Pass |
| Parser `FuzzParse`, 20s, two workers | Pass, 948,297 executions |
| Existing `BenchmarkHistory` checks | Pass |
| Whole-tree formatting and Git whitespace checks | Pass |

Both full suites passed all 44 runnable packages, with zero failure events and
65 helper/platform/opt-in test skips. Linux lint is cross-platform analysis;
this refactor adds no new Linux runtime, deployment or physical-mode evidence.
Complete logs, initial dirty-file backups and source identity are under
`/tmp/airc-main-consolidation/`. The buffered-reply before/after logs are
`/private/tmp/airc-await-reply-before.log` and
`/private/tmp/airc-await-reply-after-race.log`.

## Connection-mode pruning check (2026-10-10)

The [removed modes](removed-modes.md) were deleted from base `6a34232`. On a
Linux AMD64 sandbox without IPv6 or `/dev/mqueue`, the gateway packages,
`cmd/airc-gateway` and `integration/gateway` (with `-timeout 120m`) produced
the same 49 top-level failures on the base and the pruned tree: 22 package
tests and 27 integration tests. All are IPv6 fixtures (syslog, SNMP, MQTT-SN,
framed and WebSocket IPv6, IPv6 multicast), POSIX queues or UDP-Lite. Build, gofmt,
vet, Darwin and Linux ARM64 vet, and staticcheck with `GOTOOLCHAIN=go1.27.0`
passed. `GOOS=windows go build ./...` still fails in `internal/atomicfile`,
the same as on the base. Every gateway package builds for Windows. This is a
removal check, not mode qualification, and it adds no race, Darwin runtime
or VPS evidence.

## Bridge authors against Ergo and Prosody

On 2026-10-11 the IRC and XMPP bridges ran from this tree against real servers:
Ergo (TLS, SASL, `account-tag`) and Prosody (direct TLS, non-anonymous MUC),
with a scratch aircd on its own Unix socket and state directory started with
`--gateway-token-file`, and the gateway configured with only an `upstream`,
an IRC and an XMPP bridge (no principals, bindings or signing keys). Each
bridge mapped `carol` / `carol@localhost` to the registered AIRC account
`carol-acct`. An independent `airc history '#bridged' --json` observer read:

| seq | from | registered | sent by |
| --- | --- | --- | --- |
| 1 | `irc-alice` | no | Ergo account `alice`, plain PRIVMSG |
| 2 | `carol-acct` | yes (account ID) | Ergo account `carol` |
| 3 | `xmpp-alice` | no | `alice@localhost` as occupant nick `alice-phone` |
| 4 | `carol-acct` | yes (account ID) | `carol@localhost` as occupant nick `Carol-Display` |
| 5 | `irc-carol-acct` | no | Ergo account literally named `carol-acct` |
| 6 | `xmpp-alice` | no | `alice@localhost` using occupant nick `carol-acct` |

An unauthenticated IRC nick's message (no account tag) was not posted. aircd
logged the WEBIRC sources `bridge:ergo:irc-alice`, `bridge:ergo:carol-acct`,
`bridge:prosody:xmpp-alice` and `bridge:prosody:carol-acct`. A typed
`AIRC-READ/1 {"operation":"history.read",...}` was answered on IRC with four
`NOTICE alice :AIRC-RESULT/1 ...` lines joining into the plain page JSON, and
on XMPP with one private chat message to the occupant carrying the same JSON.
No Matrix homeserver was available; the Matrix path is covered by httptest
tests only.
