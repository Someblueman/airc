# Scorecard optimization evidence

The candidate committed with this report optimizes context budgeting, metadata
serialization, server CHECK/search and retained UI rendering. The original
[scorecard baseline](../performance-2026-10-03/scorecard.csv) remains unchanged.
See [the scorecard](../../PERFORMANCE_SCORECARD.md) for targets and current values.

## Measurement boundary

Baseline is `90814e5`; candidate is that parent plus the source changes committed
with this report. [environment.json](environment.json) records the candidate
source fingerprint, Go version and host. Both builds used the original
`roadmap-2026-10-03-v1` fixtures on Apple M4, 24 GiB RAM, macOS arm64, Go 1.25.0,
GOMAXPROCS 10. No installed binaries or production services were changed.

Five rounds ran serially, reversing baseline/candidate order each round. Each
build ran server, UI, context and real-CLI cases in that order. Microbenchmarks
used at least one second per case per round; real CLI cases used 100 iterations.
The context fixture makes two calls at each of limits 50, 500 and 1,000, yielding
ten individual calls per limit per build. Baseline and candidate used identical
inputs, retention, queue size, account modes and durability settings.

The host was otherwise uncontrolled. These are medians of run means, except
context (median of individual calls); they are not request p95/p99 values or WAN
guarantees. Timing excludes race/fuzz/profile instrumentation. Original baseline
results and exploratory tuning runs are not mixed into this comparison.

## Evidence

- [comparison.csv](comparison.csv): baseline and candidate medians, ranges,
  relative changes and target comparisons, including all three context limits.
- [baseline-server.txt](baseline-server.txt), [candidate-server.txt](candidate-server.txt):
  search and CHECK, including smaller diagnostic cases.
- [baseline-ui.txt](baseline-ui.txt), [candidate-ui.txt](candidate-ui.txt):
  cached and invalidated rendering for short and long bodies.
- [baseline-context.txt](baseline-context.txt), [candidate-context.txt](candidate-context.txt):
  all context calls, allocation deltas and output sizes.
- [baseline-cli.txt](baseline-cli.txt), [candidate-cli.txt](candidate-cli.txt):
  real-process login/send/check/wake measurements, including local durability.
- [candidate-append.txt](candidate-append.txt): additional actual append/evict/redraw
  benchmark on a full 500-message buffer; this is a separate diagnostic fixture,
  not substituted for the original version-invalidation benchmark.
- [workflow.txt](workflow.txt): five scripted real-stdio MCP attempts. Each uses
  exactly `context`, `send`, `check`, without retry or participant-side polling.
- [verification.txt](verification.txt): race/vet outcomes and search fuzz results.

The short-body cached-redraw diagnostic initially rose from 127 to 158 microseconds
(24.7%), with overlapping ranges. A focused five-round alternating repeat gave
193 versus 190 microseconds: the higher candidate median did not reproduce.
[Repeat summary](cached-repeat.csv), [baseline](baseline-cached-repeat.txt) and
[candidate](candidate-cached-repeat.txt) retain that check separately, without
replacing the original samples. This does not establish a small speed ranking.
The actual append/evict/redraw diagnostic had a 0.528 ms median and approximately
0.610 MB allocated per update, also below the redraw budgets.

## What changed and what remains

Context uses exact encoded record sizes to remove complete records in the same
priority order, rather than serializing the shrinking conversation repeatedly.
Omission counters, their changing decimal widths, protected original/correction
IDs and the final newline are included in the budget. Nested metadata encoding
and decoding no longer round-trip large buffers through strings; the published
base64 JSON wire representation is unchanged.

CHECK performs one scan per cursor selector and examines fields in place before
copying matching messages. Search avoids per-body lowercase allocations for
ASCII input and skips eight bytes at a time when none can begin a match. It uses
portable Go integer operations and bounds prefix comparisons before falling
back to the standard library. Unicode retains the original `ToLower` semantics;
this benchmark does not establish the same speedup for Unicode-heavy searches.

UI rendering retains individual rendered records alongside their renderer state
and mutable correction annotations. A rebuild reuses only matching entries and
replaces the cache with entries for the currently retained items. It remains
bounded by the 500-item buffer. Rendering a wholly new buffer or resizing can
still require formatting all records; that is distinct from ordinary updates.

Correctness tests compare context bytes with the original algorithm across
budgets, escaping and counter boundaries; compare cached rendering with a fresh
render after corrections, retractions, eviction, history insertion and resizing;
and preserve nested metadata wire bytes. Existing recovery, pagination, durable
receipt and restart tests also run in the full race suite. Search is checked
against `strings.Contains(strings.ToLower(body), strings.ToLower(query))`,
including invalid UTF-8, Unicode and repetitive prefixes, plus a separate fuzz
run. These checks support the covered behavior, not a universal proof.

The conversation scenario is `conversation-effort-v1`: a question, obsolete
answer, correction, relevant pin and ten unrelated posts. An authenticated
participant retrieves context, constructs the answer from the correction and
pin, replies to the question and observes a subsequent peer reply. Independent
history inspection checks that exactly one participant post was accepted. Setup
and the independent audit are excluded from participant calls. Returned bytes
count the UTF-8 JSON encoding of each complete MCP CallToolResult, including
both text and structured content, excluding JSON-RPC framing. This is a
deterministic adapter check, not a model reasoning trial or human usability test.

The 30-minute resource soak, simultaneous large-context retrieval, measured human
workflow, terminal input safety fixes and remote tests remain outstanding. No
leak-free claim follows from these allocation measurements. UI cache memory is
bounded per buffer but is not a process-wide memory ceiling. Small context output
budgets still do not reduce upstream wire bytes. The persistent-load bottleneck
seen at 500/1,000 agents is a separate throughput problem; durability was not
weakened to reach these single-command goals.

## Reproduction

Use the unchanged probe files and overlay creation recipe in the
[original evidence README](../performance-2026-10-03/README.md#reproduction).
Build standalone test binaries with that overlay and a CLI binary for each
revision before running measurements. Use a separate source snapshot for the
baseline; do not switch a dirty checkout beneath a running benchmark.

```sh
go build -o "$bench_dir/airc" ./cmd/airc
go test -overlay "$probe_dir/overlay.json" -c -o "$bench_dir/server.test" ./internal/server
go test -overlay "$probe_dir/overlay.json" -c -o "$bench_dir/cli.test" ./cmd/airc

# In each of five alternating baseline/candidate rounds:
"$bench_dir/server.test" -test.run '^$' -test.bench '^BenchmarkRoadmap(Search|Check)$' -test.benchmem -test.benchtime 1s
"$bench_dir/cli.test" -test.run '^$' -test.bench '^BenchmarkRoadmapUI$' -test.benchmem -test.benchtime 1s
"$bench_dir/cli.test" -test.run '^TestRoadmapContextProbe$' -test.v
AIRC_BENCH_BINARY="$bench_dir/airc" "$bench_dir/cli.test" -test.run '^$' -test.bench '^BenchmarkAgentCLI$' -test.benchtime 100x

# Separately, on the candidate:
go test ./cmd/airc -run '^TestConversationEffortThreeMCPCalls$' -count 5 -v
go test ./cmd/airc -run '^$' -bench '^BenchmarkUIAppendRedraw$' -benchmem -benchtime 1s -count 5
go test -race ./...
go vet ./...
go test ./internal/server -run '^$' -fuzz '^FuzzSearchText$' -fuzztime 15s -parallel 2
```
