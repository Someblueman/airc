# The fault-injection soak, expanded

18 October 2026, on the security-review branch, on a 4-vCPU Intel Xeon
2.10 GHz with Go 1.27.0, Linux AMD64. The [first soak](../chaos-soak-2026-10-17/README.md)
cut, froze and restarted links and daemons under four room followers, two
MQTT 5 sessions and a wake notice receiver. This run adds what it left out:

- **Slow links.** A proxy forwards in chunks of 256 to 4096 bytes with 20
  to 200 ms before each, for 10 to 40 seconds: congestion, or loss as TCP
  turns it into delay. Followers' keepalives, MQTT keepalives and the
  posters' requests all ride through it.
- **MQTT over WebSocket.** A third MQTT 5 persistent session reaches the
  gateway's `mqtt_websocket` listener through its own faulted proxy.
- **An inbox follow.** A registered account follows its inbox over
  `framed` while both posters send it a direct message every tenth post;
  it must see every one exactly once.
- **Wake timing.** Every direct message to the sleeping account must be
  covered, within 90 seconds of its acknowledgement, by a wake notice
  whose `latest` is it or a later one. Before, only the last message had
  to wake it. Only news counted in the two seconds before an aircd SIGKILL
  is excused, since aircd saves wake news at most once a second.

Run it with `AIRC_CHAOS_DURATION=30m go test ./integration/gateway -run
'^TestChaosSoak$' -count=1 -timeout 60m -v`; `AIRC_CHAOS_SEED` replays a run.

## The run

Seed 16102134152971684144, 30 minutes of faults: 13 cuts, 6 freezes and 8
slow periods across all five links; 6 gateway restarts (2 SIGKILL); 10
aircd restarts (4 SIGKILL) and 8 pauses of up to 15 seconds.
[run.json](run.json) is the summary and [run.events.txt](run.events.txt)
every fault and recovery.

| What | Result |
| --- | --- |
| Posts | 8,367 acknowledged, each in aircd's history exactly once; none unacknowledged |
| Room followers (2 framed, 2 WebSocket) | 7,364 each, none missed, none twice, no gaps |
| Inbox follower | 670, none missed, none twice |
| MQTT 5 sessions (2 TCP, 1 WebSocket) | 7,364 each, none missed; 231, 231 and 174 redelivered, as QoS 1 allows after a gateway restart restores a session from its last snapshot |
| Wake notices | 180 notices for 334 direct messages; every one covered within 90 s; none excused |

The follows resumed 117 times and the MQTT sessions reconnected 29 times;
none of it lost a message.

## What it found

Writing the wake check showed a gap that the first soak's single final
message could not: news waiting out the quiet period, or a receiver's
backoff, lived only in aircd's memory, so a restart dropped the notice
until the next message came. aircd now saves that news with the
registration (at most once a second, and when it stops), and
`TestWakeNewsOutlivesARestart` holds it; this run is the first under that
fix, and every direct message was covered. The soak was not run against
the old code, so it shows the fix holding, not the bug.

## Not covered

Native IRC clients reading through the gateway's `irc_websocket` and
`irc_connect` tunnels (a plain IRC client has no position to resume
from), QUIC carriers, which UDP faults would need a different proxy for,
and more than one gateway.
