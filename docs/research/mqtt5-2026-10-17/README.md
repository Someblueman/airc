# MQTT 5 on the gateway: real clients

Run 17 October 2026 on the MQTT 5 branch, on a 4-vCPU Intel Xeon 2.10 GHz
with Go 1.27.0, Linux AMD64. This is the [plan](../stay-connected-2026-10-17/README.md)'s
fifth tier: an MQTT client that loses its link keeps its subscriptions and
gets what it missed when it comes back, from the broker it already speaks
to. What the listener does is in [brokers](../../gateway/brokers.md#mqtt-5).

## The run

[run.sh.txt](run.sh.txt) starts a scratch `aircd` and a gateway whose only
listener is `mqtt` with its defaults, registers the account `carol`, and
then drives three clients, with posts made by plain IRC connections to
`aircd` so nothing passes through the listener under test.
[paho_client.py.txt](paho_client.py.txt) is the paho-mqtt client. Set `S` to
a directory holding `bin5/aircd` and `bin5/airc-gateway`, and run it from a
copy of this directory with the `.txt` suffixes removed from `run.sh`.
[transcript.txt](transcript.txt) is its output, with the account ID and
generated client ID redacted.

| Client | Version | What it did | Result |
| --- | --- | --- | --- |
| `mosquitto_rr` | 2.0.18 | `channels.read` as a guest named by `airc-nick`, reply to `replies/rr-guest` | The detached result on the response topic |
| `mosquitto_pub` | 2.0.18 | `send` to `#lobby` at QoS 1 as `carol`, named by `airc-nick` and `airc-token` | CONNACK 0, PUBACK reason 0; history shows carol's account ID |
| `mosquitto_sub` | 2.0.18 | Persistent session (`-c -i sub-carol -x 600`) on `airc/room/lobby` as `carol`; exits after one message, two posts land, then reconnects | The live post on the first connection, both missed posts after reconnecting |
| paho-mqtt | 2.1.0 | Guest session with expiry 600: request with Response Topic and Correlation Data, subscription to `airc/room/lobby`, then the socket closed without DISCONNECT, two posts, and a reconnect with Clean Start 0 | The reply with correlation `paho-1`; the live post; both missed posts, in order |

An independent reader's `HISTORY #lobby` at the end lists every post once,
`carol`'s with her account ID.

## What the tests add

`integration/gateway/mqtt5_test.go` drives the listener with its own MQTT 5
encoder, independent of `mqttwire`: replies and their reason codes,
subscriptions and their refusals, takeover with DISCONNECT `0x8E`, the
redelivery of unacknowledged QoS 1 messages with DUP and their original
packet IDs, that another author cannot resume a session by its client ID,
and that a session without an author is not kept. It passes under `-race`.

## Not measured

Many subscribers at once on one gateway: each MQTT 5 connection with
subscriptions holds one followed check, so the
[follower numbers](../stay-connected-measure-2026-10-17/README.md) bound
its cost, but the MQTT path itself has not been loaded.
