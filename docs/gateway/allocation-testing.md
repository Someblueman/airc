# Fixture binary hash allocation

Before source: `f7186dd`. A compiled integration workload ran
`NativeSendAndReads|^TestHTTPDurableSubmissionStatusAndReplyOverTLS$` with
`-test.count=2 -test.parallel=3 -test.timeout=180s`, from the integration
directory, collecting CPU and allocation profiles. It passed. No competing
validation or benchmark ran during profiling or the timing comparison.

The allocation profile attributed 38.51 MB cumulatively to
`buildCurrentGateway`, which read the complete executable into memory solely
to calculate SHA-256. Total sampled allocation was 152.22 MB. The fixture now
opens the same owner-only executable and streams it through SHA-256 with
`io.Copy`, retaining read and close errors. The digest still covers every byte;
build instrumentation, permissions, lifetime ownership and synchronization
are unchanged. No production or secure-file path was changed.

The same after workload passed. Its sampled total was 97.53 MB and the large
binary read no longer appeared. Sampling and transport variation affect total
allocation; the supported improvement is removing the whole-file hash buffer,
not attributing the entire total difference to this change. CPU sampling was
dominated by syscalls and waits and did not demonstrate a CPU improvement.
The installed Go tool lacked a built `pprof`; analysis used `go run cmd/pprof`
from its installed source without dependency changes.

Unprofiled serial before/after runs of the identical workload passed in
13.806598 and 14.239518 seconds wall. This does not demonstrate a wall-time
improvement. Raw outputs, timings and profiles remain under
`/tmp/airc-roadmap-baseline`: `allocation-before.test`, `allocation-after.test`,
`gateway-profile-before.*`, `gateway-profile-after.*`,
`gateway-allocation-before.pprof`, `gateway-allocation-after.pprof`,
`gateway-cpu-before.pprof`, `gateway-cpu-after.pprof`, the saved `*-top.txt`
summaries, and `allocation-timing.json`.

The same carrier tests and `TestCurrentGatewayBinaryKeepsFixturesIsolated`
passed together under the race detector in 39.225 seconds with `-count=1`,
`-parallel=3` and `-timeout=180s`. During that run, the isolation test also
temporarily compared the streamed digest with `sha256.Sum256(os.ReadFile(binary))`;
the check passed and was removed to avoid reintroducing the large allocation.
Permanent isolation assertions remain unchanged. Race output is in
`allocation-race.out`. Vet, formatting and diff checks passed. Fresh final
full acceptance remains on the roadmap.
