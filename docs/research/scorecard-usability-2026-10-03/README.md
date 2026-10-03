# Input safety, human workflow and resource-soak follow-up

Follow-up to `27f493d`, measured on the same Apple M4 / macOS arm64 / Go 1.25.0
host. The production service was not restarted, installed over or exercised.
Servers listen on isolated ephemeral loopback ports and use temporary state.

## Terminal and conversation acceptance

The terminal decoder now keeps incomplete UTF-8 and escape sequences between
reads. Bracketed paste produces text, including newlines/tabs, and cannot produce
submission/control keys. The composer displays `↵` and `⇥` without emitting those
control characters into its input row. A bare Escape has a 200 ms disambiguation
timeout; incomplete CSI sequences and pasted text are bounded.

Message/reply drafts remain pending until acknowledgement. Local validation,
server rejection, offline submission and a full command queue preserve the draft.
A lost receipt leaves a recovery ID; Enter asks for that receipt and never reposts
the body. The integration test drops an actual server receipt and independently
checks that only one message exists after recovery. This UI state is in memory;
crash-persistent drafts and sharing the CLI's durable outbox remain roadmap work.

Ctrl-Up/Down selects a message, Ctrl-O retrieves context including pins,
corrections and omission/missing counts, and Ctrl-R chooses that exact message as
the reply target. The target cannot drift when newer traffic arrives. Context
subscribes before retrieval and forwards unrelated live events. `/context ID`
provides the same view. The snapshot requests at most 100 conversation records;
retained records omitted by that limit are explicitly counted.

[`pty-trials.log`](pty-trials.log) records **5/5 successful attempts**, each using
three deliberate actions after the question is selected: open context, choose
reply, submit. The peer response appears in the same view. Each test checks the
correction and pin, exact accepted body, exact reply target, exactly one workflow
post and peer visibility through the real binary on a PTY. It then checks a
byte-by-byte multiline Unicode paste, zero posts before Enter, editing a rejected
4097-byte draft down to an accepted 4096-byte body, and terminal restoration.

The recorded scripted intervals are 0.076–0.099 seconds. They include automatic
CLI audit/peer-fixture work and do **not** measure a person's reading or reasoning,
first-time discovery, or human completion time. Answer content is deterministic.

[`ui-checks.log`](ui-checks.log) records the targeted UI suite. Native fuzzing in
[`paste-fuzz.log`](paste-fuzz.log) completed 11,378 executions in its bounded
30-second run, with no discovered failing case (four explicit initial seed cases, in
`ui_input_safety_test.go`). Exhaustive split-point tests and
one-byte chunks cover the mixed Unicode/escape/paste fixture. Existing differential
render checks still pass. [`redraw.log`](redraw.log) records a median **0.295 ms /
0.648 MB** per invalidated 500-record redraw, inside the unchanged 5 ms / 1 MB goal.
Those timings ran on a shared host during the soak; they are regression checks,
not an isolated paired comparison with the prior run.

## Resource-soak fixture and predeclared budget

`TestResourceSoak` launches a separate server process so client allocations are
not attributed to the server. Ordinary tests skip it unless explicitly enabled.
One cycle opens a root writer, 50 concurrent writers posting 20 roughly 4 KiB
replies each, then (after those writers finish) five concurrent context readers
requesting 1000 records from
the resulting 1001-record thread. Request IDs are unique per cycle; every receipt
and every complete context response is required. All clients close after each
cycle. History persists to disk, compacts normally, and retains 2000 messages.

A fill cycle precedes five saturated calibration cycles. Each checkpoint waits
for zero server clients, idles for 500 ms, forces GC, then samples server live
heap, goroutines, descriptors and queued bytes. Client heap/goroutines are sampled
separately after client GC. Descriptor counts include the temporary descriptor
used to enumerate `/dev/fd`, consistently at every checkpoint. Queued bytes are
quiescent measurements, not peak queue occupancy or RSS. RSS and transient peak
heap were not sampled by this fixture; this run tests retained-resource recovery.

Before the timed phase, freeze the largest calibration heap plus the greater of
1 MiB or three times the calibration heap range. The 1 MiB floor is an engineering
allowance for runtime/cache variation, not a confidence interval. Goroutine and
descriptor ceilings are the respective calibration maxima plus one; clients and
queued bytes must return to zero and history must stay saturated. Calibration
repeats identical cycles in this process; it does not characterize cross-host or
cross-process variance. Every timed checkpoint is checked against the same
frozen ceilings, not a moving baseline.

For this run the warm heap maximum was **11,267,728 bytes**, warm range **87,568
bytes**, frozen heap ceiling **12,316,304 bytes**, goroutine ceiling **4** and
descriptor ceiling **9**. The timed workload runs for at least 30 minutes after
calibration. The last cycle may finish slightly after that duration.

The fixture explicitly uses a **16 MiB per-connection outbound allowance**, as
in the earlier large-context benchmark. A smoke run with the default 2 MiB
allowance disconnected a large-context reader. Small CLI output budgets do not
bound the upstream context response. The soak must not be read as proof that the
default queue setting supports five simultaneous maximum-sized context responses.
A negotiated upstream response budget remains roadmap work.

**Passed:** 376 timed cycles over 1803.37 seconds after calibration. Including
fill and calibration, the workload accepted 382,382 posts, completed 1,910 large
context requests and opened/closed 21,392 client connections. No operation error
or frozen resource-budget violation occurred.

| Server resource | Warm baseline | Timed maximum | Final | Frozen ceiling |
| --- | ---: | ---: | ---: | ---: |
| Post-GC live heap | 11.268 MB | 11.399 MB | 11.332 MB | 12.316 MB |
| Goroutines | 3 | 3 | 3 | 4 |
| Descriptors | 8 | 8 | 8 | 9 |
| Queued bytes after drain | 0 | 0 | 0 | 0 |
| Connected clients after drain | 0 | 0 | 0 | 0 |

Final server heap was **63,800 bytes (+0.064 MB)** above the maximum warm sample.
The descriptive least-squares slope across all timed checkpoints was +1,636
bytes/minute; over the final ten minutes it was −1,038 bytes/minute. These slopes
are not significance tests. Client live heap ended at 586,560 bytes versus a
494,352-byte warm maximum, with three goroutines throughout. All samples remained
within the predeclared server ceilings; this bounded fixture did not show
continuing retained-resource growth. It is not a universal leak-free guarantee.

[`resource-soak.log`](resource-soak.log) retains every checkpoint and the frozen
budget. [`results.json`](results.json) records machine-readable totals, bounds,
final-minus-warm deltas and trends. Slopes use elapsed checkpoint minutes and
ordinary least squares, with the late window selected by the final 600 seconds.
RSS, transient peak heap, remote traffic and production behavior remain unmeasured.

The final full `go test -race ./...` passed ([raw output](race.log)); `go vet ./...`
and `git diff --check` also exited successfully.

## Reproduction

```sh
go test ./cmd/airc -run 'TestUI|TestTerminalRead|TestPasteDraft|TestTyping|TestSlash|FuzzBracketed' -count=1 -v
go test ./cmd/airc -run '^TestUIRealPTYConversationAndInputSafety$' -count=5 -v
go test ./cmd/airc -run '^$' -fuzz '^FuzzBracketedPasteBoundaries$' -fuzztime=30s -parallel=2
go test ./cmd/airc -run '^$' -bench '^BenchmarkUIAppendRedraw$' -benchmem -benchtime=1s -count=3
AIRC_SOAK_DURATION=30m go test ./internal/server -run '^TestResourceSoak$' -count=1 -timeout=40m -v
go test -race ./...
go vet ./...
```

The PTY test needs Python 3 and skips explicitly if it is unavailable. The soak
uses `/dev/fd` and is intended for this Unix host. A shorter duration can smoke-test
the harness but does not satisfy the 30-minute resource guardrail.
