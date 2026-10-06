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
