# Initial property-testing evidence — 2026-10-03

Environment: Apple M4, macOS arm64, Go `go1.25.0 darwin/arm64`.
Each campaign used `-fuzztime=30s -parallel=2`; see the
[reproduction commands and contracts](../../PROPERTY_TESTING.md).

| Target | Fuzz executions | Result | Raw output |
| --- | ---: | --- | --- |
| `FuzzHistoryRetentionAndPagination` | 120,582 | PASS | [history.log](history.log) |
| `FuzzCheckCursorPrefixes` | 187,437 | PASS | [check.log](check.log) |
| `FuzzContextBudget` | 46,396 | PASS | [context.log](context.log) |
| `FuzzRetainedRequestRecovery` | 175,180 | PASS | [retry.log](retry.log) |

Total: 529,595 executions. These are fuzz execution counts, not unique inputs or
independent proofs. Campaigns reused local interesting-input caches from earlier
runs; initial baseline counts therefore exceed the checked-in explicit seed
counts (three per target, except two for request recovery). No failing production
input was found, so no failure corpus was added.

`go test -race ./internal/server ./cmd/airc` passed ([output](race.log)).
After adding an explicit CHECK cutoff seed and strengthening history-record
comparisons, the final server race suite passed again
([output](server-race-final.log)); CLI tests were unchanged.
`go vet ./internal/server ./cmd/airc` and `git diff --check` also passed.

## Controlled mutation probes

Temporary Go `-overlay` files changed one production statement at a time without
editing production files in the checkout. Ordinary explicit-seed tests rejected:

- Removing request-index deletion on history eviction:
  `FuzzHistoryRetentionAndPagination` failed with
  `evicted request index retained`.
- Advancing a globally limited CHECK page cursor to the newest retained record:
  `FuzzCheckCursorPrefixes` failed with
  `cursor advanced past a deferred message`.

The second probe initially exposed a seed-coverage gap. Adding a seed with a
global budget below the per-target limit made that deliberately broken version
fail. The unmodified implementation then passed the final campaign above.

## Scope

This change adds tests and documentation only. Generated checks exercise bounded
in-memory histories, handlers and context serialization. They do not establish
crash durability, authentication-handshake correctness, network behavior,
throughput, or absence of races/leaks. Generated crash/restart and connection
lifecycle models remain on the roadmap. No service was restarted or deployed.
