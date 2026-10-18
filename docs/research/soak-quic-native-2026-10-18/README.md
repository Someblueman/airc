# The soak over HTTP/3 and the native tunnel

18 October 2026, on the `mqtt-followups` branch, on a 4-vCPU Intel Xeon
2.10 GHz with Go 1.27.0, Linux AMD64. The
[expanded soak](../chaos-soak-expanded-2026-10-18/README.md) left out the
QUIC carriers and plain IRC clients through the gateway's tunnels. This
run adds both:

- **HTTP/3 over a faulty UDP path.** A UDP proxy can rebind every flow
  (the gateway sees each client move to a new address, as after a NAT
  rebinding, and QUIC must migrate), blackhole datagrams, delay each one,
  or drop 5–40% of them at random for 10–40 seconds. A reader over HTTP/3,
  which cannot stream, waits on the gateway with 20-second checks.
- **IRC through `irc_connect`.** A plain IRC client reaches aircd through
  the gateway's HTTP CONNECT tunnel, observes the rooms, and after every
  reconnect catches each room up from history before taking messages
  live. It may see a message again, which is counted, but must miss none.

## Run 1: a stall after every gateway restart

Seed 8037704936111224335 ([run1-before-fix.json](run1-before-fix.json)):
30 minutes, 12 gateway restarts, 10 aircd restarts, 19 cuts or rebinds,
7 freezes, 7 slow periods, 3 loss periods. Nothing was lost: every reader,
the HTTP/3 one and the tunnelled IRC client included, had all 7,821 room
messages, and all 8,887 posts were in history once.

But the HTTP/3 reader logged 1,042 failed checks, 1,027 of them `http3:
conn unusable: 0-RTT rejected`, in runs of five to nine minutes, each
starting at a gateway restart ([events](run1-before-fix.events.txt)). A
restarted gateway has new TLS ticket keys, so it refuses the 0-RTT of a
client resuming its old session. quic-go then marks the HTTP/3 client
conn built on that early connection unusable, while the QUIC connection
itself completes its handshake and stays open; `airc` only redialled when
the QUIC connection closed, so it failed every request until the
connection idled out. Any `airc` on HTTP/3 would have done the same after
a gateway restart.

The fix moves the connection on with quic-go's `NextConnection`, builds a
new HTTP/3 client conn on it, and sends the request again: nothing sent
in rejected early data reached the gateway, and every contract request is
replay-safe besides. `TestHTTP3RecoversFromRejected0RTT` restarts an
HTTP/3 server with new ticket keys under a client; it fails with the same
error without the fix.

## Run 2: the same seed after the fix

[run2-same-seed-after-fix.json](run2-same-seed-after-fix.json): the same
faults in the same order. The HTTP/3 reader logged 27 failed checks, all
during the faults themselves (`upstream_unavailable` while aircd was
down, deadlines while its path was blackholed), and no 0-RTT rejection.

| What | Run 2 |
| --- | --- |
| Posts | 8,888 acknowledged, each in history once |
| Room followers (framed, WebSocket), HTTP/3 checks, MQTT 5 (TCP and WebSocket) | 7,823 each, none missed; MQTT redelivered about 440 each after gateway restarts, as QoS 1 allows |
| IRC through `irc_connect` | 7,823, none missed, one seen twice across 26 reconnects |
| Inbox follow | 711, none missed or doubled |
| Wake notices | 185 for 355 direct messages, every one within 90 s |

## What else changed on the branch

- `airc --hedge` follows over two gateway connections at once.
  `TestFollowHedgedRidesOutAStalledLink` stalls one link silently in the
  middle of a stream: all 40 messages arrived once and in order, the last
  within seconds of the stall, where a lone follow would have waited out
  its 150-second idle.
- MQTT 5 shared subscriptions and per-listener session caps; see
  [brokers](../../gateway/brokers.md#mqtt-5).
