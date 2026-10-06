# Shared history-semantics ownership

Before source: `83d64f9`. Both shared backend history and HTTP/2 history repeated
a 33-message eviction sequence to exercise retention gaps. The shared
`TestHistoryBackendRoomPagesGapAndRevocation` retains that sequence and now
also reads an actual native reply, checking exact body, parent/root linkage,
author and authenticated account continuity. It retains bounded first/second/
empty pages, raw native cursor position, retention gap, target rejection and
revocation behavior.

`TestHTTP2HistoryPagesRecentAndHonorsRevocation` retains actual HTTP/2 framing,
root/reply pages and their native metadata, sealed-cursor continuation, caught-up
empty pages, recent snapshot, current revocation and operation permission.
The duplicate eviction traffic is removed. Its caught-up cursor is explicitly
reused to verify position: HTTP tokens are opaque and may be freshly sealed,
so token-string identity is not a valid position assertion. HTTP/1.1 framing,
backend cancellation and whole-page byte bounds remain separately tested.

Serial before/after measurements cover both changed tests using compiled test
binaries with `-test.count=1 -test.timeout=120s`, from the same integration
directory. No concurrent validation ran during either measurement. Before
passed in 5.31 seconds wall; after passed in 1.57 seconds wall. Both include
the shared backend proof. Raw output/timing remains under
`/tmp/airc-roadmap-baseline/history-http-before.*` and
`history-http-after-fixed.*`. An initial revised attempt failed because a new
assertion incorrectly required identical sealed cursor tokens; its output is
retained in `history-http-after.*`. This single comparison does not establish
stable throughput or production performance.

The changed tests, HTTP/1.1 snapshot, shared withheld-snapshot cancellation,
whole-page byte overflow and current-authority dispatcher tests passed together
under the race detector in 5.336 seconds. `go vet ./integration/gateway`,
formatting and diff checks passed. Other history consolidation remains
outstanding.

Full normal acceptance at executable/test source `671e654` passed with
`go test ./... -count=1 -timeout=900s -p=1`: all packages passed,
including `integration/gateway` in 535.165 seconds; total wall time was
592.48 seconds. The run finished at documentation-only HEAD `4868bb5`.
Output and terminal timing are retained in
`/tmp/airc-roadmap-baseline/acceptance-after-consolidation-normal.out` and
`acceptance-after-consolidation-normal.stderr`. This is an integration
checkpoint; fresh final acceptance remains on the roadmap after remaining
executable changes.

Full race acceptance at the same executable/test source failed:
`go test -race ./... -count=1 -timeout=900s -p=1` reached the integration
package's 900-second timeout. The active test was
`TestWebSocketCLIPreauthAndMissingPongDeadlines`, running for 15 seconds
at timeout in fixture shutdown after its bounded final read. This establishes cumulative suite
timeout, not a demonstrated hang in that test. Other packages passed.
Total wall time was 1014.68 seconds. Full output and terminal timing remain
in `acceptance-after-consolidation-race.out` and
`acceptance-after-consolidation-race.stderr` under the same scratch directory.
Race acceptance remains unsatisfied; further test-speed work and fresh final
acceptance remain on the roadmap.
