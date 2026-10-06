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
