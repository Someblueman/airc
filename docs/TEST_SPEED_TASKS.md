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
  Separate ordinary fuzz-seed execution from opt-in fuzzing/soak/benchmarks;
  distinguish Go test-binary compilation from nested child-program builds.
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
  Concrete native blockers: `cmd/airc/main_test.go` gives all test servers the
  same `serverClockSkew`; `agentEnv` in `agent_test.go` uses `t.Setenv`, as do
  multiple other CLI tests. Keep environment-contract tests serial; consider
  per-fixture clocks only where they unlock worthwhile independent execution.
  Also inspect pure native server/protocol tests and isolated store migration
  subtests as pilots. Independent ports alone do not prove parallel safety.

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
  `udp_example_test.go` can launch `go run` up to ten times in one test (send,
  up to eight receipt polls, history). Its per-call budget is 15s; the Unix
  example has 30s. Preserve all command-line and correlation assertions.

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
  Consider ordinary cached `go test` for unchanged pure/in-process packages,
  reserving `-count=1` for timing and fresh acceptance evidence. First audit
  subprocess packages: parent test caching must not conceal changes to child
  command/example source that is outside the parent's imported dependency graph.
  A cached pass is not a fresh execution result.

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
  Concrete setup to time: store fixture initialization from schema version zero,
  per-credential file/directory sync, and layered operation fixtures rebuilding
  auth verifiers. A migrated DB template may bypass fresh-init coverage; a
  lightweight credential writer may bypass secure creation. These are conditional
  investigations, not approved shortcuts. `store/files_unix.go` syncs before
  SQLite setup and again after verifying WAL/SHM files: these are different
  durability barriers, not proven redundant syncs.

## Additional opportunities from four-agent exploration

- [ ] **T17 — Separate fixed read-lease proofs from repeated stream conformance.**
  `store/read_grant.go` sets `ReadLeaseDuration = 15s`. Quiet terminal tests in
  `udp_watch_test.go`, `dtls_watch_test.go`, `unixgram_watch_test.go`,
  `quic_datagram_lease_test.go`, `webtransport_datagram_lease_test.go`,
  `grpc_terminal_test.go`, `rpc_terminal_test.go`, and `http_events_resume_test.go`
  wait for expiry. `duplex_conformance_test.go:duplexOperations` also waits for
  expiry through TCP/TLS, WebSocket, Unix and stdio callers, including profile
  subtests. This is a potentially larger serial cost than the idle tests in T03.
  Trace the existing signed-envelope deadline clamp in
  `dispatch/detached_watch.go` and the backdated-envelope technique in
  `http_detached_events_test.go` before adding any new time seam. Use shorter
  existing validity bounds for cases proving correlated terminal delivery/resume;
  keep genuine 15s grant witnesses where fixed lease duration is the claim.
  The clamp applies to `DetachedWatchVerified`, reached through signed/encrypted
  correlated streams. Bearer gRPC/JSON-RPC/HTTP watches use `ReadPage` and cannot
  be shortened by backdating an envelope. Do not change production bearer lease
  semantics to manufacture a faster test. The duplex helper has six current
  carrier/profile executions (TCP/TLS two, WebSocket two, Unix one, stdio one).
  In particular, the HTTP SSE test explicitly proves quiet streams outlive the
  ordinary five-second write bound; a shorter expiry cannot replace that proof.
  **Done when:** each terminal/cursor/heartbeat/native-cleanup claim has an
  explicit home, retained real-time witnesses pass, and redundant full-lease
  waits fall measurably. Count actual profile executions before estimating savings.

- [ ] **T18 — Audit other production timer waits through existing time boundaries.**
  `adapter/udp_idle_test.go:TestUDPInvalidTrafficCannotRetainAdmissionLease` waits
  for the five-second source idle lifetime while sending every 50ms. Its reaper
  already accepts `now` in `adapter/packet.go`. The CoAP TCP missing-CSM test in
  `adapter/coap_tcp_test.go` waits for the five-second grace in `coap_tcp.go`
  (the 6.5s timer is a failure bound). Inspect heartbeat-only CoAP/CoAP TCP tests
  too. `coap_revocation_test.go` waits for an existing read grant to expire while
  proving immediate denial of new work; its 18s loop bound is not an unconditional
  18s sleep. Prefer direct expiry predicates/backdated internal state when the
  real carrier path is separately covered. Keep invalid-traffic non-renewal,
  exact CSM exchange, slot replacement, revocation and joined shutdown evidence.
  Avoid mutable package-wide timer overrides that would obstruct parallelism.
  **Done when:** successful timer consumption is measured and each removed wait
  has equivalent policy and real-path coverage with unchanged production defaults.

- [ ] **T19 — Seed bulk native test data over one session.**
  `cmd/airc/ui_paging_test.go` evicts a cursor using 520 `send` calls. The helper
  in `agent_test.go` runs the CLI in-process, but every call opens a fresh IRC
  connection and registers again. The same UI test already uses a persistent
  client to build replies. Audit bulk seeding in `check_recovery_test.go`,
  `check_budget_test.go`, `check_combined_test.go`, and `agent_test.go` as well.
  Use one sender for data preparation where CLI registration is not the claim;
  preserve sender identity/account, explicit IDs, receipt ordering, retained
  history size and observer draining. Keep real CLI commands for the behavior
  under test. **Done when:** retention/cursor/budget checks remain equivalent and
  seed connection count and runtime drop. Also verify whether smaller history
  fixtures can preserve the same paging/gap boundaries without 520 records.
  This seeding also hits the SAFE_RETRY outbox: `send.go` saves intent and confirmed
  receipt, and `outbox.go` atomically rewrites up to 128 retained entries using
  `SyncFlush` by default. At 520 ordinary sends that is about 1,040 synced outbox
  saves, in addition to native history writes. Keep outbox and cross-invocation
  cursor tests on their actual CLI path; do not globally replace fresh commands.

- [ ] **T20 — Stop re-registering an independent STATUS client on every poll.**
  `integration/gateway/live_http_test.go:nativeObserverCount` dials a fresh
  `live-status` client, requests STATUS and closes on every call. Observer-count
  polling across lease/slow-reader/cancel tests repeats this every 50–100ms.
  Consider one independently owned status reader per test/socket. Verify that
  keeping it connected does not alter the facts under test (observer counts,
  live-agent membership, capacity), and do not share a reader with the watch.
  A persistent reader needs explicit event ownership and cleanup; avoid accumulating
  `t.Cleanup` registrations per poll. **Done when:** independent count/cleanup
  assertions and capacity/isolation tests pass with fewer native registrations.

- [ ] **T21 — Make slow-reader pressure setup precise and audit repeated write-bound proofs.**
  The gRPC, HTTP3, raw QUIC, WebTransport and WebRTC `*_slow_reader_test.go`
  family fills bounded transport windows and waits for real writer cleanup.
  WebRTC additionally paces 80 sends by 30ms (2.4s total literal sleeps).
  Measure whether that pacing is necessary to prove write pressure rather than
  another queue/overflow path. Use a causal blocked-reader/window witness where
  available. Keep real per-carrier backpressure, deadline, native lease release
  and fresh admission assertions; tests that distinguish writer timeout from
  ordinary lease expiry must retain that distinction. Map HTTP event pressure
  and RPC batch blocked-writer checks before consolidating related scenarios.
  **Done when:** reduced setup reliably reaches the intended pressure boundary,
  preserves lower-bound timing claims where needed, and improves leaf timings.

- [ ] **T22 — Choose native test durability modes according to their claims.**
  Native `server.Config.Sync` defaults to `atomicfile.SyncFull`. It invokes file
  sync and, for atomic replacement, directory sync. Bulk append/order tests such
  as `pkg/irc/persistence_test.go` and `internal/server/runtime_limits_test.go`
  exercise many writes under that default. Identify purely semantic fixture
  setup that can use the existing lighter mode while retaining full-sync default,
  error/receipt semantics and designated persistence/durability/restart tests.
  Do not change a global fixture default or downgrade persisted-receipt evidence
  without mapping its claims. Do not alter the checksum-pinned integration daemon.
  **Done when:** syscall/setup cost is measured, each changed fixture's durability
  expectation is explicit, and durable-path acceptance remains intact.

- [ ] **T23 — Shorten load-generator test scheduling while preserving work and cancellation.**
  `cmd/airc-load/agent_test.go:TestAgentsExerciseRealChatAndCancel` schedules a
  one-second measured phase after a 50ms delay with four real agents. Investigate
  whether a shorter schedule at adjusted rate preserves all sample/mention/health
  assertions without making the test sensitive to host load. Keep real chat,
  persistence/receipt observation and prompt cancellation. **Done when:** the same
  assertions pass repeatedly under realistic load with a shorter measured phase.
  Low priority: roughly a second in one package is less valuable than T17/T02.

- [ ] **T24 — Include native CLI negative waits and raw-collector deadlines in T12.**
  `cmd/airc/agent_test.go` has successful 700ms, 400ms and 200ms waiting scenarios;
  `reply_wait_test.go` uses a 150ms unrelated-traffic window. Existing tap helpers
  can establish subscription/failed reconnect, but subscription alone does not
  prove that unrelated traffic was processed without waking the waiter. Inspect
  PTY readiness/pacing and raw IRC deadline cases too. Use causal fences only
  where ordering proves the required absence; keep genuine quiet-wait expiry
  tests. **Done when:** shorter checks still detect accidental wakeups/duplicate
  effects and real timeout/registration behavior remains exercised.

- [ ] **T25 — Audit the 14-second WebSocket ping/pong witness.**
  `websocket_lifecycle_test.go:TestWebSocketCLIPreauthAndMissingPongDeadlines`
  waits until 14s after setup, then reads the dead connection. Production
  `adapter/websocket.go` pings every 10s with a 3s pong bound; a separate preauth
  close is checked earlier in the same test. Investigate a small controllable
  adapter timing boundary for exhaustive missing-pong/preauth cases while keeping
  real connection closure and default timer evidence where claimed. This timer
  family was absent from the initial idle/lease list. **Done when:** missing-pong
  and preauth failure remain independently proved and ordinary feedback avoids
  duplicated full-duration waits without changing production defaults.

- [ ] **T26 — Use existing admission time input for delivery expiry where safe.**
  `delivery_expiry_test.go:TestDeliveryExpiresIdleWorkWhileReceiptLeaseIsBusy`
  admits idle work with expiry `now.Unix()+2` and waits for automatic maintenance
  while another worker's receipt is withheld. `Store.Admit` already takes explicit
  `now`; investigate admitting against an earlier event time instead of adding
  a clock abstraction. Preserve the pending-quota-full assertion before expiry,
  actual runner-driven expiry, quota release and zero send attempts. Simply
  backdating an already-expired request while the runner is active may let
  maintenance beat the quota assertion; establish deterministic phase ordering
  before claiming this is safe. **Done when:** the same busy-worker/maintenance
  boundary passes repeated race runs without the approximately two-second wait.

- [ ] **T27 — Measure fresh schema construction before considering consolidation.**
  `store/store.go:initialize` creates a version-one schema for an empty database,
  then runs migrations 2 through 10, including repeated trigger replacement and
  updates of empty tables. T16 already covers setup profiling; this identifies
  the exact repeated work. If material, consider direct current-schema creation
  with migration fixtures frozen independently, rather than copying a migrated
  template into every test. This changes production durable initialization and
  requires schema equivalence, old-version migration and fresh-start acceptance.
  **Done when:** measured setup justifies the change and fresh/migrated databases
  have equivalent contracts under the full store and repository acceptance gates.
  Conditional and lower priority than real timer waits and bulk CLI seeding.

## Suggested execution order

T01 → T02/T19/T08 → T06/T07 → T17/T03/T18/T25 → small T04 pilot → T05 →
remaining tasks ranked by measured time. T13 needs particular care because the
feature agent is currently changing the shared operation/conformance coverage.

For each completed item, append its commit, before/after comparable timings,
checks and terminal results here. No speedup percentage is claimed yet.

## Fanout coverage and stopping evidence (2026-10-05)

Three rounds of four Pi agents used `opencode-go/deepseek-v4.1-flash`, the
installed Pi route for DeepSeek V4.1. All assignments were read-only, with no
tests/builds or recursive delegation. The primary agent checked material claims
against source and incorporated distinct opportunities above rather than treating
worker agreement as correctness or performance evidence.

| Round | Coverage/purpose | Result |
| --- | --- | --- |
| 1 | Transport/process fixtures; gateway backend/store/auth/service/adapter; native CLI/server/client/bot/protocol; suite-wide setup/cache/build | Four agents finished. Runner rejected all four for oversized event logs; one final receipt was also malformed. Retained raw findings were reviewed directly, not counted as validated receipts. Added the major lease family, bulk seeding and setup/scheduling candidates. |
| 2 | Trace lease/auth/heartbeat timing; challenge storage/durability shortcuts; inspect native hot setup and globals; cover delivery/dispatch/config/jsoninput and smaller packages/platform/large-data/teardown paths | Four validated results. Added WebSocket ping/pong and direct-store automatic-expiry candidates; refined outbox seed cost, cache boundaries and fresh initialization. |
| 3 | Bounded search for hidden waiting operations, native repeated I/O, setup/cache omissions, and unsafe assumptions | Four validated results. No distinct substantial new mechanism; findings extended existing tasks or corrected estimates. Each reviewer reported source-audit saturation within its scope. |

Local provenance is retained under
`/tmp/airc-test-speed-fanout-20261005/round{1,2,3}/packet.json`, with private
per-worker prompts/event logs alongside it. These temporary artifacts are not
committed and may disappear after cleanup/reboot; this document retains the
actionable findings and limitations.

Material rejected/qualified suggestions:

- Do not remove store sync/checkpoint barriers, skip secure credential creation,
  or batch process-death phases into one surviving child to reduce runtime.
- Do not replace continuous-noise deadline tests with a blocked silent reader;
  noise resilience is their claim. Do not automatically substitute cancellation
  for a real deadline-expiry assertion.
- Fresh CLI cursor-persistence/outbox recovery sequences must keep separate
  invocations; bulk UI seeding is a different purpose.
- Bearer lease expiry is not signed-envelope expiry. Signed/encrypted correlated
  streams reach the detached clamp; bearer gRPC/JSON-RPC/HTTP streams do not.
- Go cached passes need care with runtime-built children. Keep fresh acceptance
  execution; a source-content cache proposal is conditional, not a prerequisite
  for ordinary small improvements.
- No measured basis supports a claim that certificate/key generation is a major
  cost, or that it is definitely negligible. Keep T16 conditional and low priority.
- A smaller log-rotation workload might remove a few MiB of test I/O, but could
  stop proving repeated rotation. Small in-memory/property loops and opt-in soak
  were not promoted above observed long-wait/repeated-I/O mechanisms.
- `Config.Sync` matters only when a fixture actually persists data. Ordinary
  in-memory CLI servers do not incur a history-file flush on every send merely
  because the config's zero value is `SyncFull`.

Stopping criterion: all major test areas and the source-visible cost mechanisms
(timer consumption, subprocess/build churn, connection churn, durable setup,
bulk data preparation, scheduling/cache and duplicated coverage) were reviewed;
the final round produced refinements rather than a new large opportunity family.
This supports broad coverage of **suspected** opportunities, not a numerical
completeness guarantee. Remaining uncertainty is primarily measured attribution,
claim-preserving implementation feasibility, contention/flakiness under bounded
parallelism, opt-in/platform execution, and changes made by the feature agent.
T01 should choose actual optimization work before another broad source audit.
