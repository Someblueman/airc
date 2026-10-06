# Gateway package concurrency comparison

Executable/test source: `469451b`. The comparison uses full normal repository
tests with `go test ./... -count=1 -timeout=900s -parallel=3`, changing only
`-p`. Planned order is 1, 2, 2, 1; within-package scheduling is fixed.
Existing build/module caches are used and test-result reuse is disabled.
No competing validation or benchmark runs during measurement.

The first two samples passed all packages with terminal exit 0:

| Sample | Package limit | Wall seconds |
| --- | --- | --- |
| 0 | 1 | 584.275346 |
| 1 | 2 | 541.632303 |

The first comparison favors `-p=2`, but one pair does not establish a stable
improvement. The reversed pair and final acceptance are unfinished.

Raw commands, timings and terminal outcomes are saved incrementally under
`/tmp/airc-roadmap-baseline/package-concurrency-results.json`; source, Go
version, host, CPU count and initial load are in
`package-concurrency-metadata.json`. Per-run package output and stderr use
`package-concurrency-<sample>-p<limit>.out` and `.stderr` in that directory.
The roadmap comparison remains open until the evidence supports a decision.
