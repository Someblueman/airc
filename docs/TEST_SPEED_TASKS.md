# Test speed task list

Running backlog started 2026-10-05 on `codex/connection-gateway`, inspected at
HEAD `6317ebf` with concurrent, uncommitted WHO/WHOIS feature work. This is an
exploration deliverable: no test, fixture, production code, or running process
was changed. Recheck counts and ownership after the feature work lands.

## Evidence and limits

- Source inventory: 645 `_test.go` files and 1,094 top-level `Test…` functions;
  `integration/gateway` accounts for 312 files and 355 functions. These counts
  include platform-specific and opt-in tests, not necessarily executed cases.
- No test file currently calls `t.Parallel()` or `testing.Short()`. Go can run
  packages concurrently, but tests within these packages run serially unless
  they create their own concurrency.
- Gateway integration has 89 `time.Sleep` call sites. Many are polling loops;
  a count alone does not establish wasted time.
- Historical repository evidence: `docs/gateway/STATUS.md` records integration
  runs of 411.213s/417.621s normal/race, 428.625s for a later serial race gate,
  and 477.317s for another race run. These are earlier trees, not today's timings.
- During inspection, two full normal/race suites were already running with
  `-p 1 -timeout 30m -count=1`. No additional suite was launched, and neither
  existing run was interrupted or reported as passed.
- `CLAUDE.md` requires the full race suite before pushing. Browser probes,
  resource soak, and warm RTT measurements are already opt-in. There is no CI
  configuration or test runner script in this checkout.

Impact below is a source-based hypothesis unless an explicit wait is identified.
Check boxes mean completed improvements with evidence, not just investigated ideas.

## First measurements

- [ ] **T01 — Capture a trustworthy timing baseline after feature work settles.**
  Record commit, dirty state, Go version, host load, flags, environment and exit
  status. Use `go test -json -count=1 -timeout 30m ./...` and its race equivalent,
  running one at a time; keep the current `-p 1` setting for the initial comparable
  baseline if that is the feature agent's gate. Rank leaf subtests separately
  from parent totals so nested `Elapsed` values are not double-counted. Measure
  wall time as well as package/test time; time the lazy child builds separately.
  Compare first and warm builds without clearing the user's shared Go cache.
  Reuse current logs where they contain sufficient evidence. **Done when:** a
  ranked list explains the largest costs and includes complete terminal results.

## Highest-value candidates

- [ ] **T02 — Match gateway child race instrumentation to the test mode.**
  `integration/gateway/gateway_binary_test.go:buildCurrentGateway` always executes
  `go build -race -trimpath`, including during normal tests. The binary is already
  shared once per test process, so there is no per-test rebuild here. Measure
  child startup, execution and shutdown separately; investigate race-runtime exit
  delay as a contributor across repeated launches. Select race/non-race child
  builds to match the parent test mode using a small explicit mechanism. Preserve
  race instrumentation and race-report failure detection in the full race gate;
  do not globally change `GORACE` to hide reports or shrink drain time without
  evidence. **Done when:** normal mode gets a normal child, race mode still gets
  a race child, and timing plus shutdown/restart/race checks show the benefit.

- [ ] **T03 — Remove production-length waits from idle-policy tests where possible.**
  `integration/gateway/webrtc_lifecycle_test.go:TestWebRTCInvalidTrafficDoesNotRenewPeerIdleDeadline`
  loops for up to 31s; `dtls_idle_test.go:TestDTLSMalformedAndUnauthorizedRecordsDoNotRenewIdle`
  deliberately exercises a 28–40s window. Production policy lives in
  `internal/gateway/adapter/webrtc_peer.go` and
  `internal/gateway/service/dtls_packets.go` with one-second reapers and 30s idle
  bounds. `internal/gateway/service/dtls_lifecycle_test.go` already backdates peer
  activity for a faster lower-level check. Extend that approach or extract the
  smallest controllable time/policy boundary. Retain real packet/peer tests for
  invalid-traffic non-renewal, valid renewal, slot reuse and joined shutdown.
  Do not add public timeout settings solely to accelerate tests or silently remove
  the existing process-level claim. If the long real-process proof remains needed,
  give it an explicit acceptance lane. **Potential:** roughly a minute of serial
  waiting across these two tests. **Done when:** equivalent boundary coverage has
  explicit evidence and production defaults are unchanged.

- [ ] **T04 — Introduce bounded parallelism for independently owned fixtures.**
  Start with pure core/config/auth tests, then independent gateway tests. Shared
  binaries already use `sync.Once`; `fixture_test.go` and `service_fixture_test.go`
  allocate separate processes, sockets, state and credentials. Review each
  candidate for shared environment, globals, filesystem paths, port assumptions
  and parent-owned fixtures before adding `t.Parallel`. Subtests sharing a daemon
  or observer cannot simply be marked parallel. Keep limits modest (measure 2 and
  4), especially with SQLite/fsync and WebRTC/QUIC peers. Do not split the giant
  package first: that would duplicate its once-only binary builds. **Done when:**
  affected tests pass race and shuffled repeated runs, fixture isolation still
  passes, and wall time improves without exceeding resource bounds.

- [ ] **T05 — Revisit package serialization and competing full-suite runs.**
  Full gates currently use `-p 1`; the repository status records why an earlier
  concurrent race run failed: short native CLI waits, a collector budget and the
  integration package's aggregate timeout. Compare `-p 1`, a bounded package
  concurrency and default package concurrency after fixes settle. Investigate
  genuine load-sensitive assumptions rather than repeatedly increasing timeouts.
  Measure this independently of T04, then measure the combined result. Run normal
  and race acceptance sequentially for stable comparisons. **Done when:** the
  chosen setting passes the required complete gate with a recorded wall time.

## Avoid repeated work and unnecessary waits

- [ ] **T06 — Share the current CLI executable within `cmd/airc` tests.**
  `mcp_test.go`, `conversation_effort_test.go` and `ui_pty_test.go` each build
  `airc` into a different temporary directory. Reuse one immutable, lazily built
  executable per test process with package-level cleanup; preserve each test's
  server, identity, state directory and actual subprocess/PTY boundary. Decide
  race-mode behavior explicitly (these builds currently omit `-race`).
  `latency_test.go` also builds a binary, but that path is a benchmark, not an
  ordinary test cost. **Done when:** the three real-process tests still pass and
  build/link time is paid once in an ordinary package run.

- [ ] **T07 — Build example clients once instead of repeatedly using `go run`.**
  `integration/gateway/framed_example_test.go`, `unix_stream_boundary_test.go`
  and `udp_example_test.go` invoke example programs through `go run`. Keep the
  real executable boundary and CLI arguments; build each needed example once
  lazily and reuse the immutable executable. Separate compile time from runtime
  deadlines (currently framed examples share a 20s budget with `go run`).
  **Done when:** TCP/TLS/Unix and UDP example checks still verify the same wire
  behavior and command failures, with measured build/startup savings.

- [ ] **T08 — Skip throttle waits in recovery cases that never resend.**
  `activity_mutation_recovery_test.go` sleeps 2100ms for both Retry and ReceiptOnly
  across typing/thinking: four sleeps, 8.4s in total. Only Retry needs a visible
  second broadcast outside the pinned native two-second throttle. Audit
  `reply_signal_recovery_test.go`, `message_mutation_recovery_test.go` and
  `poll_mutation_recovery_test.go` for the same distinction. Remove sleeps only
  where the case cannot require the throttle/backoff to elapse. **Done when:**
  receipt-only still proves no resend/uncertain acceptance and retry still proves
  the real second native effect. Activity receipt-only alone could save 4.2s.

- [ ] **T09 — Consolidate remaining native expiry/throttle scenarios.**
  Repeated 2100ms waits occur in activity HTTP/IPC and reply-signal IPC tests;
  `waiting_transport_test.go` waits 1200ms. Identify which assertions require
  wall-clock expiry and which merely need completed native mutation processing.
  Use existing ACKs/observer signals for the latter. Keep a real expiry witness
  wherever the carrier or native integration adds a distinct claim. The upstream
  executable is pinned and checksum verified: do not modify it, replace it with
  current source, or change its throttle to save time. **Done when:** redundant
  waits are removed with an assertion-by-assertion coverage map.

- [ ] **T10 — Reduce repeated AGD1 assembly-expiry waits without losing expiry proof.**
  `udp_loss_replay_test.go` and `unixgram_boundary_test.go` each sleep 6s to cross
  the five-second assembly lifetime in `internal/gateway/adapter/agd1.go`.
  Prefer direct explicit-time assembler boundary tests for exhaustive expiry
  cases; inspect existing assembler tests before adding more. Keep real carrier
  evidence that incomplete requests cannot admit, expiry frees state, and retry
  succeeds. If each carrier truly needs the full real-process expiry witness,
  retain it and parallelize independent cases rather than trimming the timeout.
  **Done when:** time-bound semantics and carrier integration each remain covered.

- [ ] **T11 — Replace readiness polling with existing causal signals.**
  Fixture startup polls every 10ms in `fixture_test.go`; receipt helpers often
  poll every 40ms; membership/operator propagation uses 20–50ms loops. Current
  gateway startup already has a JSON ready record in `service_fixture_test.go`.
  Use available ACK, ready, proxy or observer signals rather than sleeping after
  a completed action. Polling may still be appropriate for externally observed
  durable receipt state; do not replace it with busy loops or indiscriminately
  lower intervals. **Done when:** changed helpers remain bounded and diagnostics
  explain early child exit instead of waiting out the whole deadline.

- [ ] **T12 — Audit silence and timeout tests by the behavior they prove.**
  Examples: `framed_denial_test.go`, `unix_stream_boundary_test.go`,
  `coap_tcp_security_test.go`, `quic_boundary_test.go`, and the 150ms
  `assertNoActivitySignal` helper. A `time.After` used as a failure bound is not
  automatically a runtime cost; successful tests often return immediately.
  For true negative waits, use an ordered fence/native state check where it
  proves the claim. Preserve timing tests for incomplete-frame expiry, idle
  deadlines and asynchronous no-broadcast behavior where a fence is insufficient.
  **Done when:** the baseline identifies actual timeout consumption and each
  shortened check still detects a deliberately violated boundary.

## Coverage and workflow improvements

- [ ] **T13 — Audit duplicate semantic coverage across transport matrices.**
  Compare `native_operations_test.go`, its read/mutation helpers,
  `duplex_conformance_test.go`, and operation-specific HTTP/IPC/TLS/carrier tests.
  These already share helpers, but helpers still execute the full scenario for
  every carrier/profile. Make a coverage table first: operation × carrier × auth
  profile × unique boundary. Put exhaustive semantics in the narrowest existing
  layer; retain each carrier's actual authentication, framing, mutation receipt,
  independent native observation, watch/cancellation and failure boundaries.
  Do not reduce matrices just because helpers look similar; do not mix cases
  into shared mutable fixtures that obscure isolation. **Done when:** removed
  work is demonstrably duplicated and the required carrier claims remain tested.

- [ ] **T14 — Document a fast feedback command and complete acceptance lanes.**
  There is no `testing.Short` behavior today, so `go test -short` currently does
  not avoid these expensive tests. Start with affected-package/anchored-test
  commands, then decide whether a small explicit short/acceptance division is
  useful after T01. Keep the full race gate required by `CLAUDE.md`, real browser
  probes where claimed, and existing opt-in soak/fuzz/performance workflows.
  Avoid running full normal and race suites after every tiny edit; run the complete
  required gates at the appropriate integration checkpoint. Do not introduce
  silent skips, broad regexes that miss new tests, or an elaborate runner.
  **Done when:** contributors have accurate commands and each omitted fast-lane
  test belongs to a clearly named, exercised acceptance lane.

- [ ] **T15 — Measure pinned fixture export/build overhead before caching further.**
  `pinned_fixture_test.go` already shares the checksum-verified executable once
  per package process and builds lazily. Each new invocation still archives,
  extracts and builds the pinned source. First batch related focused tests into
  one invocation. Consider reuse across invocations only if T01 shows meaningful
  cost; key artifacts by verified baseline/archive, toolchain, platform and build
  flags, and verify artifact provenance. Avoid reusing the dirty current gateway
  binary across runs without a trustworthy source key. **Done when:** any cache
  saves measured time while preserving immutable upstream and per-test state
  isolation. This is lower priority than per-process and wall-clock wait costs.

- [ ] **T16 — Profile setup/allocation/I/O only if it remains material.**
  Repeated TLS/key provisioning and journal/schema setup occur in
  `internal/testtls`, transport fixtures and store fixtures. The native fixture
  runs with `--sync fsync`, and durability is part of acceptance. Measure setup
  versus scenario time after higher-impact work. Reuse only demonstrably safe
  immutable material if generation is expensive; keep mutable directories,
  credentials, journal locks and durable-data checks isolated. Do not switch
  durability tests to in-memory SQLite or disable fsync for a speed claim.
  **Done when:** profiles identify a real bottleneck and any change preserves the
  persistence/security boundary under the relevant broader gate.

## Suggested execution order

T01 → T02 and T08 → T06/T07 → T03 → small T04 pilot → T05 → remaining tasks
ranked by measured time. T13 needs particular care because the feature agent is
currently changing the shared operation/conformance coverage.

For each completed item, append its commit, before/after comparable timings,
checks and terminal results here. No speedup percentage is claimed yet.
