# Independent HTTP fixture parallelism

> Dated evidence. Runs recorded here predate the
> [identity removal](removed-identity.md): their principals, signed or
> encrypted requests and return routes no longer exist.

Before source: `aad9068`. Three top-level tests now call `t.Parallel()`:
`TestHTTPDurableSubmissionStatusAndReplyOverTLS`,
`TestHTTPDenialsAndScopedLookup`, and
`TestHTTPExactOriginsPreflightAndBearerAuthority`. Their subtests retain
serial execution and all assertions are unchanged.

Each fixture owns its native daemon process, Unix socket, native history,
gateway journal, credentials, observer, worker, HTTP server and ephemeral port.
Matching author/room names belong to separate daemon instances. Only the
checksum-verified immutable pinned executable is shared, behind `sync.Once`;
its root is removed by `TestMain` after all tests complete. No process-wide
environment or working-directory mutation was introduced. A process check
found no orphaned private gateway test processes before measurement.

Compiled serial/pilot binaries ran exactly those three tests with
`-test.count=3 -test.parallel=3 -test.timeout=120s`, from the integration
directory. Six paired samples alternated order, with no competing validation
or timing process. All twelve runs passed. Serial median was 3.681465 seconds;
pilot median was 3.485969 seconds, about 5.3 percent lower. The pilot was faster
in all six pairs. Each invocation includes pinned fixture compilation and
cleanup; this is a measured improvement for the selected tests, not evidence
of a full-suite speedup.

The changed tests and `TestPinnedFixtureSharesOnlyImmutableBinary` passed
together under `go test -race ./integration/gateway` with `-count=10`,
`-shuffle=42`, `-parallel=3` and `-timeout=180s` in 17.280 seconds.
`go vet ./integration/gateway`, formatting and diff checks passed.
Production deadlines, native fsync, gateway durability and cleanup checks
are unchanged. Fresh final full acceptance remains on the roadmap.

Raw evidence remains under `/tmp/airc-roadmap-baseline`:
`http-before.test`, `http-parallel.test`, `http-paired-samples.json`, and
`http-parallel-race.out`.
