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
