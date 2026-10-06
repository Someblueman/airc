# Slow-reader verification

## gRPC flow-control witness

Before source: `1499615`. The existing real TLS gRPC client still receives the
initial page, pauses consumption and generates 40 native messages. A test-only
credential wrapper observes decrypted HTTP/2 frames, verifies the advertised
static 65,535-byte stream window, counts outgoing stream replenishment and
subtracts actual incoming DATA (including flow-controlled padding). It requires
zero remaining credit while part of a gRPC message is still pending: observed
flow-control pressure, rather than inference from a message count. Replenishment
is accounted before forwarding to avoid overstating pressure from scheduling.
Framing state is protected across concurrent reads/writes. Production code,
TLS verification and SDK behavior are unchanged.

Normal repetitions observed 3,911, 3,911 and 3,912 message bytes pending at
exhaustion. The independent native observer must return to baseline at least two
seconds after pressure, within the original two-to-eight-second generation
bound, while the client context remains active. The ordinary native read lease
and client cancellation cannot satisfy those bounds. The fixed message flood
remains unchanged in this checkpoint; reducing it is a separate roadmap item.

Focused normal count-three parent seconds before: 6.71, 3.09, 3.11; after:
6.33, 3.11, 3.12. Package seconds: 13.422/13.250; command wall: 15.22/16.55.
First samples include builds. Timing did not overlap validation; stronger
coverage shows no warm runtime improvement. Focused race count-three and
integration package vet pass. Raw evidence:
`/tmp/airc-roadmap-baseline/grpc-pressure-*`, including the initial witness run.
These results verify this carrier witness, not whole-suite acceptance or speed.
