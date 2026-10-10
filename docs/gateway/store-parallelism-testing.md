# Gateway store fixture parallelism pilot

Before source: `b036fa1`. The pilot added `t.Parallel()` to the six tests in
`acceptance_test.go` and `files_test.go`. Each owns a separate fixture directory,
credentials, journal, connections and triggers. Reopen, acceptance fencing,
revocation/expiry, failed-save immutability, ownership, schema-version rejection,
private files, lost connections and symlink protection assertions were retained.
Migration, subprocess and checkpoint fixtures kept serial scheduling. No
durability setting or production code changed.

Compiled serial/pilot binaries ran the entire store package in four paired
samples, alternating order, with
`-test.count=3 -test.parallel=4 -test.timeout=120s`. No competing validation
or benchmark ran during measurement. All eight runs passed. Serial median
was 16.104728 seconds; pilot median 16.136268 seconds, about 0.2 percent slower.
Serial range was 15.901060–16.508810 seconds; pilot range was
15.871142–16.389982 seconds. The pilot was faster in two of four pairs.
There is no measured stable improvement, so the six tests remain serial.

The pilot passed
`go test -race ./internal/gateway/store -count=3 -shuffle=42 -parallel=4 -timeout=120s`
in 77.120 seconds, including unchanged durable, migration and process-death
checks. The scheduling additions were removed completely; final Go source
matches the before source. Diff checks passed.

Raw evidence remains under `/tmp/airc-roadmap-baseline`:
`store-before.test`, `store-parallel.test`, `store-paired-samples.json`, and
`store-parallel-race.out`. The bounded store pilot is complete; fresh final
full acceptance remains on the roadmap.
