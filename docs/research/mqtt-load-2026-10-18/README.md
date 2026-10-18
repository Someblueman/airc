# MQTT 5 subscribers under load

Run 18 October 2026 on main at `8f500d0` (and once on the security-review
branch), on a 4-vCPU Intel Xeon 2.10 GHz with Go 1.27.0, Linux AMD64. The
question: how many MQTT 5 clients, each a session following a few rooms
through the gateway's `mqtt` listener, can one gateway and aircd carry, and
what does that cost? What the listener does is in
[brokers](../../gateway/brokers.md#mqtt-5).

## The harness

[mqttload.go.txt](mqttload.go.txt) (module [go.mod.txt](go.mod.txt); it
imports nothing from this repository) starts a scratch `aircd` and a
gateway whose only listener is `mqtt` with its defaults, apart from higher
connection limits. It connects N MQTT 5 clients, each a guest named by
`airc-nick`, Clean Start with a Session Expiry Interval of ten minutes,
subscribed at QoS 1 to 3 of 10 rooms chosen at random, acknowledging every
message. Four plain IRC connections to `aircd` post `t=<send time> p=<seq>`
round robin over the rooms at a fixed rate, so nothing passes through the
listener under test but the subscribers. After a 10 s warmup it measures
60 s (30 s for the last run): every post's expected deliveries (its room's
subscribers) against those that arrived, the latency from post to the
subscriber reading it, and CPU and resident memory of both daemons from
`/proc`. Run it as `go build` in a copy of this directory with the `.txt`
suffixes removed, then
`./mqttload -bin DIR -subs 500 -rate 20 -label x -out results.jsonl`, with
`DIR` holding `aircd` and `airc-gateway`.

## Results

[results.jsonl](results.jsonl) has every field. CPU is percent of one core
over the measured window.

| Subscribers | Posts/s | Deliveries/s | Delivered | p50 / p99 ms | Max ms | Gateway CPU, RSS | aircd CPU, RSS |
| ---: | ---: | ---: | --- | --- | ---: | --- | --- |
| 100 | 20 | 600 | 36,000 of 36,000 | 115 / 249 | 256 | 10%, 39 MiB | 6%, 25 MiB |
| 250 | 20 | 1,500 | 89,922 of 89,922 | 116 / 250 | 257 | 20%, 59 MiB | 13%, 40 MiB |
| 500 | 20 | 3,000 | 180,147 of 180,147 | 117 / 251 | 1,215 | 40%, 97 MiB | 25%, 67 MiB |
| 500 | 100 | 15,000 | 900,000 of 900,000 | 129 / 253 | 296 | 97%, 95 MiB | 61%, 76 MiB |
| 500 | 0 (quiet) | 0 | — | — | — | 1.6%, 84 MiB | 1.0%, 51 MiB |
| 1,000 | 20 | 6,000 | 360,000 of 360,000 | 124 / 250 | 277 | 70%, 162 MiB | 45%, 118 MiB |
| 500, security branch | 20 | 3,000 | 180,000 of 180,000 | 127 / 250 | 286 | 40%, 97 MiB | 26%, 66 MiB |

No subscriber was disconnected and no message was delivered twice in any
run. Connecting took 40–50 ms at the median and about 100 ms at p99,
including the identify round trip to aircd and the SUBACK.

**Latency is the follow batch.** p50 near 120 ms and p99 near 250 ms at
every size and rate is `limits.follow_batch_ms` (250 by default): each
session's followed check gathers news for up to that long before it
publishes, so a delivery waits half the window on average. A listener that
wants lower latency lowers it, at more frames per message. Two runs had a
slow tail beyond that (a maximum of 1.2 s at 500 subscribers on main, not
seen in the rerun on the branch, and 4.3 s in the over-the-cap run), but
p99 stayed within 280 ms everywhere.

**Cost grows with sessions and with deliveries.** At 20 posts/s the
gateway's CPU rose from 10% of a core at 100 subscribers to 70% at 1,000,
about 0.07% per subscriber, and aircd's from 6% to 45%. At 500
subscribers, five times the deliveries (100 posts/s) cost 2.4 times the
gateway CPU, so much of the cost is per session per batch window, not per
message. Memory grew about 135 KiB per subscriber in the gateway and 100
KiB in aircd. Quiet sessions cost almost nothing: 500 idle subscribers used
under 2% of a core in each daemon.

**The limit is the session cap.** With 1,100 subscribers (the last line of
`results.jsonl`) the 1,025th CONNECT and those after it got CONNACK `0x97`
"too many sessions": the listener keeps at most 1024 MQTT 5 sessions, clean
or not. The 1,024 that connected received everything; the 13,680 missing
deliveries are exactly the 76 refused subscribers' share. The backend's
default `limits.waits` is also 1024, so a gateway meant for more than about
a thousand followers needs both raised, and the session cap is a constant
today.

## Not measured

MQTT over WebSocket, TLS, QoS 0, registered accounts (their sessions are
the same code), subscribers that resume after a drop (the
[chaos soak](../chaos-soak-2026-10-17/README.md) covers that for a few
sessions), and more than one gateway host.
