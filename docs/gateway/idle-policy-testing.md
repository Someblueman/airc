# Gateway idle-policy evidence

Keep production idle/negotiation deadlines and real closure/admission witnesses.
Use existing completion signals to end completed waits; authenticated traffic
must renew activity, while malformed or unauthorized traffic must not. Each
scenario owns its listeners, credentials, peers and shutdown.

## WebRTC invalid traffic and valid renewal

Before Go/test source: `1153da4`. There is one real WebRTC 30-second invalid-
traffic idle witness, with no duplicate full-duration wait to remove. Its loop
now selects the actual channel-close callback instead of sleeping after closure,
retaining one-second invalid traffic and the original 36-second failure bound.
The lower-bound clock begins before peer negotiation to include setup time.

A second peer uses acknowledged initial authenticated activity, a renewal at
that activity plus 20 seconds, and a successful request after the original
activity plus 31 seconds (idle interval plus one reaper tick). This adds a real
valid-renewal control within the same idle window. The extra boundary wait is
necessary to prove survival past the original deadline, not a substitute for
invalid-peer closure. Production timers remain unchanged.

Command: `go test -json ./integration/gateway -run
'^TestWebRTCInvalidTrafficDoesNotRenewPeerIdleDeadline$' -count=1 -timeout=120s`.
Parent seconds before/final: 32.52/33.40; package: 32.939/33.908; command wall:
33.96/36.35. An initial candidate passed at 32.97 parent / 36.16 wall seconds;
review identified its lower-bound clock after negotiation as a possible flake,
so the clock and authenticated activity anchor were corrected and rechecked.
The first candidate's raw evidence is preserved as `after-attempt1`.

Load before start/end: 13.45/14.13/14.89 and 13.17/13.99/14.81; final:
11.56/13.03/14.32 and 11.92/12.95/14.23. Measurements were isolated from other
validation in this work, and each first run includes fixture builds. The added
renewal control increases coverage; no speed improvement is claimed. Normal,
race and package vet checks pass. Raw JSON, stderr, load and validation evidence
is retained under `/tmp/airc-roadmap-baseline/webrtc-idle-*`.

## DTLS idle-window decision

At `ccbb5cf`, `TestDTLSMalformedAndUnauthorizedRecordsDoNotRenewIdle` is the
only real DTLS idle-policy window. It combines two malformed/unauthorized
traffic classes, a session-capacity rejection, authenticated renewal at 20
seconds, actual 28–40-second slot-release bounds, fresh authenticated admission,
and continued usability of the renewed peer. These are distinct claims sharing
one unchanged production idle window, rather than repeated idle-expiry waits.
Retain this single real witness and its validity controls; shortening its wait
would weaken the observed idle/admission boundary.

The scenario file is unchanged since `7118107`. Existing individual normal/race
passes in `normal-authorized.json` and `race-authorized.json` are 32.57/32.67
seconds, retained under `/tmp/airc-roadmap-baseline`. Those full baseline commands
later timed out in other scenarios and do not establish final acceptance. No
runtime change or speed improvement is claimed for this decision.

## UDP source-idle reaper

Before source: `4fa5842`. `TestUDPInvalidTrafficCannotRetainAdmissionLease` now
uses the existing `reapSources(now)` boundary instead of waiting five wall-clock
seconds. A small wrapper around a real UDP socket signals when the owner starts
its next read. For these synchronous invalid payloads, that fences processing
of the preceding packet, including any activity update. No fake I/O or production
time hook is introduced.

Twenty fresh AGD1 records carry invalid application bodies, bypass completion
cache suppression, and must leave the original source pointer/activity unchanged
with no active requests. The reaper retains admission one nanosecond before idle
expiry, releases its source and slot exactly at expiry, and a twenty-first real
packet must acquire a fresh source and slot. Owned cancellation still joins the
socket reader; existing UDP bounds/shutdown and packet truncation tests also run.
Production idle duration and reaper scheduling are unchanged.

Command: `go test -json ./internal/gateway/adapter -run
'^TestUDPInvalidTrafficCannotRetainAdmissionLease$' -count=3 -timeout=120s`.
Before parent seconds: 5.07, 5.07, 5.07; after: 0.00, 0.00, 0.00 at JSON's
reported precision (not zero execution cost). Package seconds: 15.644/0.430;
command wall: 17.52/2.24. Load before start/end: 14.50/13.60/14.27 and
14.33/13.61/14.26; after: 12.64/13.25/14.10 at both boundaries. Timing runs were
isolated from validation. Normal count-three and affected UDP/packet race
count-five checks and adapter package vet pass. Raw evidence is retained under
`/tmp/airc-roadmap-baseline/udp-idle-*`.

## CoAP TCP missing-CSM decision

At `f7a4be7`, `TestServeCoAPTCPClosesAfterMissingCSM` is the sole real missing-
CSM termination witness. It reads the server's CSM over an owned duplex pipe,
withholds the peer CSM, and waits for the serving owner to terminate under the
unchanged five-second negotiation grace. The 6.5-second timer is a failure bound,
not an unconditional extra sleep. Separate CSM validation tests operate directly
on messages without this wait. Retain the one actual failed-negotiation witness;
there is no repeated grace wait to shorten.

Its test file is unchanged since `7118107`. Individual normal/race passes in
`normal-authorized.json` and `race-authorized.json` are both 5.00 seconds. Raw
baseline evidence is under `/tmp/airc-roadmap-baseline`; later integration
baseline timeouts do not invalidate these leaf results or certify final
acceptance. No runtime change or speedup is claimed for this decision.
