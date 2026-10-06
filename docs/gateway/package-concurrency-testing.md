# Gateway package concurrency comparison

Executable/test source: `469451b`. The comparison uses full normal repository
tests with `go test ./... -count=1 -timeout=900s -parallel=3`, changing only
`-p`. Planned order is 1, 2, 2, 1; within-package scheduling is fixed.
Existing build/module caches are used and test-result reuse is disabled.
No competing validation or benchmark runs during measurement.

All four samples passed all packages with terminal exit 0:

| Sample | Package limit | Wall seconds |
| --- | --- | --- |
| 0 | 1 | 584.275346 |
| 1 | 2 | 541.632303 |
| 2 | 2 | 541.063546 |
| 3 | 1 | 586.669587 |

Both pairs favor `-p=2`; medians are 585.472466 seconds for `-p=1` and
541.347924 seconds for `-p=2`, about 7.54 percent lower. Repeats within each
limit differ by less than half a percent. Normal acceptance now uses `-p=2`
with the measured `-parallel=3` limit. This is a host-specific normal-test
decision, not a claim about race or production performance. Race acceptance
retains `-p=1`; fresh final acceptance remains unfinished.

The host was macOS 26.6.2 arm64 with ten logical CPUs and Go 1.27.1.
Initial load averages were 13.06, 11.51 and 12.65; unrelated host activity
was not controlled. The repeated comparison supports the local decision,
but these results should not be extrapolated to other hosts.

Raw commands, timings and terminal outcomes are saved incrementally under
`/tmp/airc-roadmap-baseline/package-concurrency-results.json`; source, Go
version, host, CPU count and initial load are in
`package-concurrency-metadata.json`. Per-run package output and stderr use
`package-concurrency-<sample>-p<limit>.out` and `.stderr` in that directory.
The roadmap comparison remains open until the evidence supports a decision.
