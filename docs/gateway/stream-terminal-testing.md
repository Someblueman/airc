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
