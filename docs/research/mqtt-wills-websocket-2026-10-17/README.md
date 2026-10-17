# MQTT wills and MQTT over WebSocket: real clients

Run 17 October 2026 on the branch that adds them, on a 4-vCPU Intel Xeon
2.10 GHz with Go 1.27.0, Linux AMD64. Two pieces of the
[plan](../stay-connected-2026-10-17/README.md)'s fifth tier that the
[MQTT 5 note](../mqtt5-2026-10-17/README.md) left: a will, so an agent
that dies still updates its presence, and MQTT over WebSocket, so a browser
or an HTTP-only network can hold an MQTT session. What the listeners do is
in [brokers](../../gateway/brokers.md#mqtt-5).

## The run

[run.sh.txt](run.sh.txt) starts a scratch `aircd` and a gateway with an
`mqtt` and an `mqtt_websocket` listener, registers the account `carol`, and
drives the clients; posts come from plain IRC connections to `aircd`, and
presence is read with `airc directory --who carol`, both independent of
the listeners under test. To repeat it, copy this directory, remove the
`.txt` suffixes, put MQTT.js's `dist/mqtt.min.js` (`npm pack mqtt@5.16.0`)
and a `node_modules` with Playwright beside it, and set `S` to a directory
holding `binws/aircd`, `binws/airc-gateway` and `binws/airc`.
[transcript.txt](transcript.txt) is its output, account IDs redacted.

| Client | Version | What it did | Result |
| --- | --- | --- | --- |
| `mosquitto_sub` | 2.0.18 | As `carol`, with a will `presence.set` away "MQTT link lost"; killed with SIGKILL | `carol` away with that note, set by the gateway |
| `mosquitto_sub` | 2.0.18 | The same will; exits after one message (`-C 1`), a DISCONNECT with reason 0 | Presence unchanged |
| `mosquitto_sub` | 2.0.18 | The same will; SIGINT, on which mosquitto_sub sends DISCONNECT `0x04` (with will) | `carol` away: the client asked for its will |
| paho-mqtt | 2.1.0 | `transport="websockets"`, path `/mqtt`: `channels.read` with Response Topic and Correlation Data, a subscription to `airc/room/lobby` | The reply with correlation `ws-1`; the live post |
| MQTT.js in headless Chromium | 5.16.0, Chromium 141 | A page served from an allowed origin connects as MQTT 5, subscribes to `airc/room/lobby`, and publishes a `send` at QoS 1 | PUBACK; its own post and a native post delivered; history shows both |

paho-mqtt sends an `Origin` header naming the gateway's own address, which
the listener refuses unless it is in `allowed_origins`; the run lists it.
The gateway does not accept same-origin requests by default, since a page
on a rebound DNS name would pass that check.

## What the tests add

`integration/gateway/mqtt5_will_test.go` runs wills against the gateway
binary with an independent MQTT encoder and a native observer: a dropped
link posts the will, twice for two drops (each connection's will is its own
job); a normal DISCONNECT posts nothing and `0x04` posts it; a delayed will
is dropped by a session that resumes within the delay and posted after it
otherwise; and a will on another topic (`0x90`), a read (`0x99`), a retained
will (`0x9A`) or one without an author (`0x87`) is refused. It also carries
a session over WebSocket: a subscription delivery and a request.
