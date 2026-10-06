# Stream terminal timing checks

Signed and encrypted stream conformance proves correlated terminal delivery,
live native messages, scoped cursor renewal, and joined shutdown. It uses the
existing detached-envelope deadline clamp, separate from the fixed read-grant
lifetime. `TestReadGrantPermissionLifetimeCloneAndSingleConsumption` checks the
15-second grant boundary with explicit time; real carrier witnesses such as
`TestUDPQuietWatchHeartbeatCursorAndLeaseExpiry` retain actual 13–18-second
elapsed bounds, heartbeats, cursor stability, native lease release and reuse.
Neither production duration nor bearer semantics changes for these test fixes.

The shared `duplexOperations` helper computes one envelope expiry using
`auth.ClockSkewSeconds` and checks that the correlated `lease_expired` terminal
arrives no earlier than expiry plus that allowance. Each carrier chooses its
terminal validity window explicitly. Live delivery must precede the terminal;
renewal must not replay the delivered message, and shutdown must join active work.

## Plain framed TCP

Before source: `3f427ba`. Only plain TCP changes from five to three seconds;
TLS, both WebSocket profiles, Unix and stdio retain five seconds.

Comparable command: `go test -json ./integration/gateway -run
'^TestFramedCLISharedOperationsAndMultiplexedWatch$/^false$' -count=3 -timeout=120s`.
Parent/subtest seconds before: 9.90, 5.00, 5.00; after: 7.33, 2.99, 3.00.
Package seconds: 20.284 versus 13.777; command wall seconds: 22.28 versus 17.55.
The first sample includes lazy builds. Host load was not captured; the warm
samples match the two-second validity reduction, without claiming a stable
whole-suite speedup. Raw JSON/stderr and focused race/vet results are retained
at `/tmp/airc-roadmap-baseline/framed-terminal-*`.

Normal count-three and race count-five TCP checks passed. All six shared-helper
executions also passed normal and race checks (`duplex-terminal-*` evidence).
Package vet passed. Fixed-duration witnesses remain unchanged.

## Framed TLS

Before source: `8c3b818`. TLS now uses three seconds; plain TCP is unchanged.
Certificate trust/name rejection, signed and encrypted profile controls, actual
native live delivery, correlated expiry, cursor renewal, and shutdown remain.

Comparable command: `go test -json ./integration/gateway -run
'^TestFramedCLISharedOperationsAndMultiplexedWatch$/^true$' -count=3 -timeout=120s`.
Parent/subtest seconds before: 9.38, 5.00, 5.00; after: 5.84, 3.00, 3.00.
Package seconds: 19.753 versus 12.373; command wall seconds: 20.93 versus 14.36.
First samples include builds; host load was not captured. Warm samples match
the two-second validity reduction, without establishing full-suite performance.
Normal count-three, race count-five, and package vet pass. Raw evidence is in
`/tmp/airc-roadmap-baseline/tls-terminal-*`.

## WebSocket and WSS

Before source: `3bd15eb`. Both encrypted WebSocket and signed WSS conformance
now use three-second envelope validity. Existing native live delivery, correlated
terminal, no-early-expiry, scoped cursor renewal and hijacked-connection shutdown
assertions remain unchanged.

Comparable command: `go test -json ./integration/gateway -run
'^TestWebSocketCLISharedOperationsAndMultiplexedWatch$' -count=3 -timeout=120s`.
Parent seconds before: 12.89, 10.00, 10.00; after: 8.24, 6.00, 6.00.
Encrypted subtest seconds before: 7.90, 5.00, 5.00; after: 5.26, 3.00, 3.00.
Signed subtest seconds before: 4.99, 5.00, 5.00; after: 2.98, 3.00, 3.00.
Package seconds: 33.335 versus 20.730; command wall seconds: 35.27 versus 23.52.
Load averages before start/end: 12.99/16.99/15.92 and 12.90/16.56/15.80;
after: 13.06/16.36/15.75 and 13.75/16.33/15.75. The after command overlapped
race validation; first samples include builds. Warm leaf times match each
profile's two-second validity reduction; these runs do not establish stable
whole-suite performance. Normal and race count-three checks and package vet
pass. Raw JSON, stderr, load and validation evidence is in
`/tmp/airc-roadmap-baseline/ws-terminal-*`.

## Darwin Unix stream

Before source: `3a64fbd`. Unix-stream conformance now uses three-second validity.
The real private socket, active-path rejection, native live messages, scoped
cursor renewal, joined shutdown and socket removal checks remain unchanged.
Linux runtime evidence remains a separate outstanding qualification item.

Comparable command: `go test -json ./integration/gateway -run
'^TestUnixStreamSharedOperations$' -count=3 -timeout=120s`.
Parent seconds before: 7.62, 5.00, 5.00; after: 5.39, 3.00, 3.00.
Package seconds: 18.017 versus 11.888; command wall seconds: 19.04 versus 15.09.
Load before start/end: 12.05/15.52/15.48 and 12.22/15.33/15.42;
after: 11.59/15.04/15.31 and 11.58/14.87/15.25. First samples include builds;
normal timing commands did not overlap other checks from this work. Warm samples
match the two-second validity reduction, without proving whole-suite performance.
Normal and race count-three checks and package vet pass. Raw evidence is in
`/tmp/airc-roadmap-baseline/unix-terminal-*`.

## Framed stdio

Before source: `3c5bd59`. Owned anonymous-pipe conformance now uses three-second
validity. Frame correlation, native live delivery, cursor renewal, SIGINT with
an active watch, journal unlocking, and independent native message/reply checks
remain unchanged.

Comparable command: `go test -json ./integration/gateway -run
'^TestStdioPipeRunsFullConformanceAndSIGINTWatchShutdown$' -count=3 -timeout=120s`.
Parent seconds before: 6.66, 5.03, 4.97; after: 6.20, 3.00, 3.00.
Package seconds: 17.015 versus 12.757; command wall seconds: 17.89 versus 16.30.
Load before start/end: 11.00/14.30/15.02 and 12.37/14.37/15.02;
after: 10.71/13.87/14.83 and 13.44/14.33/14.97. First samples include builds;
normal timing commands did not overlap other checks from this work. Warm samples
match the two-second validity reduction; command totals are affected by setup
and changing load. Normal and race count-three checks and package vet pass.
Raw evidence is in `/tmp/airc-roadmap-baseline/stdio-terminal-*`.

## Raw QUIC datagram lease decision

At `682c55b`, raw QUIC has one fixed-duration datagram witness:
`TestRawQUICQuietDatagramWatchExpiresWithContiguousSequence`. It checks actual
13–18-second elapsed bounds, contiguous terminal sequence, quiet heartbeat
contents, stable cursor, independently observed native lease release and reuse.
`TestRawQUICDatagramWatchRetainedLiveAndConnectionCancellation` instead closes
the connection after retained/live pages; it does not wait for grant expiry.
There is no repeated full-duration terminal check to shorten. Retain the one
real expiry witness rather than replacing its elapsed-time assertion with a
short signed envelope.

Unchanged scenario sources reuse verified `f195def` evidence:
`status-affected-false.json` records 15.08 seconds for expiry and 0.16 for live
cancellation; `status-affected-true.json` records 16.30 and 1.35 seconds with race
instrumentation. Both real scenarios passed in both modes. Evidence is under
`/tmp/airc-roadmap-baseline`; the runtime-instrumentation and fixture isolation
checks remain documented in testing.md. No speedup is claimed for this decision.

## WebTransport datagram lease decision

At `9d0b392`, `TestWebTransportQuietDatagramWatchEndsWithContiguousSequence` is
WebTransport's sole full-duration datagram terminal witness. It retains actual
13–18-second elapsed bounds, contiguous sequence, quiet heartbeat contents,
stable cursor, independent native lease release and session reuse.
`TestWebTransportDatagramWatchSequencesRetainedAndLive` consumes retained and
live pages and returns to fixture cleanup without waiting for lease expiry.
There is no duplicate full-duration wait to shorten; the real witness remains.

The quiet scenario source is unchanged since `f195def`. Reuse its verified
normal/race passes from `status-affected-false.json` and
`status-affected-true.json`: 15.08 and 16.26 seconds respectively, retained under
`/tmp/airc-roadmap-baseline`. No speed improvement is claimed, and browser
certificate/remote-path qualification remains separate.

## UDP mailbox read-lease decision

At `b47af32`, UDP has one full-duration read-lease terminal witness:
`TestUDPQuietWatchHeartbeatCursorAndLeaseExpiry`. Its actual 13–18-second bound,
ordered response sequence, cursor stability, empty heartbeat contents, native
observer release and fresh request remain. The retained/live metadata scenario
returns after its independent CHECK comparison without waiting for expiry.
There is no repeated read-lease terminal wait to shorten. Datagram assembly,
delivery retention and source-idle timers are distinct roadmap items.

Both scenario sources are unchanged since `f195def`. Reuse normal/race passes
from `status-affected-false.json` and `status-affected-true.json`: quiet expiry
15.10/16.31 seconds, retained/live 0.08/1.27 seconds. Raw evidence remains under
`/tmp/airc-roadmap-baseline`. No speedup is claimed for retaining the witness.

## DTLS read-lease decision

At `1187ca7`, `TestDTLSQuietWatchContiguousHeartbeatAndLeaseExpiry` is the sole
full-duration DTLS read-lease terminal witness. Retain its actual 13–18-second
bound, contiguous sequence, stable scoped cursor, empty heartbeats, independently
observed native lease release and fresh datagram request. The retained/live test
closes the remote DTLS connection and independently awaits release rather than
waiting for grant expiry. There is no duplicate full-duration wait to shorten.
DTLS idle policy and slow-reader checks remain separate roadmap items.

Scenario sources are unchanged since `f195def`. Existing normal/race evidence
records quiet expiry at 15.07/16.25 seconds and remote-close retained/live at
0.13/1.30 seconds in `status-affected-false.json` and
`status-affected-true.json`, under `/tmp/airc-roadmap-baseline`. No speedup is
claimed for this decision.
