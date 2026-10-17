# Fault injection across aircd and the gateway

Run 17 October 2026 on `main` after the stay-connected work (PRs #38 to
#45), on a 4-vCPU Intel Xeon 2.10 GHz with Go 1.27.0, Linux AMD64. Each
part of that work was tested on its own; this soak runs them together under
faults for half an hour and checks the outcome against aircd's own history
file.

```sh
AIRC_CHAOS_DURATION=30m go test ./integration/gateway -run '^TestChaosSoak$' -count=1 -timeout 60m -v
```

## What runs

`integration/gateway/chaos_test.go` starts aircd (history file, accounts,
wake notices) and the gateway (framed, WebSocket and MQTT listeners) as
real processes, and every client reaches them through a proxy
(`chaos_infra_test.go`) that can cut its connections or freeze them: hold
them open and pass nothing, as a dead NAT path does, for 5 to 40 seconds.

- Two posters, accounts, post a numbered message every 400 ms with
  `pkg/fallback`, one preferring native IRC and one the gateway, each
  request keeping its ID across every attempt and carrier; every 25th is a
  direct message to an account with a wake URL.
- Four followers, guests, follow three rooms over framed and WebSocket,
  resuming after the last message they saw whenever a follow ends.
- Two MQTT 5 persistent sessions subscribe to the rooms and reconnect with
  Clean Start 0; a lost session subscribes again after its last message.
- A wake receiver records the notices aircd sends.

Every 10 to 40 seconds one fault: a cut (30%), a freeze (20%), a gateway
SIGTERM (15%) or SIGKILL (10%) and restart, an aircd pause with SIGSTOP for
3 to 15 seconds (10%), or an aircd SIGTERM (10%) or SIGKILL (5%) and restart.
Then 20 calm seconds, the posters stop, a last direct message is sent, and
every reader gets three minutes to catch up.

The verdict: every acknowledged post is in aircd's history exactly once;
no reader missed a message; followers saw none twice (MQTT QoS 1 may
redeliver, so its duplicates are counted, not failed); and the last direct
message woke its account.

## Results

| Run | Faults | Posts | Followers | MQTT sessions | Wake |
| --- | --- | --- | --- | --- | --- |
| [1](run1-before-fix.json), seed 5818422723055184715 | 18 gateway restarts (8 SIGKILL), 7 aircd restarts (4 SIGKILL), 7 pauses, 27 link faults | 8568, none lost or doubled | 8226 each, none missed or doubled | **1541 missed each** | woke |
| [2](run2-same-seed-after-fix.json), the same seed, after the fix | the same | 8568, none lost or doubled | 8226 each, none missed or doubled | 8226 each, about 790 redelivered | woke |
| [3](run3-new-seed.json), seed 8830618716642717531 | 13 gateway restarts (5 SIGKILL), 11 aircd restarts (all SIGTERM), 5 pauses, 32 link faults | 8574, none lost or doubled | 8232 each, none missed or doubled | 8232 each, about 730 redelivered | woke |

[run3-new-seed.events.txt](run3-new-seed.events.txt) is the third run's
event log: each fault, each follow that ended and resumed, each MQTT
reconnect.

## What run 1 found

Both MQTT sessions stopped receiving 25 minutes in, after a 40-second
freeze of their link and a gateway SIGKILL. The gateway restored them from
their snapshot well behind, and every followed check from those positions
asked for 200 messages that together passed the 64 KiB a check may carry.
The gateway refused the whole check with `capacity_exceeded`, and every
retry from the same positions, in every later gateway process, met the
same refusal. The followers on framed and WebSocket ask for the same 200
and escaped only because no fault left them that far behind; any client
with a large enough backlog, or messages large enough, would have stalled
the same way.

The fix, in `internal/gateway/backend`: a check too large to carry is asked
for again with half as many messages, down to one, and AIRC cuts its pages
at the messages it leaves out, so a backlog is read in several checks; a
result still over the limit is cut to its oldest messages, each page ending
at a whole message with status `more`. Finding it showed a second defect:
the gateway stopped reading an oversized answer midway, which would have
left the rest to be read as the next CHECK's answer on the same session;
it now reads it to its end first. (Before the fix a failed session was
closed, never reused, so this had not yet mattered.)
`TestBackendCheckFitsLargeBacklog` reads 60 messages of 3 KB with a
200-message check, which was refused before and now takes five checks
with every message once, in order.

## What it did not cover

The soak runs on loopback, with one gateway and one aircd; network latency,
packet loss short of a freeze, and disk faults are outside it. Wake notices
are checked only at the end: a direct message that arrives while aircd is
down for a restart, and a notice still waiting out its quiet period when
aircd stops, are not followed up, since aircd keeps pending notices in
memory.
