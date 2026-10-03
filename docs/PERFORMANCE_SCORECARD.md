# AIRC performance scorecard

Track these six measures when implementing the [roadmap](PERFORMANCE_ROADMAP.md).
Optimize the slow context/query/render paths first; preserve the already fast
ordinary send and wait paths. Keep the measures separate rather than combining
them into a score that can hide regressions.

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

Measure 4 has two independently reported components: do not average them.
Its fixtures exclude networking and concurrent lock contention. Measure 5
measures the model render, not terminal display latency. The context fixture uses
an embedded server with a 16 MiB outbound limit to isolate client trimming;
production defaults are different. Keep these boundaries in comparisons.

Use diagnostic measurements when a headline number changes: login and empty
check latency for 1; search allocations and quota insertion for 4; cached redraw
time and terminal bytes written for 5. Their existing baselines are in the same
evidence directory. They need not all become release gates.

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

This scenario and its targets are defined here but have **not been run or
automated**. Likewise, terminal input probes in the investigation are not human
workflow measurements.

## Correctness and resource guardrails

Keep these beside the six performance measures. An improvement is unacceptable
if it breaks a guardrail, regardless of how much latency falls.

| Guardrail | Required observation |
| --- | --- |
| Send recovery | Zero duplicate posts in the fixed crash/disconnect suite; every reported persisted acceptance still within configured retention survives restart. Track lost confirmations separately from lost messages. |
| Context and catch-up | Exact original text and reply/correction IDs; no hidden omission or retention gap; no cursor advancement past deferred messages. |
| Human input | Zero unintended posts from multiline paste; no lost split Unicode characters or discarded drafts on rejection. Existing parser probes demonstrate failures, so this is currently outstanding. |
| Resource stability | Under a fixed 30-minute retention-saturating churn test, sample server post-GC live heap, goroutines, descriptors and queued bytes after identical drain/idle checkpoints. Report final-minus-warm-baseline and trend; queues drain and resources return within a predeclared tolerance. |

The resource soak has not been run. Establish its warmed baseline and repeat-run
noise before setting a numeric tolerance; do not invent a zero-growth guarantee.
Record server and client processes separately, retaining RSS and peak heap as
diagnostics. Allocation churn, RSS and retained live heap measure different
things. A rising post-GC heap with constant retained state warrants investigation;
one high RSS sample does not prove a leak.

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

As load and remote support become priorities, add per-operation p50/p95/p99 from
raw request timings at fixed concurrency and RTT. In particular, measure send
latency during concurrent search/catch-up and UI input-to-frame latency in a PTY.
Those are **new measurements**: percentiles of benchmark batch averages are not
request percentiles. Keep local/cold CLI, remote CLI and warm MCP as separate
series. Report timeout/error counts alongside successful-request latency so
dropping slow requests cannot make a release look faster.
