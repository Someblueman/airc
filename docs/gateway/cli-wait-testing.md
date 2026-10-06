# CLI wait verification

## Exact reply filtering

Before source: `e165f70`. Removed the reply-filter scenario's supplementary
150 ms negative observation. Its final output must contain only the exact ID
of the matching reply sent later, so a result caused by preceding unrelated
traffic cannot pass. Added the existing tap's acknowledged observation fence
before live traffic; this also closes the previous startup ambiguity. Own and
nested replies, unrelated mentions/DMs and other conversations remain covered.
Actual retained-reply and quiet-timeout checks remain unchanged.

Focused normal count-three parent seconds before: 0.17, 0.17, 0.16; final:
0.02, 0.02, 0.02. Package seconds: 0.830/0.368; command wall: 1.29/1.88. Builds
make command wall an unreliable speed comparison; the focused test removes
the supplementary wait. The intermediate version without the readiness fence
is retained in raw evidence. Final timing overlapped the intermediate race
check. No whole-suite speedup is claimed. Final filtering plus retained-reply/
actual-timeout race count-three checks and CLI package vet pass. Raw evidence:
`/tmp/airc-roadmap-baseline/cli-reply-negative-*`.
