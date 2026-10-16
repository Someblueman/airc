# AIRC performance scorecard

Track these six measures when changing the client or server's hot paths, and
preserve the already fast ordinary send and wait paths. Keep the measures separate rather than combining
them into a score that can hide regressions.

For concurrent traffic at 50/100/500/1000 clients, use the [scripted load
harness](LOAD_TESTING.md). It records individual-operation p50/p75/p90/p99,
throughput, failed/missed actions and separate server resource samples.

## Headline measures

Baselines below come from the 3 October 2026 investigation of `9b303ae` on an
Apple M4 with Go 1.25.0. They are medians of the recorded run averages, except
context, which is the median of two individual calls. They are **not p95
latencies**. Targets are proposed engineering budgets for the same fixtures and
host, not current guarantees. MB and GB are decimal; allocations are cumulative,
not peak memory. [Raw evidence and commands](research/performance-2026-10-03/README.md)
and [machine-readable baselines](research/performance-2026-10-03/scorecard.csv)
make the measurements reproducible.

| Measure | Fixed measurement | Baseline | Initial target |
| --- | --- | --- | --- |
| 1. Send confirmation latency | Real authenticated CLI invocation through successful receipt output/exit, including durable local outbox and server history | 28.11 ms | At most 30 ms; preserve durability |
| 2. Reply wake-up latency | SDK post to an already-waiting guest CLI returning the matching message and exiting, including server Sync | 2.61 ms | At most 5 ms; no polling required |
| 3. Context retrieval cost | One root plus 999 replies of 3,500 bytes; `limit=1000`, 32 KiB output budget | 2,447 ms and 2,215 MB allocated | At most 250 ms and 100 MB allocated |
| 4. Expensive server-query latency | Search 10,000 × 4,096-byte mixed-case bodies for absent text; separately CHECK 64 absent rooms from an old valid cursor | Search 94.34 ms; CHECK 20.27 ms | Search at most 20 ms; CHECK at most 10 ms |
| 5. UI redraw cost | Invalidated render cache, 500 × 1,024-byte messages, 160 × 45 viewport | 28.37 ms and 17.45 MB allocated | At most 5 ms and 1 MB allocated |
| 6. Conversation effort | Retrieve relevant context, reply to the correct message and receive a peer response, with the scenario below | Not measured | At most 3 agent tool calls or 4 human actions; all required content correct |

The baselines above were measured on 3 October 2026 (Apple M4, Go 1.25.0). The
first optimization pass that day met all six targets for these fixtures; its
[results, environment and commands](research/scorecard-optimized-2026-10-03/README.md)
record what each measure covers and leaves out. When a headline number
changes, use the diagnostic measurements there: login and empty check latency
for 1, search allocations and quota insertion for 4, cached redraw time and
terminal bytes written for 5.

## Conversation effort scenario

Use the same scripted transcript for humans and agents. The room contains one
question, one superseded answer, its correction, a relevant pin and ten unrelated
messages. Retain every record and make the relevant context fit inside 32 KiB.
Start logged in with the question ID supplied to the agent, and the question
visible and selected in the human UI. This measures interaction after discovery;
it does not claim to measure first-time onboarding or search.

The participant must retrieve the context, answer using the correction rather
than the obsolete value, attach the reply to the specified question, and observe
a scripted peer reply arriving after the response. Do not count a reaction or
reply-coming signal as the peer's answer.

For agents, count every tool invocation, including unsuccessful calls, polling
and retries: ideally `context`, `send`, `check --reply-to ... --wait ...`. Record
total returned UTF-8 bytes as a companion measure. Count shell commands as calls
if a tool gap requires a shell; do not hide them. If using a real model, record
model/version, prompt, sampling settings, success count and total trials. Keep
deterministic adapter checks separate from model reasoning evaluations.

For humans, count deliberate navigation/command/submission actions, excluding
typing the answer and passive reading. Record completion time separately because
reading and reasoning vary between people. The proposed four-action budget is
open context, choose reply, submit, and open the incoming response if needed.
Repeated keypresses and corrective actions count individually; log the actual
sequence rather than inferring it from supported commands.

An attempt succeeds only if the relevant pin and correction were available,
the answer uses the current value, the reply points to the right question,
exactly one post is accepted, and the peer response is observed. Report success
as `successful attempts / all attempts`, plus calls/actions per successful
attempt and the cost of failed attempts separately. A faster failed attempt is
not an improvement. Aim for every scripted attempt passing; real-model success
rates need observed denominators and cannot establish a universal guarantee.

The agent adapter scenario is now automated by
[`TestConversationEffortThreeMCPCalls`](../cmd/airc/conversation_effort_test.go)
and passed five recorded trials. Returned result bytes and exact acceptance
checks are retained in the new evidence report. Model reasoning and human
reasoning time remain unmeasured. The human-interface scenario now passes 5/5
real PTY trials in three deliberate actions: Ctrl-O (context), Ctrl-R (reply),
Enter (submit). The peer response appears automatically. Selection/setup, typing
the answer and passive reading are excluded as specified above. These are
scripted UI acceptance trials, not a human usability study.

[Follow-up evidence and reproduction](research/scorecard-usability-2026-10-03/README.md)
include all trial outputs and terminal-input checks. A redraw regression check
with the selection index measured 0.295 ms and 0.648 MB allocated (median of three
one-second runs on the shared host), still below the original 5 ms / 1 MB target.

## Correctness and resource guardrails

Keep these beside the six performance measures. An improvement is unacceptable
if it breaks a guardrail, regardless of how much latency falls.

| Guardrail | Required observation |
| --- | --- |
| Send recovery | Zero duplicate posts in the fixed crash/disconnect suite; every reported persisted acceptance still within configured retention survives restart. Track lost confirmations separately from lost messages. |
| Context and catch-up | Exact original text and reply/correction IDs; no hidden omission or retention gap; no cursor advancement past deferred messages. |
| Human input | Zero unintended posts from multiline paste; no lost split Unicode characters or discarded drafts on rejection. Verified by real PTY paste/validation checks, every-byte-boundary tests, server rejection and dropped-receipt tests, and bounded native fuzzing. Drafts survive a full command queue or an offline connection. |
| Resource stability | Under a fixed 30-minute retention-saturating churn test, sample server post-GC live heap, goroutines, descriptors and queued bytes after identical drain/idle checkpoints. Report final-minus-warm-baseline and trend; queues drain and resources return within a predeclared tolerance. |

The isolated 30-minute soak now passes: 376 measured cycles after calibration,
with server post-GC heap ending at 11.332 MB versus an 11.268 MB warm maximum
(+0.064 MB), below its frozen 12.316 MB ceiling. Goroutines stayed at 3,
descriptors at 8, and drained queues at zero. The last-ten-minute heap trend was
−1,038 bytes/minute. [Raw checkpoints, fixture and limits](research/scorecard-usability-2026-10-03/README.md)
include separate client samples and the calibration/noise rule fixed before the
timed phase. The fixture uses 50 writers followed by five concurrent large-context
readers, disk persistence, 2000-message retention and a 16 MiB outbound allowance.
It does not establish a zero-growth guarantee or default-queue capacity.

Allocation churn, RSS and retained live heap measure different things. This soak
did not sample RSS or transient peak heap; retain those as diagnostics in future
load comparisons. A rising post-GC heap with constant retained state warrants
investigation; one high RSS sample does not prove a leak.

## Binary size

`scripts/binary-size.sh` builds `airc`, `aircd`, `airc-gateway` and the
`airc-load` development tool three ways: `plain` (`go build`),
`trimpath-stripped` (`-trimpath -ldflags='-s -w'`) and `installed` (as
`scripts/install.sh` builds them; now the same flags as `trimpath-stripped`,
since `-tags grpcnotrace` stopped mattering once no binary linked gRPC). It prints one Go
benchmark line per build with its size in bytes and its `gzip -9` size, a
download proxy. Builds are deterministic, so one sample per build is exact;
`--compare` prints the deltas between two result files.

```sh
scripts/binary-size.sh > /tmp/size-new.txt                     # host build
scripts/binary-size.sh --target linux/amd64 --target darwin/arm64 --target linux/arm64
scripts/binary-size.sh -C ../airc-old > /tmp/size-old.txt      # another checkout or worktree
scripts/binary-size.sh --compare /tmp/size-old.txt /tmp/size-new.txt
scripts/binary-size.sh --check                                 # fail over a generous budget
```

`--check` fails when an installed host binary exceeds its budget in the
script, about one and a half times the installed sizes of 12 October 2026:
14 MiB for `airc`, 10 MiB for `aircd` and `airc-load`, 32 MiB for the gateway. It is
opt-in and not part of `go test`; run it before adding a dependency. A full
host run takes a few minutes on a cold build cache. Sizes and what dominates
each binary: [October 2026 measurements](research/benchmarks-2026-10-11-final/README.md#binary-size),
the [stripped install](research/benchmarks-2026-10-11-final/README.md#addendum-the-stripped-install-2026-10-11),
and the [in-repo MCP and gRPC servers](research/binary-size-2026-10-12/README.md)
(linux/amd64 installed: `airc` 9.4 MB, `aircd` 6.7 MB, `airc-gateway` 22.2 MB).

## Comparing changes over time

1. Before changing a relevant path, run its recorded fixture on the baseline
   commit. Afterward, repeat on the candidate under the same conditions. Run
   serially on an otherwise quiet host, without race/profiling instrumentation.
   Use the same Go version, CPU, GOMAXPROCS, history, body sizes, account mode,
   persistence settings and network conditions.
2. Retain raw Go benchmark output and summarize the median of run means. Use at
   least five independent runs, with at least one second per microbenchmark and
   100 iterations per real-CLI case. These longer runs improve on the exploratory
   baseline's small samples; do not claim their precision for the old baseline.
   Compare old and new binaries with the same repetition settings.
3. For context, use the existing probe at limits 50, 500 and 1,000 and repeat it
   five times. Keep all limits visible so an optimization at the stress limit
   cannot conceal a default-path regression. Preserve complete records, not just
   output byte counts. Run the correctness suite separately.
4. Record each metric's change as `100 × (candidate − baseline) / baseline`;
   negative means improvement for time, allocations, bytes and steps. An increase
   above 10% is an investigation trigger, not an automatic failure on noisy
   hardware. Repeat a paired comparison when the difference exceeds observed
   run variation. Hard correctness failures are never waived as benchmark noise.
5. Save results after relevant changes and before releases; link them from the
   PR/commit. Record date, commit, clean/dirty state, fixture version, host,
   toolchain, configuration, sample count, statistic and raw evidence. Keep the
   original baseline immutable. Version changed workloads rather than presenting
   different inputs as a speedup.

The CSV contains exact summaries of the historical samples and explicit missing
values for the unmeasured workflow. An empty value means unmeasured, never zero.
Future runs can use the same columns, with a sibling evidence file describing
environment and sample boundaries.

The load harness now supplies per-operation p50/p75/p90/p99 from raw request
timings during concurrent chat/search/catch-up. A separate
[cold/warm MCP TLS fixture](research/participation-2026-10-03/README.md) now
measures nominal 0/25/100 ms RTT; its three-sample medians are not load percentiles.
Real remote load and UI input-to-frame latency in a PTY remain separate
measurements to add. Percentiles of benchmark
batch averages are not request percentiles. Keep local/cold CLI, remote CLI and
warm MCP as separate series. Report timeout/error counts alongside successful
request latency so dropping slow requests cannot make a release look faster.

## Persisted posts

`go test ./internal/server -run '^$' -bench BenchmarkPersistedPosts` measures
confirmed direct messages against a history file with 1, 16 and 64 concurrent
senders. Concurrent posts share history syncs (group commit), so durable
throughput grows with senders instead of being one sync per post. On a Linux
VM with ~160µs fsync (2026-10-16), `--sync full` went from 5.6k to 34k posts/s
at 64 senders and from 5.1k to 26k at 16, with one sender unchanged at ~3.8k;
`--sync none` was unchanged. With a simulated 5ms sync (F_FULLFSYNC-like), 64
senders reached ~4.8k posts/s where one sync per post caps at 200.
