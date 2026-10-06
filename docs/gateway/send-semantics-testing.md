# Shared send-semantics ownership

Before source: `90c2d70`. The native carrier matrix repeated reply linkage and
reaction semantics for 16 cases: raw QUIC bidirectional, paired unidirectional
and datagram modes; DTLS, UDP and Unix datagrams; four WebRTC delivery modes;
four CoAPS confirmability/encryption combinations; and signed/encrypted
CoAPS/TCP. The consolidation moves those semantics to direct backend acceptance
rather than rerunning them through every generic frame carrier.

`TestBackendAuthorizedRoomAndNestedReplies` verifies exact author, target, body,
parent/root linkage, nested replies and retained receipt recovery against real
native observation. `TestBackendReactionMatchesNativeMessage` verifies a real
reaction with exact author, target, body, parent/root linkage and account identity,
then records and verifies durable native acceptance. Backend reaction boundary
and recovery tests retain feature/parent rejection, uncertainty, retained tuple
recovery, receipt-only policy and eviction behavior.

`exerciseCarrierSendReadsAndMutations` keeps each carrier's authenticated real
send, independent native history observation, scoped receipt and complete read
results. Its other native mutation checks remain. Carrier framing, authentication,
replay, size, cancellation, lease, shutdown and negative-write fixtures are not
removed. Matrix test names now say `NativeSendReadsAndMutations`, rather than
claiming full per-carrier operation parity. Historical full matrices included
reply/reaction execution; the current matrix delegates those semantics to the
shared backend tests. Dedicated carrier reply/reaction tests remain where they
already exist; no new aliases, mocks or alternate runtime paths are introduced.

Initial comparison source: `90c2d70`. Fresh single-package runs used
`-count=1 -timeout=300s -p=1`, without concurrent validation. The before run
failed after 126.401 seconds (127.96 seconds wall): `coaps-encrypted` lost its
connection during retract receipt lookup. The revised matrix also failed after
125.773 seconds (129.44 seconds wall), in `coaps-non-signed` at the same retained
boundary. All other carrier parents passed. These failed attempts remain at
`/tmp/airc-roadmap-baseline/send-consolidation-{before,after}.*`.

Diagnostic request counts reproduced association closure exactly at request
257, exceeding CoAPS's lifetime 256-token budget. Checkpoint `1052bd5` fixes
fixture receipt polling; [CoAPS polling evidence](coap-polling-testing.md)
records the separate verification. Production limits and deadlines remain.

The corrected serial comparison uses source `1052bd5` on both sides, changing
only the carrier reply/reaction consolidation. Both binaries were compiled
separately from the timed execution, and run with `-test.count=1`,
`-test.timeout=300s` and `-test.parallel=1` from the same integration directory.
No concurrent validation ran during either measurement. Every carrier passed.

| Measurement | Wall seconds |
| --- | ---: |
| Before: carrier matrix | 116.29 |
| After: carrier matrix | 134.41 |
| Added shared reaction proof | 0.61 |

Raw output and timing are retained at
`/tmp/airc-roadmap-baseline/send-{before,after}-budget.{out,stderr}` and
`send-reaction-cost.{out,stderr}`. This pair is slower after consolidation;
it does not establish a runtime improvement. The change removes 32 repeated
reply/reaction executions and assigns their semantic proof to shared backend
acceptance. Existing reply coverage already ran independently; the newly added
reaction proof's cost is included above.

Selected shared reply/reaction acceptance, boundary and recovery tests passed
normally in 6.186 seconds (7.61 seconds wall). Shared proofs and isolated CoAPS
passed together under the race detector in 46.779 seconds. Remaining affected
carriers passed race verification in 114.565 seconds. After the polling fix,
CoAPS passed two normal matrices and one race matrix. Those overlapping
validation timings are correctness evidence, not performance samples.
`go vet ./integration/gateway`, formatting and diff checks passed.

## Shared action, correction and retraction

Before source: `191fc7b`. `exerciseNativeMutations` also repeated an action,
correction and retraction sequence across the same 16 carrier cases. Their
semantic assertions now belong to `TestBackendMessageMutationsMatchNativeHistory`.
It uses the actual backend and durable journal, independently observes all three
native messages, verifies exact accepted message facts, account/root continuity,
and checks the original's retained retraction/supersession annotations and the
retained correction's body, parent and thread root.

The carrier matrix retains its real send, scoped receipt, complete reads and
other mutations. Dedicated mutation carrier, native policy, authorization,
recovery and uncertainty tests are unchanged. Forty-eight repeated mutation
executions are removed; this does not claim every optional mutation executes
through every carrier.

The serial before measurement is the still-valid preceding matrix result at
`send-after-budget.{out,stderr}`: 134.41 seconds wall. The revised compiled
single-package binary used the same count, timeout and parallel flags, with
no concurrent validation during measurement; every carrier passed in 45.61
seconds wall. The added shared native proof passed separately in 0.81 seconds
wall. Raw revised results are at `/tmp/airc-roadmap-baseline/message-after.*`
and `message-shared-cost.*`. This sample reduces combined wall time to 46.42
seconds; it is one serial comparison, not a claim of stable throughput or
production performance.

The new proof initially rejected an invalid dotted request ID; valid stable
identifiers fixed the test setup without changing production validation. Both
attempts are preserved as `message-shared-normal{,-fixed}.out`.
All affected carrier matrices, the new shared proof, and the unchanged message
mutation recovery, authorization and native policy tests passed together under
the race detector in 81.935 seconds. `go vet ./integration/gateway`, formatting
and diff checks passed.

## Shared profile and presence

Before source: `cad63d1`. The same matrix repeated profile and presence updates
and checked their native card fields in 16 carrier cases. Those semantics now
belong to `TestBackendProfileAndPresenceMatchNativeCard`, which uses the actual
backend and journal. It independently queries the native directory after each
operation, compares the profile with the durable acknowledgement, and verifies
authenticated account continuity and exact presence state, note and expiry.
The carrier matrix retains its real send, reads, receipt and other mutations.
Dedicated card transports, replay, clearing, expiry, selector rejection and
uncertain acknowledgement tests remain unchanged.

The preceding, still-valid serial matrix result is 45.61 seconds wall at
`message-after.{out,stderr}`. The revised compiled test binary uses the same
count, timeout, parallel flags and integration directory, with no concurrent
validation during measurement. All 16 cases passed in 40.31 seconds wall, and
the added shared proof passed separately in 0.69 seconds wall. Combined time
is 41.00 seconds in this single comparison. Raw evidence is retained at
`/tmp/airc-roadmap-baseline/card-after.*` and `card-shared-cost.*`; no stable
throughput or production performance claim follows from this sample.

The new test initially referenced a nonexistent presence aggregate. Correcting
it to the native card's state, note and expiry fields fixed compilation; both
attempts remain in `card-shared-normal{,-fixed}.out`.
All affected carrier cases, the new shared card proof and existing clearing,
expiry, invalid-selector and oversized-acknowledgement uncertainty tests passed
together under the race detector in 73.144 seconds. `go vet ./integration/gateway`,
formatting and diff checks passed.

## Shared topic acceptance

Before source: `8f1f0d0`. Sixteen carrier cases previously set a topic and compared
its room/body with a native query. `TestBackendTopicMatchesNativeMetadata` now
owns this semantic proof through the actual backend and journal. It independently
queries native topic metadata and compares exact target, body, author and set
time with the durable acknowledgement. Carrier send, read, receipt and remaining
mutations are retained; dedicated topic transport, clearing and restart tests
are unchanged.

The still-valid preceding serial matrix result is 40.31 seconds wall at
`card-after.{out,stderr}`. Using the same compiled-binary count, timeout,
parallel flags and integration directory, all revised carrier cases passed in
36.91 seconds wall. The new shared topic proof passed separately in 1.45 seconds
wall, giving 38.36 seconds combined for this sample. No concurrent validation
ran during measurement. Raw evidence is retained under
`/tmp/airc-roadmap-baseline/topic-after.*` and `topic-shared-cost.*`.
This single comparison does not establish stable throughput or production
performance.

All affected carrier cases, the new shared topic proof and existing clearing
and restart replay tests passed together under the race detector in 67.569
seconds. `go vet ./integration/gateway`, formatting and diff checks passed.

## Shared room-update acceptance

Before source: `3cbb9d1`. Sixteen carrier cases previously set one-second slow
mode and history limit 32, then compared both with native room state.
`TestBackendRoomUpdateMatchesNativeConfiguration` now owns that proof through
the actual backend and journal, independently querying the native room and
comparing the exact target/settings with the durable acknowledgement.
Carrier send, reads, receipt and remaining mutations are retained. Subsequent
optional mutations now use fixture default slow mode instead of inheriting an
incidental one-second update. Dedicated room transports, slow-mode boundaries,
retention quotas, permission rejection and restart replay remain unchanged.

The preceding serial matrix result remains valid at 36.91 seconds wall in
`topic-after.{out,stderr}`. With the same compiled-binary count, timeout,
parallel flags and integration directory, all revised carrier cases passed in
33.65 seconds wall. The added shared proof passed separately in 1.47 seconds
wall, giving 35.12 seconds combined for this sample. No concurrent validation
ran during measurement. Raw evidence is retained under
`/tmp/airc-roadmap-baseline/room-after.*` and `room-shared-cost.*`.
This comparison includes removal of incidental slow-mode setup; it does not
establish stable throughput or production performance.

All affected carrier cases, the shared room proof and existing retention,
invalid-intent rejection, HTTP permission/boundary and stdio restart replay
tests passed together under the race detector in 68.450 seconds.
`go vet ./integration/gateway`, formatting and diff checks passed.

## Shared pin add/remove acceptance

Before source: `c53c446`. Sixteen carrier cases previously added the sent message
to pins, checked its ID in the native snapshot, then removed it and checked an
empty snapshot. `TestBackendPinAddRemoveMatchesNativeSnapshot` now owns that
sequence through the actual backend and durable journal. It verifies complete
accepted pin acknowledgements and independently checks the pinned message's
ID, author, nonempty account, room and body, then the empty removal snapshot.
Carrier send, reads, receipt and remaining mutations stay intact. Dedicated pin
transports, authorization, eviction, retained no-op, quota, mute, recovery and
uncertainty tests remain unchanged.

The preceding serial matrix measurement remains valid at 33.65 seconds wall
in `room-after.{out,stderr}`. With the same compiled-binary count, timeout,
parallel flags and integration directory, all revised carrier cases passed in
27.35 seconds wall. The added shared proof passed separately in 1.30 seconds
wall, giving 28.65 seconds combined for this sample. No concurrent validation
ran during measurement. Raw evidence is retained under
`/tmp/airc-roadmap-baseline/pin-after.*` and `pin-shared-cost.*`.
This single comparison does not establish stable throughput or production
performance.

All affected carrier cases, the shared pin proof and existing recovery,
exact-room authorization, eviction/no-op, invalid-intent, quota and mute tests
passed together under the race detector in 62.787 seconds.
`go vet ./integration/gateway`, formatting and diff checks passed.

## Shared activity acceptance

Before source: `20e04b0`. Sixteen carrier cases previously submitted typing
activity and compared its accepted facts with an independent native signal.
`TestBackendActivityMatchesNativeSignal` now owns that same proof through the
actual backend and durable journal, using `assertActivityMutationFacts` for
exact kind, note, target, author, account, expiry and accepted-result facts.
Carrier send, reads, receipt and remaining mutations stay intact. Dedicated
activity transports, unauthorized/invalid intent, mute, throttle, recovery and
receipt-only uncertainty tests are unchanged.

The preceding serial matrix measurement remains valid at 27.35 seconds wall
in `pin-after.{out,stderr}`. With the same compiled-binary count, timeout,
parallel flags and integration directory, all revised carrier cases passed in
24.23 seconds wall. The added shared proof passed separately in 1.41 seconds
wall, giving 25.64 seconds combined for this sample. No concurrent validation
ran during measurement. Raw evidence is retained under
`/tmp/airc-roadmap-baseline/activity-after.*` and `activity-shared-cost.*`.
This single comparison does not establish stable throughput or production
performance.

All affected carrier cases, the shared activity proof and existing recovery,
receipt-only, invalid/unauthorized intent and native mute/recovery tests passed
together under the race detector in 59.706 seconds.
`go vet ./integration/gateway`, formatting and diff checks passed.
