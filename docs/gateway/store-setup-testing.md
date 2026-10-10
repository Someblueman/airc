# Fresh store initialization measurement

At source `484abc0`, a temporary store-package test measured actual `Open`
calls in fifty separate private fixture directories. Each pair opened a fresh
journal, closed it, reopened the same journal, and closed it again. Credential
fixture setup was outside the timed region. Timers covered `Open` only,
excluding subsequent checks and close/cleanup. No competing validation or
benchmark ran.

| Open mode | Samples | Median milliseconds | Range milliseconds |
| --- | --- | --- | --- |
| Fresh | 50 | 14.234750 | 9.004583–32.213500 |
| Reopen | 50 | 8.502666 | 6.244792–14.855291 |

Every open succeeded and independently reported the current schema version
and `PRAGMA synchronous=2` (FULL). Every close and fixture cleanup succeeded;
the compiled measurement process exited 0. The command was
`store-setup.test -test.run=^TestMeasureStoreSetup$ -test.v -test.count=1 -test.timeout=120s`.

The approximately 5.7-millisecond median gap includes first-use protected
files and WAL setup as well as schema/migration work. This does not isolate
schema creation as a material bottleneck or justify a fresh-schema rewrite.
Initialization remains unchanged, preserving the shared fresh/migrated path,
transactional migrations, private-file checks and durability. Existing
`TestJournalMigrations` covers versions 1–9 and remains unchanged.

The temporary measurement source was removed, leaving permanent Go source
unchanged. Raw evidence is retained under `/tmp/airc-roadmap-baseline`:
`store_setup_measurement_test.go`, `store-setup.test`, `store-setup.out`,
`store-setup.stderr`, and `store-setup-samples.json`. Diff checks passed.
The measurement and conditional consolidation decision are complete; fresh
final full acceptance remains on the roadmap.
