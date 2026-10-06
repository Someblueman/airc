# Raw collector wait verification

## Pin no-write fence

Before source: `bbda894`. The foreign-proof pin exchange completes synchronously
with rejection. The test then sends a marker on the same native TCP stream. The
fixture records pin/unpin commands before recognizing the marker and returns it
through the same FIFO command channel; requiring the marker next proves no
preceding mutation was written. This replaces the supplementary 50 ms negative
window with ordered processing. Its one-second timer is only a failure bound.
Production deadlines and all other pin mutation cases remain unchanged.

Focused normal count-three seconds before: 0.05, 0.05, 0.05; after: 0.00, 0.00,
0.00 at reporter precision, not zero execution cost. Package seconds:
0.498/0.306; command wall: 1.06/1.17. Timing did not overlap validation; builds
obscure command wall comparisons. All pin mutation race count-three checks and
backend package vet pass. Raw evidence:
`/tmp/airc-roadmap-baseline/collector-pin-negative-*`. No whole-suite speedup
is claimed.

## Reply-signal no-write fences

Before source: `d1b511b`. The same ordered native-stream proof replaces six
malformed-parent 50 ms windows and the foreign-parent window. A shared test helper
sends the marker only after synchronous exchange completion. The fixture records
prepare/cancel commands before recognizing it, and the next FIFO entry must be
the marker. A forbidden mutation instead produces a recorded command and fixture
termination, so the single-slot channel cannot conceal it. Canonical controls,
rejection codes, acknowledgements and real timeout/noise cases remain intact.

Focused normal count-three malformed-matrix parent seconds before: 0.31, 0.31,
0.31; after: 0.01, 0.00, 0.00. Foreign-parent seconds: 0.05 each before, 0.00
each after at reporter precision, not zero execution cost. Package seconds:
1.330/0.312; command wall: 1.79/1.00. Timing did not overlap validation; builds
remain part of command wall. All reply-signal race count-three checks and backend
package vet pass. Raw evidence:
`/tmp/airc-roadmap-baseline/collector-signal-negative-*`. No whole-suite speedup
is claimed.

## Poll no-write completion records

Before source: `ad4840b`. Replace eight supplementary 50 ms windows (foreign
proof, five numerics, two proof boundaries) with joined fixture completion.
After synchronous collector return, close the client, join the fixture and
require its command record to be closed and empty. Buffered mutations remain
visible. Numeric and unexpected-receipt responses now keep the fixture reading
until client closure; previously it returned immediately after those responses,
which could miss a forbidden subsequent write. Channel closure precedes owner
completion. The actual 30 ms proof timeout remains unchanged.

Focused normal count-three seconds before/final:

| Scenario | Before | Final |
| --- | --- | --- |
| Foreign proof | 0.05 each | 0.00 each |
| Five preflight numerics | 0.26 each | 0.00 each |
| Timeout and unexpected receipt | 0.13 each | 0.03 each |

Zero values reflect reporter precision, not zero execution cost. Package seconds:
1.537/0.409; command wall: 1.96/1.11. Timing did not overlap validation; builds
remain part of command wall. The initial partial baseline selection and initial
implementation measurements remain in raw evidence; final measurements use the
complete selection and corrected post-response reader. All poll mutation race
count-three checks and backend package vet pass. Raw evidence:
`/tmp/airc-roadmap-baseline/collector-poll-negative-*`. No whole-suite speedup
is claimed.
