# Gateway core parallelism pilot

At source `e5bdac5`, the seven top-level tests in
`internal/gateway/core/request_test.go` use local request values and no mutable
global state, environment changes or filesystem fixtures. The pilot added
`t.Parallel()` to those seven tests, leaving their assertions unchanged.

Compiled before/pilot binaries ran the entire core package with
`-test.count=1000 -test.parallel=4 -test.timeout=120s`. Eight paired samples
alternated before/pilot order; all passed. No competing test or benchmark ran.
Serial median was 2.383605 seconds, pilot median 2.380677 seconds: about
0.12 percent, well within the observed variation. Serial range was
1.433571–2.533645 seconds; pilot range was 1.667367–2.656380 seconds.
An earlier seven-sample serial series also varied substantially and is retained.
This does not demonstrate a stable improvement, and ordinary single package
execution is already short. The seven tests remain serial.

The pilot passed shuffled race verification:
`go test -race ./internal/gateway/core -count=10 -shuffle=on -parallel=4 -timeout=120s`
in 1.450 seconds. An initial attempt failed during build-cache access under the
sandbox, before tests ran; the retry with cache access passed. No production
code, deadlines, test claims or durability settings changed.

Raw binaries, timings and output are retained under
`/tmp/airc-roadmap-baseline`: `core-before.test`, `core-parallel.test`,
`core-before-samples.json`, `core-paired-samples.json`,
`core-parallel-race.out`, and `core-parallel-race-retry.out`.
The roadmap pilot is complete; no parallelism change is retained.
