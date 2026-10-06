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
