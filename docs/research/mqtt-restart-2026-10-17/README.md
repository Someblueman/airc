# MQTT sessions across a gateway restart

Run 17 October 2026 on the branch that keeps MQTT 5 sessions in the state
directory, on a 4-vCPU Intel Xeon 2.10 GHz with Go 1.27.0, Linux AMD64. It
closes the gap the [MQTT 5 note](../mqtt5-2026-10-17/README.md) left: a
gateway restart, such as an upgrade, lost every session.

[run.sh.txt](run.sh.txt) starts a scratch `aircd` and a gateway with one
`mqtt` listener. mosquitto_sub 2.0.18, as the account `carol`, opens a
persistent session (`-c -i durable-carol -x 600`) on `airc/room/lobby`,
receives a post and exits; the gateway is stopped with SIGTERM, two posts
are made with plain IRC while it is down, and the gateway is started again
on the same state directory. The same mosquitto_sub command then receives
both posts. [transcript.txt](transcript.txt) is the output, the account ID
redacted; the state directory listing shows the owner-only
`mqtt-mqtt.sessions` beside the journal.

`integration/gateway/mqtt5_restart_test.go` does the same over TCP and
WebSocket with an independent client and checks more than mosquitto shows:
Session Present after the restart, and the message left unacknowledged
before it redelivered with DUP and its original packet ID before the missed
ones. `TestMQTT5SnapshotRoundTripAndRefusals` covers the file itself:
expired and authorless sessions are not restored, and a shared, linked,
unversioned or malformed file is refused.
