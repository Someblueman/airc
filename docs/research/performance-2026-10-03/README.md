# Performance investigation evidence

These measurements support the [roadmap](../../PERFORMANCE_ROADMAP.md). Source
baseline: `9b303ae`, 3 October 2026. Host: Apple M4, 24 GiB RAM, macOS arm64,
Go 1.25.0, default GOMAXPROCS (benchmark suffix 10). The application source was
not edited. Servers were isolated loopback fixtures; the live daemon was not
restarted or benchmarked.

## Evidence files

| File | Measurement boundary |
| --- | --- |
| [cli.txt](cli.txt) | Existing real-process CLI benchmark. Three runs of 20 operations per case. Authenticated login/send/check include startup and local filesystem work. Wait wake starts after the initial snapshot and includes an SDK post, server Sync and waiting guest CLI exit. |
| [history.txt](history.txt) | Existing ring query benchmark, five runs of at least 250 ms per case. |
| [server-probes.txt](server-probes.txt) | Ring insertion with/without quotas; CHECK handler encoding/enqueue; search. Three runs of at least 150 ms per case, excluding network/disk. |
| [ui-probes.txt](ui-probes.txt) | UI model rendering and parser observations. Three runs. Rendering excludes terminal writes and terminal emulator work. |
| [context.txt](context.txt) | End-to-end `runContext` over loopback, with embedded server in the same process. Two calls at each of three limits; process-wide TotalAlloc deltas and wall time. |
| [context-alloc-top.txt](context-alloc-top.txt) | Sampled allocation-space profile from a separate run of the complete context fixture, including setup and all six calls. |
| [context-cpu-top.txt](context-cpu-top.txt) | CPU profile from that separate profiling run. |
| [server_probe_test.go.txt](server_probe_test.go.txt) | Package-internal server probes, loaded through a temporary Go overlay. |
| [ui_probe_test.go.txt](ui_probe_test.go.txt) | Package-internal UI/context probes, loaded through the same overlay. |

The `.go.txt` files are deliberately outside the regular test suite. They are
investigation fixtures rather than permanent regression tests or acceptance
assertions. Formatting was normalized after measurement, so logged source line
numbers refer to the original temporary copies.

## Interpretation limits

- Reported benchmark ns/op values are per-run averages. There are no p95/p99
  observations here. Runs used a shared host without affinity, power-mode control
  or a statistical significance test. Do not infer small rankings from these data.
- Large search cases ran only two iterations per repetition, and long-message UI
  invalidation cases ran six. Their repeatability supports locating a cost, not a
  production latency guarantee.
- The quota fixture has eight equally populated rooms and one quota of a quarter
  of total retention. It measures fair eviction when the ring is full, not only
  eviction at an explicit room ceiling. ID/target construction is included in
  both quota-on and quota-off cases.
- CHECK selectors point to absent rooms with the oldest valid cursor in a full
  ring. This intentionally measures a large stale scan with no matching results.
  It does not represent repeated checks whose cursor is already near the head.
  Encoding and a synthetic queue are included; socket writes, client decoding,
  concurrent lock contention and authentication are excluded.
- UI invalidation bumps the buffer version between renders without inserting a
  message. This isolates the full cache rebuild performed when content changes.
  All 500 messages are retained; the viewport is 160 columns by 45 rows. Input
  probes log parser output; they are not actual PTY paste acceptance tests.
- The context fixture uses one root and 999 replies, each exactly 3,500 ASCII
  bytes. It raises the isolated outbound queue limit to 16 MiB, uses no history
  file, and requests 32,768 output bytes. Timing includes login, protocol
  serialization, decoding and CLI trimming; it excludes OS process startup.
  Allocation deltas include the embedded server and asynchronous client. Garbage
  collection runs before each measured call. All calls output 30,358 bytes.
- The allocation profile samples the whole fixture. About 93% of sampled
  allocation space falls beneath `runContext`; about 83% is directly attributed
  to `json.Marshal`. These are cumulative allocations, not simultaneous memory
  use. CPU profiling confirms heavy JSON string encoding but is not a separate
  uninstrumented speed measurement.
- An initial context fixture with 4,200-byte bodies was correctly rejected by
  the SDK's 4,096-byte limit. It was corrected before collecting the saved data.
  No claim is based on that failed fixture.
- No WAN, slow-disk, full fan-out, long-term RSS or production workload was tested.
  Existing source bounds are not evidence of a leak-free service; equally, large
  allocation totals do not establish a retained leak.

## Reproduction

Run from the repository root at the stated baseline. Python only constructs a
temporary overlay; Go executes the probes. The overlay adds virtual test files
without writing application files. The temporary directory remains available for
inspection after the commands finish.

```sh
probe_dir=$(mktemp -d /tmp/airc-roadmap.XXXXXX)
export AIRC_ROADMAP_PROBE_DIR="$probe_dir"
python3 - <<'PY'
import json
import os
from pathlib import Path

root = Path.cwd()
dest = Path(os.environ['AIRC_ROADMAP_PROBE_DIR'])
evidence = root / 'docs/research/performance-2026-10-03'
mapping = {}
for source, package in [('server_probe_test.go.txt', 'internal/server'),
                        ('ui_probe_test.go.txt', 'cmd/airc')]:
    copied = dest / source.removesuffix('.txt')
    copied.write_bytes((evidence / source).read_bytes())
    mapping[str(root / package / 'roadmap_probe_test.go')] = str(copied)
(dest / 'overlay.json').write_text(json.dumps({'Replace': mapping}))
PY

go test ./internal/server -run '^$' -bench '^BenchmarkHistory$' -benchmem -benchtime=250ms -count=5
go test ./cmd/airc -run '^$' -bench '^BenchmarkAgentCLI$' -benchtime=20x -count=3
go test -overlay "$probe_dir/overlay.json" ./internal/server -run '^$' -bench '^BenchmarkRoadmap' -benchmem -benchtime=150ms -count=3
go test -overlay "$probe_dir/overlay.json" ./cmd/airc -run '^TestRoadmapInputProbe$' -bench '^BenchmarkRoadmapUI$' -benchmem -benchtime=150ms -count=3 -v
go test -overlay "$probe_dir/overlay.json" ./cmd/airc -run '^TestRoadmapContextProbe$' -count=1 -v

go test -overlay "$probe_dir/overlay.json" ./cmd/airc -run '^TestRoadmapContextProbe$' -count=1 -memprofile "$probe_dir/context-alloc.pprof" -cpuprofile "$probe_dir/context-cpu.pprof" -o "$probe_dir/airc.test"
go tool pprof -top -alloc_space "$probe_dir/airc.test" "$probe_dir/context-alloc.pprof"
go tool pprof -top "$probe_dir/airc.test" "$probe_dir/context-cpu.pprof"
```

Run cases serially to avoid contaminating one another. Do not add `-race` to
performance comparisons; use separate race runs when implementing concurrency
changes. Future regression tests should assert semantics and bounded behavior,
not hard-code these machine-specific timings.
