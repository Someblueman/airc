# Load-generator test schedule

Before source: `025e7c0`. `TestAgentsExerciseRealChatAndCancel` now uses a
500-millisecond measured phase at twenty operations per second per agent,
instead of one second at ten. Both schedules produce ten slots for each of
four agents: forty operations over two rooms with the same rotating mix.

The real native server, persisted-message request, complete six-operation
coverage, successful/scheduled counts, eight mention deliveries, receipt
validation, healthy-client assertions and active-client cancellation check
remain unchanged. Start lead, one-second operation timeout, five-second
setup/result bounds, final event-drain allowance and two-second cancellation
bound remain unchanged. Production workload defaults and scheduling code
were not changed.

Compiled before/after binaries ran the actual changed test serially with
`-test.count=10 -test.timeout=120s`; no competing validation or benchmark ran.
Both passed. Wall times were 12.049780 seconds before and 7.020103 seconds
after, consistent with removing half a second of schedule wait per repetition.
This is test-time improvement, not production throughput evidence.

The entire `cmd/airc-load` package passed with
`go test -race ./cmd/airc-load -count=20 -shuffle=42 -timeout=120s`
in 15.611 seconds, including idle/in-flight reset, timeout/cancellation
classification, missed-slot and workload-boundary checks. Vet, formatting
and diff checks passed. Raw evidence remains under
`/tmp/airc-roadmap-baseline`: `load-before.test`, `load-after.test`,
`load-timing.json`, and `load-race.out`. Fresh final full acceptance remains
on the roadmap.
