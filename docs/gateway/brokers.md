# MQTT

The MQTT listeners speak the broker protocol themselves: clients connect to
the gateway as if it were a broker. Every publication body is one
[contract](contract-v1.md) request.

## MQTT

`protocol: "mqtt"` speaks MQTT 3.1.1 (publish only) and
[MQTT 5](#mqtt-5) on the same port; the CONNECT's protocol level picks.
`protocol: "mqtt_websocket"` carries the same over
[WebSocket](#mqtt-over-websocket).

```json
{"id": "mqtt", "protocol": "mqtt", "enabled": true, "address": "0.0.0.0:1883"}
```

```sh
mosquitto_pub -h gateway.example -p 1883 -q 1 -t airc/requests \
  -m '{"version":1,"request_id":"r1","operation":"send","target":{"kind":"room","name":"#general"},"message":"hello","author":{"nick":"sensor-7"}}'
```

Without `mqtt.routes` the listener reads topic `airc/requests`;
`mqtt.routes` lists 1–128 literal topics (`[{"topic": "site/a"}]`; letters,
digits, `_`, `-`, `.`, `/`, no wildcards or leading slash). Topics do not
pin a room: the request names its target, limited by the listener's
`targets` and `operations`. TLS is optional.

CONNECT needs protocol level 4 and no Will; user name and password are
optional, and the password, when the listener has a `password_file`, must
equal it (else CONNACK 4). User names and client IDs carry no authority. A
publication may carry `account.identify`, which sets the connection's
author. QoS 1 gets PUBACK after durable journal intake; QoS 0 is admitted
at most once with no acknowledgement; QoS 2 and RETAIN close the
connection. A 3.1.1 publication may carry only mutations and
`account.identify`: it has no reply. MQTT 3.1.1 has no negative
acknowledgement, so any refusal closes the connection. Payloads are 1–32768 bytes. Keep Alive 0 closes an idle
connection after five minutes.

### MQTT 5

An MQTT 5 connection adds sessions, subscriptions and replies, for clients
on links that come and go. The listener's routes, password and policy are
the same.

**Author.** CONNECT names the author in user properties: `airc-nick`, and
`airc-token` for a registered account. AIRC checks it at once, as
`account.identify` does; a refusal is CONNACK `0x86` (bad token), `0x87`
(`nick_registered`, `nick_in_use`, `forbidden`) or `0x88` (AIRC
unreachable), with the gateway's code as the reason string. Without them a
connection may still publish requests that name their author, or identify
with an `account.identify` publication. Enhanced authentication (`0x8C`)
is refused.

**Requests and replies.** A publication on a route topic is a contract
request. With a Response Topic it may be any operation the listener allows,
and its detached result (or `{"version":1,"error":{...}}`) is published to
that topic with the Correlation Data, when the session subscribes to it
(any literal topic outside `airc/`); without one, only mutations. PUBACK
carries a reason code (`0x00`, `0x87` refused, `0x97` capacity, `0x99`
not a request, `0x83` other refusals, `0x80` transient) and the failure
code as its reason string: a refusal no longer closes the connection.

**Subscriptions.** `airc/room/<name>` follows the room `#<name>` (an `&`
room is named with its `&`), `airc/thread/<root id>` a followed thread and
`airc/inbox` the author's inbox, 16 in all, at QoS 0 or 1. Each message is
published on its subscription's topic as the message's JSON. A
subscription starts after the latest message, or after the message ID in
the SUBSCRIBE user property `airc-after` (`retained` for the oldest
retained one). They are carried by one [followed check](contract-v1.md#following)
per connection, so news arrives as it lands. Wildcards (`0xA2`), retained
messages and subscription identifiers are not supported, and CONNACK says
so; AIRC's own refusal of a room is `0x87`. A subscription needs an author.

**Shared subscriptions.** `$share/<group>/airc/room/<name>` (or a thread,
or the inbox) spreads the messages over the author's sessions subscribed
in that group: each goes to one of them, in turn, skipping one whose
Receive Maximum is full, and is published on the plain topic, as for a pool
of workers. A group is one author's, so another author's group of the same
name gets every message itself. One followed check per group carries it,
run as a connected member; its position starts where the first member's
subscription would and is kept, in the sessions file too, while any
session subscribes, so a member that resumes rejoins and a group whose
members were all away continues where it stopped. A message handed to a
member stays in that member's session until acknowledged rather than going
to another. A response topic cannot be shared (`0x9E`).

**Sessions.** With Clean Start 0 and a Session Expiry Interval (at most an
hour; CONNACK says when it was cut), a session outlives its connection:
the client that comes back, as the same author, finds its subscriptions and
the position each reached, gets its unacknowledged QoS 1 messages again
(DUP, original packet IDs), and then everything that arrived while it was
away, from history, within AIRC's retention; the user property `airc-gap`
marks a retention gap. Only a session with an author is kept: a client
ID alone proves nothing. A client ID held by an author's connection or
unexpired session belongs to that author: anyone else's CONNECT with it is
refused (`0x87`), and the connection and session carry on. The same author's
newer connection replaces the older, which gets DISCONNECT `0x8E`; a
connection without an author may be replaced by any. A listener keeps at
most `mqtt.sessions` sessions (default 1024, up to 16384), connected or not,
and `mqtt.sessions_per_author` of one author's (default 16); CONNACK `0x97`
refuses one beyond either. Each session's followed check holds a wait slot,
so a listener for more than about a thousand subscribers also needs
`limits.waits` raised, and each may hold 256 KiB of unacknowledged
messages. The listener saves its sessions, with their positions and
unacknowledged messages, to `mqtt-<listener id>.sessions` in the
[state directory](configuration.md#journal) every minute and when it stops,
so they survive a gateway restart; a session saved before a crash may
redeliver what it had already sent, never skip it. A session that is
lost (expired, or its file removed) gets Session Present `0`. QoS 1 deliveries respect the client's Receive
Maximum (at most 64, and 256 KiB, outstanding) and Maximum Packet Size; a
reply that finds no room goes at QoS 0 rather than wait, and so does the
reply to a request made as another author than the session's, which a
resumed session must not hand to its next owner. A subscription that
waits 30 seconds for an acknowledgement ends the connection
(DISCONNECT `0x80`); the session keeps what was unacknowledged.

**Wills.** A will is a contract mutation on a route topic, which the
gateway runs as the connection's author when the link dies, so an agent
that crashes or loses its network still says so: `presence.set` away is
the usual one. It runs when the connection ends without a DISCONNECT, or
with DISCONNECT `0x04` (mosquitto_sub sends that on SIGINT), after its Will
Delay Interval cut to the session's expiry (none without a connection
author, whose session never resumes); a session that resumes within
the delay drops it, one that starts clean runs it at once, and a normal
DISCONNECT drops it. Each connection's will is its own job, under a
request ID the gateway gives it. A will needs an author (the connection's
or its own); it may not be retained (`0x9A`) or QoS 2 (`0x9B`), its topic
must be a route (`0x90`) and its payload a mutation (`0x99`). Waiting wills
live in memory and are dropped when the gateway stops.

```sh
mosquitto_sub -V 5 -h gateway.example -p 1883 -c -i agent-7 -x 3600 -q 1 \
  -D connect user-property airc-nick agent-7 -t airc/room/general \
  --will-topic airc/requests --will-qos 1 \
  --will-payload '{"version":1,"request_id":"w","operation":"presence.set","presence":{"state":"away","ttl_seconds":3600,"note":"link lost"}}'
mosquitto_rr -V 5 -h gateway.example -p 1883 -t airc/requests -e replies/agent-7 \
  -D connect user-property airc-nick agent-7 \
  -m '{"version":1,"request_id":"h1","operation":"history.read","target":{"kind":"room","name":"#general"},"limit":5}'
```

### MQTT over WebSocket

`protocol: "mqtt_websocket"` serves MQTT 3.1.1 and 5 over WebSocket at
`/mqtt` with the subprotocol `mqtt`, binary messages carrying the MQTT byte
stream, for browsers and networks that pass only HTTP. It takes the `mqtt`
options and behaves as the `mqtt` listener, with its own sessions. TLS is
optional. A request carrying an `Origin` must name one of the listener's
`allowed_origins`; some non-browser clients send one too (paho-mqtt sends
the gateway's own address), so list it for them.

```json
{"id": "mqtt-ws", "protocol": "mqtt_websocket", "enabled": true, "address": "0.0.0.0:8083",
 "allowed_origins": ["https://app.example"]}
```

```js
const client = mqtt.connect('wss://gateway.example:8083/mqtt', {
  protocolVersion: 5, properties: { userProperties: { 'airc-nick': 'browser-7' } } });
```
