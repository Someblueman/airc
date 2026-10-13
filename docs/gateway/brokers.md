# Brokers and databases

MQTT, AMQP and STOMP listeners speak the broker protocol themselves: clients
connect to the gateway as if it were a broker. MQTT-SN takes connectionless
UDP publications. Redis and PostgreSQL are the other way round: the gateway
connects to your server as a client and consumes a channel or stream. Every
message body is one [contract](contract-v1.md) request.

## MQTT

`protocol: "mqtt"` speaks MQTT 3.1.1 (publish only; it is not a
subscription broker and returns nothing over MQTT).

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
`targets`, and `operations` may list only mutations. TLS is optional.

CONNECT needs protocol level 4 and no Will; user name and password are
optional, and the password, when the listener has a `password_file`, must
equal it (else CONNACK 4). User names and client IDs carry no authority. A
publication may carry `account.identify`, which sets the connection's
author. QoS 1 gets PUBACK after durable journal intake; QoS 0 is admitted
at most once with no acknowledgement; QoS 2 and RETAIN close the
connection. MQTT has no negative acknowledgement, so any refusal closes the
connection. Payloads are 1–32768 bytes. Keep Alive 0 closes an idle
connection after five minutes.

## MQTT-SN

`protocol: "mqtt_sn"` (UDP) takes MQTT-SN 1.2 connectionless QoS −1
PUBLISH with predefined topic IDs: no CONNECT, registration or subscription.
`operations` must be `["send"]` and `mqtt_sn.routes` maps 1–128 topic IDs to
targets:

```json
"mqtt_sn": {"routes": [{"topic_id": 7, "target": {"kind": "room", "name": "#sensors"}}]}
```

Each payload (1–32768 bytes) is a `send` request whose target must equal
its topic's. Success sends nothing back; a well-formed send on an unknown
topic gets an invalid-topic PUBACK. UDP can lose a publication before
intake, so producers retry with the same `request_id`.

## AMQP

`protocol: "amqp"` speaks AMQP 1.0 as a fixed-link server, plain or over TLS
1.3. The client sends its protocol header first. Without a listener
password it may skip SASL or use ANONYMOUS; SASL PLAIN's password is the
listener password, and its user names are ignored.

Without `amqp.routes` the listener reads link address `airc.requests` and
answers on `airc.responses`. Routes name other pairs and may narrow
operations to `send`, `context.read`, `history.read` and `search`:

```json
"amqp": {"routes": [{"request_address": "airc/request", "response_address": "airc/response",
                     "operations": ["send", "history.read"]}]}
```

A message body is one or more data sections or one `amqp-value` string or
binary holding the request; `to` and `reply-to`, if present, must be the
route's addresses. Routes do not pin a room. Each transfer is settled with a
disposition: Accepted after durable intake, Rejected
(`amqp:precondition-failed`, the failure code as description) or Released
for a transient failure. The response link is optional; with one, a JSON
result `{"version":1,"protocol":"amqp/1","request_id","request_address","response_address","result"|"error"}`
is held until the client settles it. Pre-settled transfers are admitted at
most once. There are no dynamic nodes, transactions or link recovery.

## STOMP

`protocol: "stomp"` speaks STOMP 1.2, plain or over TLS 1.3. CONNECT (or
STOMP) must offer 1.2; any `host` is accepted and `login` ignored;
`passcode` is the listener password. A refused CONNECT gets an ERROR frame.

Without `stomp.routes` it takes SEND to `airc.requests` and answers on
`airc.responses`; routes (`request_destination`, `response_destination`,
optional `operations` from `send`, `context.read`, `history.read`,
`search`) pick others. A SEND body is the request; `reply-to`, if present,
must be the route's response destination, and `transaction` is refused. A
SUBSCRIBE to the response destination (`ack:client-individual`) receives one
MESSAGE with the JSON result per request; without one, a SEND gets its
RECEIPT after durable intake. A refused request gets an ERROR whose body is
the JSON failure.

## Redis

The gateway dials Redis (RESP2) over verified TLS with ACL users; there is
no plaintext mode. Each route still names a fixed `target` that requests
must use, plus `operations` (from `send`, `context.read`, `history.read`,
`search`) and its author policy (`bound_author`, `guests`; see
[route authors](authentication.md#bound-authors)). AIRC sees `route:<id>`
as the source.

**Pub/Sub** (top-level `redis_pubsub`, up to 32 owners, 1–128 routes each):

```json
"redis_pubsub": [{"id": "ops", "enabled": true, "address": "192.0.2.5:6379",
  "tls_server_name": "redis.example", "tls_ca_file": "/abs/redis-ca.pem",
  "subscriber": {"username": "airc_sub", "password_file": "/abs/sub.pass"},
  "publisher": {"username": "airc_pub", "password_file": "/abs/pub.pass"},
  "routes": [{"request_channel": "airc.requests", "response_channel": "airc.responses",
              "target": {"kind": "room", "name": "#ops"}, "operations": ["send"],
              "bound_author": {"nick": "ops-bot", "account_token_file": "/abs/ops.token"}}]}]
```

A PUBLISH on the request channel is admitted; the result is published on the
response channel as
`{"version":1,"protocol":"redis/pubsub","request_id","request_channel","response_channel","target","result"|"error"}`.
A publication that is not a request (or exceeds 32768 bytes) is dropped
silently; a wrong target or operation gets a `forbidden` result. Pub/Sub
loses messages published while the gateway is not subscribed.

**Streams** (top-level `redis_streams`): one route per owner, read through a
pre-created consumer group:

```json
"redis_streams": [{"id": "jobs", "enabled": true, "namespace": "jobs", "address": "192.0.2.5:6379",
  "tls_server_name": "redis.example", "credential": {"username": "airc", "password_file": "/abs/airc.pass"},
  "group": "airc", "consumer": "gateway-1",
  "route": {"stream": "airc:requests", "result_stream": "airc:results",
            "target": {"kind": "room", "name": "#ops"}, "operations": ["send"], "guests": true}}]
```

Each entry has one field, `intent`, holding the request. The gateway admits
it, appends a result with `XADD <result_stream> MAXLEN = 1024`
(`{"version":1,"protocol":"redis/streams","request_id","namespace","stream","result_stream","entry_id","target","result"|"error"}`)
and then acknowledges the entry. A redelivered or re-added request returns
the original result. A transient failure is reported and the entry stays
pending for retry. A result too large for the result stream fails
permanently (`capacity_exceeded`, not transient) and is acknowledged, since a
redelivery would produce the same result; an entry that is not a request (or exceeds 32768 bytes)
stays pending without a result until an operator removes it. A lost XADD
reply can duplicate a result (deduplicate by `entry_id` and `request_id`).

Redis carries no publisher identity: **anyone allowed to PUBLISH or XADD to
the request channel can post as its bound author**, and anyone who can read
the response channel sees every result. Restrict both with ACLs and disable
the `default` user.

## PostgreSQL NOTIFY

Top-level `postgres_notify` (up to 16 owners) holds a `LISTEN` over verified
TLS (1.2 or newer) with certificate authentication; there is no password or
plaintext fallback. The client key is Ed25519 and the certificate's Common
Name must equal `username`; database, username and channel match
`[a-z_][a-z0-9_]{0,62}`.

```json
"postgres_notify": [{"id": "db", "enabled": true, "address": "192.0.2.10:5432",
  "tls_server_name": "postgres.example", "tls_ca_file": "/abs/pg-ca.pem",
  "client_cert_file": "/abs/pg-client.pem", "client_key_file": "/abs/pg-client.key",
  "database": "airc_requests", "username": "airc_listener", "channel": "airc_requests",
  "route": {"target": {"kind": "room", "name": "#general"},
            "bound_author": {"nick": "db-alerts", "account_token_file": "/abs/alerts.token"}}}]
```

Each payload is a `send` request for the route's target, posted as its
author or the bound author. It is send-only and ephemeral: notifications
sent while the gateway is disconnected are lost. **Any role that can connect
to the database can NOTIFY any channel**, and so post as the bound author;
use a dedicated database with `CONNECT` granted only to producers.
