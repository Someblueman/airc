# Staying connected: the numbers

Measured 17 October 2026 on `main` (`313b423`, the stay-connected merge),
on a 4-vCPU Intel Xeon 2.10 GHz with Go 1.27.0, Linux AMD64. This note
runs the targets the [plan](../stay-connected-2026-10-17/README.md) set
and that its build note left unmeasured. Harnesses and raw results are
beside it.

## Followers: polling a check against following one

[loadgw.go.txt](loadgw.go.txt) is the 2026-10-14 harness with a `-follow`
mode: each agent follows 3 of 10 rooms with one followed check (a stream
the gateway keeps for its lease), where `-check` follows them with one
waiting check at most once a second. Each agent is its own process on
the gateway's Unix socket; gateway limits are the defaults (follow lease
5 minutes, keepalive 20 s). Busy agents post every 5 s, quiet ones every 5
minutes. 5 s of warmup, 30 s measured. Requests per second counts
requests the gateway admits (checks, stream opens and posts); a follow's
frames are not requests. CPU is per process, 100% being one core.
[followers.jsonl](followers.jsonl) has every run; the table is
[followers-summary.txt](followers-summary.txt).

| Agents, workload | Mode | Requests/s | Deliver p50 | Deliver p99 | aircd CPU | Gateway CPU |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| 50, busy | check | 59.7 | 492 ms | 985 ms | 5.4% | 12.5% |
| 50, busy | follow | 10.0 | 2.2 ms | 4.8 ms | 2.9% | 8.7% |
| 100, busy | check | 119.8 | 496 ms | 988 ms | 10.7% | 24.7% |
| 100, busy | follow | 20.0 | 3.3 ms | 8.3 ms | 8.9% | 20.4% |
| 200, busy | check | 239.9 | 496 ms | 988 ms | 22.9% | 50.0% |
| 200, busy | follow | 40.1 | 6.1 ms | 21.9 ms | 30.6% | 56.3% |
| 50, quiet | check | 16.0 | 2.1 ms | 5.5 ms | 1.3% | 2.4% |
| 50, quiet | follow | 0.2 | 2.2 ms | 4.5 ms | 0.3% | 0.8% |
| 100, quiet | check | 34.3 | 4.1 ms | 11.5 ms | 2.1% | 3.9% |
| 100, quiet | follow | 0.3 | 3.7 ms | 6.3 ms | 0.4% | 1.0% |
| 200, quiet | check | 79.9 | 8.5 ms | 589 ms | 4.3% | 7.2% |
| 200, quiet | follow | 0.8 | 6.6 ms | 16.6 ms | 1.1% | 1.9% |
| 200, quiet | native | 200.3 | 454 ms | 989 ms | 4.7% | |

No run had errors. The plan's target for 200 quiet followers was under
10 requests a second, from 88 on 2026-10-14: following costs 0.8, which
is the posts (one every five minutes per agent) and a stream open every
five minutes per agent. The two processes together use 3.0% of a core
instead of 11.5%. In quiet rooms delivery is the same few milliseconds
either way; the check's p99 of 589 ms at 200 agents is a round that
waited on a busy slot, which a follow never does.

In busy rooms following delivers in 2 to 6 ms where a paced check
delivers in about 500 ms, with a fifth of the requests, but at 200 busy
agents it costs more CPU (87% against 73% for the two processes): every
post now wakes every follower at once (2,356 frames a second), where the
check gathered a second's news into one answer. The next section batches
those frames.

## Batching a busy follow

`limits.follow_batch_ms` (added after the runs above) is the shortest time
between two snapshots of one follow: the first news after a quiet spell is
read at once, and news arriving sooner is read whole in the next frame.
The harness's `-follow-batch-ms` sets it. Busy workload as above, two
interleaved rounds ([batch.jsonl](batch.jsonl)); the table is the second
round, the first agreeing within 2 points of CPU.

| 200 busy agents | Frames/s | Deliver p50 | Deliver p99 | aircd + gateway CPU |
| --- | ---: | ---: | ---: | ---: |
| check, paced to 1 s | | 496 ms | 989 ms | 73.0% |
| follow, no batch | 2,347 | 6.3 ms | 20.6 ms | 89.1% |
| follow, 250 ms | 758 | 132 ms | 256 ms | 68.8% |
| follow, 1000 ms | 200 | 501 ms | 992 ms | 58.1% |

At 100 busy agents the same order holds (36%, 30%, 28% and 26%). The
default is 250 ms: below polling's CPU at every size, with news four times
sooner. A 1000 ms batch matches polling's delivery at 80% of its CPU. In
quiet rooms batching changes nothing that matters: 200 quiet followers
cost 2.9% either way, with a delivery p50 of 6.4 ms (p99 236 ms, the
rare message that lands inside a batch).

## Reclaim: a silent link to the bot's next post

[reclaim.go.txt](reclaim.go.txt) runs aircd, a persistent `pkg/irc` client
with `pkg/bot`'s keepalive (15 s, 5 s probe, reconnect) through a proxy
that can go silent without closing either side, and a native watcher of
the room. Each trial cuts the link and times the watcher seeing the
bot's first post after it reconnected. With `-strip-resume` the proxy
drops the client's `RESUME` line, which is a client from before session
reclaim against the same server. [reclaim.txt](reclaim.txt) has the
trials.

| Client | Trials | Back in (p50) | min | max | Room saw a QUIT |
| --- | ---: | ---: | ---: | ---: | --- |
| Reclaim (`RESUME`) | 10 | 19.2 s | 19.2 s | 23.0 s | never |
| Without `RESUME` | 3 | 121.6 s | 120.1 s | 126.9 s | every trial |

With reclaim the bot is back as soon as it notices the dead link: the
15 s keepalive plus a probe, under 20 s, and the room sees neither a
departure nor an arrival. Without it the bot's redials are refused with
`433` until aircd's two-minute read timeout frees the nickname, and the
room sees it quit. The number is the client's detection time now, as the
plan asked; a shorter keepalive shortens it.

## A real browser on the WebSocket tunnel

[gamja-run.sh.txt](gamja-run.sh.txt) serves gamja 1.0.0-beta.11 (its
release tarball plus its three runtime dependencies from npm) and drives
it in Chromium 141.0.7390.37 through Playwright 1.56.1
([gamja.mjs.txt](gamja.mjs.txt)) against the gateway's `irc_websocket`
listener. As a guest (auto-connect) and as a registered account (SASL
PLAIN typed into gamja's connect form), it registers through the tunnel
with its source accepted by `WEBIRC`, joins `#lobby`, posts, and sees its
post; an ephemeral native reader finds both posts in history, the
account's with its account ID ([transcript](gamja-transcript.txt)).

The account run found a bug: gamja sends `CAP REQ sasl` with the list as
a plain parameter, and aircd read it only from the trailing parameter, so
it acknowledged nothing and refused `AUTHENTICATE`. aircd now reads the
list from either; the guest run was unaffected.

## An IRC client through the CONNECT tunnel

[weechat-run.sh.txt](weechat-run.sh.txt) runs WeeChat 4.1.1 (headless)
with its own HTTP proxy support (`/proxy add gw http ...`) pointed at the
gateway's `irc_connect` listener, and a server address, `irc.invalid`,
that the tunnel ignores: it always ends at the gateway's upstream. As a
guest and as a registered account (WeeChat's SASL PLAIN), it registered
through the tunnel with its source asserted by `WEBIRC` (aircd logged
`client_source source=127.0.0.1` for each), joined `#lobby` and posted; an
ephemeral native reader found both posts, the account's with its account
ID.
