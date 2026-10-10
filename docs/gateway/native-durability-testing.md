# Individually scoped native fixture durability

Before source: `25297de`. Only
`TestHistoryBackendRoomPagesGapAndRevocation` now selects the pinned native
daemon's existing `--sync none` mode. It checks live bounded pages, exact
author/account/body/reply metadata, caught-up cursor position, in-memory
retention gaps and current authority. It neither restarts the native daemon
nor asserts stable-storage receipt facts. All assertions and deadlines remain.

`fixture` and `fixtureWithAdmin` still choose `fsync`. Their common implementation
now accepts an explicit native mode, so this one caller can select `none`
without changing other fixtures. Gateway journal FULL synchronization is
unchanged. Native persistence/restart tests retain their existing mode. The
pinned source supports `none`, `fsync` and `full`; no daemon feature was added.

Serial compiled before/after runs from the integration directory retained
`-test.timeout=120s`. The first ten-repeat comparison passed in 1.044379 and
1.527589 seconds wall. Four longer paired samples, alternating order and using
`-test.count=100`, all passed. Medians were 4.079144 seconds before and
4.018180 seconds after; only two of four pairs favored after. This does not
establish a stable wall-time improvement. The retained change explicitly
selects the lighter mode for a fixture with no native persistence claim.
No competing validation or benchmark ran during timing.

An earlier candidate used `none` in the three backend wire-deadline cases.
Its two-repeat comparison passed in 8.156308 and 9.016679 seconds without
benefit, so that candidate was removed and those fixtures retain `fsync`.
Raw evidence is under `/tmp/airc-roadmap-baseline`: `durability-before.test`,
`durability-after.test`, `durability-history-after.test`,
`durability-timing.json`, `durability-history-timing.json`, and
`durability-history-paired.json`.

The changed history test, unchanged wire-deadline test and pinned fixture
isolation test passed together under race detection with `-count=2`,
`-shuffle=42` and `-timeout=180s` in 10.483 seconds. The selection also contained
an unmatched CLI-history name; no CLI-history qualification is claimed by that
run. Output is in `durability-history-race.out`. Vet, formatting and diff
checks passed. Fresh final full acceptance remains on the roadmap.
