# Gateway auth fixture parallelism

Before source: `b7d6ae0`. Four Unix auth tests now call `t.Parallel()`:
bearer resolution/permissions, rotation/revocation/ambiguity, credential
creation/no-overwrite, and unsafe-file/malformed-content rejection.
Each owns a separate `privateTemp(t)` directory with mode 0700. Credential
files, verifiers, permission snapshots, symlinks and FIFO paths remain private
to that test. No shared credential material or process environment is changed.
Only these four tests opt in, so this pilot permits at most four simultaneous
top-level tests even when the runner's parallel limit is higher.

Compiled serial/pilot binaries ran the whole auth package in six paired samples,
alternating order, with `-test.count=100 -test.parallel=4 -test.timeout=120s`.
No competing validation or benchmark ran during measurement. All twelve runs
passed. Serial median was 18.303017 seconds; pilot median 17.407291 seconds,
about 4.9 percent lower. The pilot was faster in five of six pairs.
Serial range was 15.937959–19.237169 seconds; pilot range was
15.526399–18.688992 seconds. This supports retaining the small scheduling
change, but repeated-run throughput does not establish a full-suite speedup.

`go test -race ./internal/gateway/auth -count=10 -shuffle=42 -parallel=4 -timeout=120s`
passed in 5.630 seconds. `go vet ./internal/gateway/auth`, formatting and diff
checks passed. Production code, secure-file checks, randomness, credential
durability, assertions and deadlines are unchanged. Initial baseline compilation
was denied build-cache access before tests ran; compilation with access passed.

Raw evidence remains under `/tmp/airc-roadmap-baseline`:
`auth-before.test`, `auth-parallel.test`, `auth-paired-samples.json`, and
`auth-parallel-race.out`. Fresh final full acceptance remains required on the
roadmap.
