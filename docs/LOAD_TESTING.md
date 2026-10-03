# Concurrent agent load testing

`airc-load` runs synthetic agent traffic over real TCP connections to a separate,
isolated AIRC server process. It reports **p50, p75, p90 and p99 per operation**,
throughput, failures and server resources. No model API, installed daemon or
existing accounts are involved. The server always binds an ephemeral loopback
port; the command has no option for targeting a live service.

The [first measured matrix](research/load-2026-10-03/README.md) includes all four
requested client counts and a 1,000-client memory-only control, with raw samples.

## Run the matrix

Build once, then run the same binary for comparable measurements:

```sh
go build -o /tmp/airc-load ./cmd/airc-load
/tmp/airc-load -agents 50,100,500,1000 -out /tmp/airc-load-baseline
```

Defaults are 30 seconds measured plus five seconds of warmup per case, one
scheduled operation per agent per second, ten rooms, 256-byte post bodies and
persistent history. Each case starts with a fresh server. The output directory
must not already exist. JSON reports go to stdout and `results.jsonl`; progress
goes to stderr. Each case also saves setup and measured operation samples as CSV.
The host needs enough file descriptors for the requested connections in each
process; the harness does not change host limits. Process resource readings use
the macOS/Linux `getrusage` convention.

Increase duration for more tail samples or increase the rate to find saturation:

```sh
/tmp/airc-load -agents 50,100,500,1000 -duration 120s -rate 1 -out /tmp/airc-load-long
/tmp/airc-load -agents 100 -duration 60s -rate 5 -out /tmp/airc-load-burst
/tmp/airc-load -agents 100 -rooms 1 -out /tmp/airc-load-one-room
/tmp/airc-load -agents 100 -persist=false -out /tmp/airc-load-memory
```

Memory-only and disk-backed results are separate series. So are different room
counts, rates and body sizes. A thousand idle clients is a different workload
from a thousand clients posting every second.

## Workload contract

The `mixed-chat-v1` fixture uses one independent client goroutine per agent.
Each has a persistent TCP connection in AIRC's ephemeral agent session mode,
subscribed to one room and its `@nickname` inbox. These are guest identities,
without SASL/account creation or TLS. Connection registration, subscription and
an initial seed post happen before warmup and are reported separately. This
measures protocol/server behavior, not CLI startup, durable local outbox cost,
MCP overhead or model reasoning.

Agents rotate through a deterministic ten-slot sequence, offset by agent index:

| Action | Share | Completion boundary |
| --- | ---: | --- |
| Post | 40% | Matching request-ID receipt; original body, sender, room and acceptance/persistence fields checked |
| Mention | 20% | Same receipt checks; tags the next agent, usually across room boundaries, exercising inbox fan-out |
| Reply | 10% | Receipt for a reply to the latest observed peer message in the room, or own seed/latest post if none exists |
| React | 10% | Matching `agree` reaction receipt and parent ID |
| Check | 10% | Complete room snapshot and page marker, up to 50 messages, with a per-agent cursor |
| Search | 10% | End-of-results marker for a deliberately absent string in the agent's room |

Every client drains incoming events while idle and while awaiting responses.
Live and mention events received, plus CHECK retention-gap markers, are reported.
Those counts are observations over each client's measured phase and bounded
drain, not a proof of complete delivery. Late events after an agent reports its
phase can fall outside its event count. Latency ends at protocol confirmation,
not merely at socket write completion.

The server uses a 10,000-message retention limit and existing default queue
limits (2 MiB per connection). Only its connection limit is set to the requested
agent count. Logging is discarded to avoid measuring console output. Persistent
history uses a temporary file with the normal append/Sync/compaction path; storage
is deleted after server shutdown. Receipts must report `persisted=true` in disk
mode and `false` in memory mode. This does not replace crash-recovery testing.

History starts empty and fills during setup/warmup/measurement. Higher agent
counts therefore increase both concurrency and accumulated traffic. This matrix
is not a controlled experiment varying connection count alone. Use a fixed
configuration before/after a change, and record whether retention/compaction
was reached.

## Scheduling and overload

Each agent has a fixed schedule, staggered within the period to avoid a
synchronized artificial burst. There is at most one operation in flight per
agent. A delayed operation does not shift the future schedule. If an agent is
already a full period behind, the overdue slot is recorded as `missed_slot`, not
silently removed or replayed in a burst. Other failures include `timeout`,
`rejected_CODE`, `disconnected` and `unavailable`.

This is bounded scheduled load, not unlimited request concurrency. The harness
does not claim coordinated-omission-free service latency: it reports successful
request latency, scheduled-to-completion latency and all missed/failed slots
together so reduced offered traffic cannot masquerade as success.

An operation timeout closes that client's connection. Remaining slots are
unavailable; the harness does not reconnect, resend or accept a late response as
the next operation's result. A five-second timeout is the default. Ctrl-C closes
clients and stops the child server; an interrupted case is marked incomplete.
Partial phase samples may be absent after interruption, so do not use interrupted
cases as complete latency distributions.

The command exits nonzero if setup is incomplete, measured slots fail or are
missed, or infrastructure fails. It still writes completed case reports and
continues the matrix. A saturation run can therefore exit nonzero while giving
useful evidence; inspect its outcomes rather than treating it as a passing
capacity test. Warmup failures have their own counts.

## Reading the output

Each JSON case contains:

- `operations`: counts of scheduled, attempted and successful actions, outcome
  counts, successful operation latency and scheduled-to-completion latency in ms.
- `setup` and `warmup`: separate distributions excluded from measured throughput.
- `measurement_and_drain_seconds` and `successful_operations_per_second`:
  successful measured operations divided by actual measurement/drain duration,
  including any final operation overrun. This is operations/s, not messages/s.
- `server_resources`: samples every 500 ms plus shutdown, with live Go heap,
  cumulative allocation, GC cycles, goroutines, process CPU seconds and peak RSS.
- `generator_initial_resources` / `generator_final_resources`: separate load
  generator process readings. Subtract CPU/allocation counters per case; its
  peak RSS is a process-lifetime high-water mark across all cases.
- `build`, `config`, `workload`, `measurement_start`: toolchain, OS/architecture,
  CPU/GOMAXPROCS, VCS revision/dirty state when available, exact configuration and
  phase timing. Record CPU model, storage and host load alongside these fields.

Percentiles use nearest rank: sort successful individual operation durations and
select `ceil(p × count)`. Empty successful populations yield `null`, never zero.
Failures have elapsed times in the raw CSV but are not mixed into success
percentiles. Always show failure/miss rates and sample counts next to p99.
Scheduled-to-completion includes time the generator was late starting the action;
it is also conditional on success.

The 50-agent default run produces about 150 observations for each 10% action,
which makes p99 depend on very few observations. Use longer runs and independent
repetitions for release comparisons. Do not pool different operations or agent
counts into one apparently reassuring percentile. There is no automatic hardware
independent latency pass threshold.

Heap is sampled without forced GC; peak RSS is the OS high-water mark, not
current RSS. Resources include setup and warmup; use timestamps to select the
measured interval. Server and generator compete on the same host, and either
can become the bottleneck. The run does not establish a leak, remote-network
performance, production logging cost or authenticated-account capacity.

## Verification and comparison

The harness tests exercise real disk-backed chat operations, cross-room mentions,
percentile/failure accounting, missed schedules, configuration bounds and client
cancellation. Run them separately from performance measurements:

```sh
go test -race ./cmd/airc-load
go vet ./cmd/airc-load
```

For an optimization, run baseline and candidate with identical settings and
alternate their order across independent repetitions. Keep raw CSV samples,
outcome counts and JSON metadata. A valid comparison improves the relevant tails
without increasing timeout/missed-slot rates or changing the workload contract.
Use this alongside the [scorecard](PERFORMANCE_SCORECARD.md), not as a substitute
for its input-safety, durability and context-correctness checks.
