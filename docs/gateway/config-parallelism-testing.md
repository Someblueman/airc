# Gateway config parallelism pilot

At source `427b162`, the seven tests in `config_test.go`, `webrtc_test.go`
and `unix_test.go` use local config values and per-test temporary directories.
They do not change the process environment or working directory. The pilot
added `t.Parallel()` to those tests, retaining all parsing, identity-reference,
private-file, symlink, missing-file and listener-validation assertions.
Other tests retained their scheduling.

Compiled serial/pilot binaries ran the entire config package with
`-test.parallel=4 -test.timeout=120s`. Paired runs alternated order and no
competing validation or timing process ran. All samples passed:

| Repetitions per run | Paired samples | Serial median | Pilot median |
| --- | --- | --- | --- |
| 200 | 8 | 1.656454 s | 1.571090 s |
| 1000 | 6 | 8.857371 s | 9.237722 s |

The short comparison suggested about five percent improvement, but the longer
comparison was about four percent slower. Variation was substantial in both.
This does not demonstrate a stable improvement; the seven tests remain serial.
No production code, fixture isolation or secure-file checks changed.

The pilot passed
`go test -race ./internal/gateway/config -count=10 -shuffle=42 -parallel=4 -timeout=120s`
in 1.747 seconds. Raw binaries and evidence remain under
`/tmp/airc-roadmap-baseline`: `config-before.test`, `config-parallel.test`,
`config-paired-samples.json`, `config-long-paired-samples.json`, and
`config-parallel-race.out`. The bounded pilot is complete and no parallelism
change is retained.
