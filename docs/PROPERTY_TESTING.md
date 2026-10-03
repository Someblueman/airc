# Property-based testing

Use Go's native fuzzing to generate bounded inputs and shrink failing cases.
Ordinary `go test` runs explicit seeds and any checked-in regression corpus;
`-fuzz` additionally explores mutations for the specified duration. No additional
testing dependency is required.

| Target | Coverage and oracle |
| --- | --- |
| `FuzzHistoryRetentionAndPagination` | Posts, replies, corrections/retractions, quota changes, wraparound and eviction. Compare retained records and filtered history with an independent chronological-list model; verify ID/request indexes and expired cursors. Bounds: 128 operations, 0–16 retained records. |
| `FuzzCheckCursorPrefixes` | Overlapping room/inbox/DM selectors, own-message filtering, expired and valid cursors, per-target/global limits. Each target receives a contiguous eligible prefix; cursors never pass deferred messages; repeated pages drain without repeating records within a selector. Output is ordered/deduplicated within each response. Bounds: 48 input records, 1–16 retained records, four selectors. |
| `FuzzContextWireBudget` | Compare server byte admission with repeated full-wire serialization and record removal; check exact-fit and failed protected-record budgets, including summary, terminator and CRLF. Bounds: 32 records and 128-byte generated text. |
| `FuzzContextBudget` | Unicode/JSON escaping, protected records, pins/profiles, exact byte boundaries and omission-counter digit changes. Compare the optimized trimmer with the previous repeated-serialization policy and check byte/count/protection invariants. Bounds: 24 records, 128-byte text. |
| `FuzzRetainedRequestRecovery` | Actual post/receipt handlers with generated already-authenticated identities, guest case folding, account scope, duplicate/conflicting request IDs, multiline bodies and eviction. A simple list records accepted requests; recovery never posts, retained duplicates return the original ID/content, and conflicts fail. Bounds: 96 operations, 1–8 retained records. |
| `FuzzBracketedPasteBoundaries` | Existing terminal-paste property: arbitrary read partitions preserve printable text and never execute pasted submit/control keys. |
| `FuzzSearchText` | Existing search property: compare the optimized matcher with lowercase substring matching. |

The history oracle uses slices and direct predicates, not production ring offsets,
ID lookups, cached mentions or eviction helpers. Context testing deliberately uses
the old implementation as a differential oracle; the independent size, omission
and protection assertions complement it. Receipt generation tests identity scope
after authentication, not the authentication handshake itself.

Idempotency is bounded by retention. Once a request is evicted, `RETRY` reports an
unknown outcome and does not post. An explicit new send reusing that evicted key
can create a new message. The tests must not assert eternal deduplication.

## Running

```sh
# Seeds/regressions and existing failure-boundary tests.
go test -race ./internal/server ./cmd/airc
go vet ./internal/server ./cmd/airc

# One exact target per fuzz invocation; bounded workers/time.
go test ./internal/server -run '^$' -fuzz '^FuzzHistoryRetentionAndPagination$' -fuzztime=30s -parallel=2
go test ./internal/server -run '^$' -fuzz '^FuzzCheckCursorPrefixes$' -fuzztime=30s -parallel=2
go test ./internal/server -run '^$' -fuzz '^FuzzContextWireBudget$' -fuzztime=30s -parallel=2
go test ./cmd/airc -run '^$' -fuzz '^FuzzContextBudget$' -fuzztime=30s -parallel=2
go test ./internal/server -run '^$' -fuzz '^FuzzRetainedRequestRecovery$' -fuzztime=30s -parallel=2
```

Go saves minimized failures beneath the affected package's
`testdata/fuzz/FuzzTargetName/`. Keep reproducible failing seeds with the fix and
rerun the exact seed, then the bounded campaign. Interesting non-failing inputs
also live in Go's local fuzz cache, so execution counts and initial coverage can
vary between runs. Record Go version, duration, worker count and raw output.

[The initial run evidence](research/properties-2026-10-03/README.md) includes two
controlled mutation checks: the tests reject stale request indexes and cursors
that skip deferred records. The overlays used for these checks never modify the
checkout's production code.

## Remaining work

Generated crash/restart and persistence-fault sequences, authentication changes,
connection/subscription lifecycle models, cancellation and slow-reader scenarios
remain separate work. Existing real-network, crash/recovery, PTY and resource-soak
tests still apply. These fast fuzz targets do not measure throughput or prove
absence of races/leaks. No CI workflow is added here; seeds run wherever ordinary
Go tests already run, and a future CI fuzz job needs an explicit bounded budget.
