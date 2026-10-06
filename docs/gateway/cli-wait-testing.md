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

## Own-message filtering

Before source: `0732a97`. Removed the supplementary 200 ms negative window
from `TestCheckWaitWakesWhenAMessageArrives`. Its existing acknowledged observation
fence precedes the own message. Exact output must contain only the later writer
reply, so an empty or own-message result cannot pass. Real wait limit, prompt
result bound and consumed-cursor assertion remain unchanged.

Focused normal count-three parent seconds before: 0.21, 0.21, 0.21; after:
0.01, 0.01, 0.01. Package seconds: 0.879/0.337; command wall: 1.24/1.77.
Timing did not overlap validation; build cost obscures command wall comparisons.
Own-message, real quiet-timeout and direct-message race count-three checks and
CLI package vet pass. Raw evidence: `/tmp/airc-roadmap-baseline/cli-own-negative-*`.
No whole-suite speedup is claimed.

## Mention filtering

Before source: `19d82c1`. Removed the supplementary 700 ms negative window.
Acknowledged live observation precedes unrelated chatter; exact later tagged
message output with `Mentioned=true` rejects premature empty/chatter results.
Real wait and response failure bounds remain unchanged.

Focused normal count-three parent seconds before: 0.71, 0.71, 0.71; after:
0.01, 0.01, 0.01. Package seconds: 2.393/0.327; command wall: 2.93/2.23.
Timing did not overlap validation; build cost remains in command wall. Mention
filter plus real quiet-timeout race count-three checks and CLI package vet pass.
Raw evidence: `/tmp/airc-roadmap-baseline/cli-mention-negative-*`. No whole-suite
speedup is claimed.

## Reaction filtering

Before source: `07fe34d`. Removed the supplementary 100 ms negative window and
added the existing acknowledged observation fence before the live reaction.
Exact later answer identity rejects reaction/empty results. Linked fields,
idempotence, thread retention, routing and rendering assertions remain intact.
Real reply-timeout coverage is unchanged.

Focused normal count-three parent seconds before: 0.11, 0.11, 0.11; after:
0.02, 0.01, 0.01. Package seconds: 0.546/0.344; command wall: 1.57/1.87.
Timing did not overlap validation; build cost obscures command wall comparisons.
Reaction filtering plus real reply-timeout race count-three checks and CLI
package vet pass. Raw evidence: `/tmp/airc-roadmap-baseline/cli-reaction-negative-*`.
No whole-suite speedup is claimed.
