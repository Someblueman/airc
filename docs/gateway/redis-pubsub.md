# Redis Pub/Sub broker ingestion profile

This profile is implemented and enabled in the service. A producer publishes a
plain contract-v1 JSON request on a configured Redis channel; the gateway posts
it as the route's configured principal and publishes a plain JSON result on the
route's response channel. A 2026-10-10 real Redis 7.0.15 run with an independent
native observer is recorded [below](#real-redis-evidence-2026-10-10).

The gateway subscribes to an explicitly configured Redis broker. It does not
implement a Redis server. The selected wire profile is
[RESP2](https://redis.io/docs/latest/develop/reference/protocol-spec/), with exact
channel subscriptions rather than patterns, sharded Pub/Sub or cluster routing.

## Requests

Each publication on a route's request channel is one contract-v1 request: the
same JSON body an HTTP client sends ([contract-v1.md](contract-v1.md), parsed by
`core.Decode`). No envelope, signature, key or token is involved:

```sh
redis-cli --tls --cacert ca.pem --user producer --pass "$PRODUCER_PASSWORD" \
  PUBLISH airc.requests \
  '{"version":1,"request_id":"r1","operation":"send","target":{"kind":"room","name":"#probe"},"message":"hello"}'
```

A request must name the route's exact `target` and one of the route's
`operations`; a route permits a subset of `send`, `context.read`,
`history.read` and `search`. Unknown fields, malformed JSON, payloads above the
32,768-byte bulk bound, another target, an operation outside the route, and a
principal that is unknown or revoked in configuration are refused. A refused
publication is dropped without a correlated result and without journal intake;
it does not disconnect other publishers sharing the subscription.

## Trust boundary

Redis [Pub/Sub messages](https://redis.io/docs/latest/develop/pubsub/#format-of-pushed-messages)
carry a channel and payload but no publisher identity. Authority therefore comes
from the route, not from the payload: **anyone who can PUBLISH to a route's
request channel acts as that route's principal**, within the principal's
configured targets and operations and the route's narrower grant. Anyone who can
SUBSCRIBE to the response channel reads every result for that route, including
history and search pages.

Restrict both with Redis ACLs. Give producers `&<request channel>` and
`+publish` only, consumers `&<response channel>` and `+subscribe` only, and the
gateway's two ACL users exactly the channels they use (`+subscribe` on request
channels for the subscriber, `+publish` on response channels for the publisher).
Disable the `default` user. Use a separate route and principal for each producer
population that should be distinguishable in AIRC history. Broker credentials
authenticate the gateway to Redis; they are not AIRC credentials, and a route
principal needs no AIRC bearer token or signing key of its own.

Admission (`adapter.admitRedis`) checks the received literal channel against the
route, then calls `dispatch.AuthenticateRoute(route.Principal, payload, now)`.
That resolves the active configured principal, decodes the contract request and
applies the principal's existing target/operation authorization. The adapter
then requires the route's exact target and operation set. Only then does the
request compete for a shared bounded request slot (a full pool returns a
transient `capacity_exceeded` result) and run through the unchanged detached
dispatcher with an absolute three-second timeout bounded by the parent. Send
success requires durable journal intake. Durable revocation in the journal,
request-ID conflicts and native-read authority remain governed by the existing
dispatcher and store, and produce correlated error results.

Route requests have no validity window. A read is bounded by the caller's
context; a Send's idempotency comes from the journal deduplicating `request_id`
for the principal. Republishing the same request returns the original durable
result; changing the body under the same `request_id` returns `id_conflict`.

## Results

Results are plain JSON published on the route's response channel:

```json
{"version":1,"protocol":"redis/pubsub","request_id":"r1",
 "request_channel":"airc.requests","response_channel":"airc.responses",
 "target":{"kind":"room","name":"#probe"},
 "result":{"version":1,"operation":"send","result":{"request_id":"r1","durable":true}}}
```

(`result.result` is abbreviated here; it is the full detached result.) Exactly
one of `result` or `error` is present. Send results carry the durable journal
result for the same `request_id`; context, history and search results carry the
complete dispatcher representation, including opaque cursors, status and gap.
A client subscribes to the response channel before publishing and correlates by
`request_id`. If the whole result would exceed the 32,768-byte bulk bound, the
gateway publishes a transient `capacity_exceeded` error for that request instead;
pages and cursors are never sliced. Subscription and connection identifiers never
replace durable request identity, and a PUBLISH receiver count is not a receipt.

## Configuration

The optional top-level `redis_pubsub` array is separate from `listeners` and
accepts at most 32 brokers. Each entry has `id`, `enabled`, numeric-IP `address`,
required `tls_server_name`, optional absolute `tls_ca_file`, `subscriber` and
`publisher` credentials, and 1-128 `routes`. Each credential has an explicit ACL
`username` and absolute `password_file`; password contents are not configuration
values. Usernames are nonempty valid UTF-8, at most 255 bytes, without controls.
TLS names are DNS names or IP literals. Dial addresses reject unspecified IPs and
port zero. An omitted CA file selects system trust.

Each route has `principal`, `request_channel`, `response_channel`, exact `target`
and distinct explicit `operations`. Validation requires an active configured
principal whose binding, target and operation grants cover the route. History and
search also require the existing cursor key. No response signing key and no
principal credential is required; a principal referenced by a Redis route may
have neither a bearer token file nor signing keys. Both channel roles are disjoint
across all routes dialing the same canonical IP/port, including equivalent IPv6
and numeric-port spellings. Channels are bounded to 255 UTF-8 bytes without
controls; spaces, Unicode and glob characters are literal bytes under SUBSCRIBE.
Enabled entries need two shared socket slots each. Password and CA references
take part in the existing private-file and no-alias checks, so two brokers need
distinct CA file paths even when they trust the same CA.

`auth.ReadRedisPassword` loads 1-4,096 exact bytes from the referenced private
regular file, with the existing ownership, permission and symlink checks. No
whitespace, newline or encoding transformation occurs: a trailing newline is part
of the password. Errors disclose neither content nor path.

## Broker connection ownership

RESP2 subscription mode restricts commands, so each broker owner uses a dedicated
subscriber connection and a separately authenticated publisher connection, both
TLS 1.3 with verified server name and trust and their own ACL `AUTH`. The pair
reserves two shared socket slots before dialing and rolls back a partial
reservation. Dial, TLS, AUTH and subscription share one absolute three-second
setup deadline. Subscriptions are pipelined in batches of at most 64 literal
channels and confirmed by exact increasing counts; publications that arrive for
an already confirmed channel before all subscriptions are confirmed are discarded
(explicit pre-intake loss, never queued). Established subscriptions wait without
an idle timeout, then apply a three-second deadline to each started frame.
Results may use only configured response channels and require a nonnegative
integer PUBLISH reply.

One owner (`adapter.ServeRedisPubSub`) handles one publication and its result at
a time, without an application queue. Malformed RESP or channel frames and failed
result publication terminate the pair. Reconnection repeats both TLS/AUTH
exchanges and all subscriptions, with cancelable backoff from 100ms doubling to
five seconds; only a completed operation/result exchange resets it. Cancellation
closes the raw sockets, joins the current dispatch and callbacks, and releases
capacity. Broker error text and credentials never appear in output.

## Service integration checkpoint

Service startup prepares only enabled owners: private passwords, optional CA
roots (otherwise system trust) and a dispatcher restricted to the union of route
operations. No response signer is loaded for this mode. All preparation completes
before broker goroutines start; failure emits no ready record and releases the
journal. Readiness adds `brokers` entries with only ID and `redis/pubsub`; it
means local preparation completed, not that Redis is connected. Shutdown cancels
and joins broker owners before clearing password buffers and closing the journal.

## Framing

`internal/gateway/rediswire` handles bounded RESP2 simple strings, errors, 64-bit
integers, bulk strings and arrays, preserving null versus empty values. Bounds
are 32,768 bulk bytes, 4,096 line bytes, 128 array elements, four aggregate levels
and 65,536 encoded bytes per frame, checked before allocation. Unsupported RESP3
types, malformed CRLF, overflow and truncation are terminal for the connection.

## Delivery meaning

Redis Pub/Sub is [at most once](https://redis.io/docs/latest/develop/pubsub/#delivery-semantics).
Publications made while the gateway is disconnected or before journal intake are
lost and cannot be recovered by reconnection. Only a durable Send result
establishes journal intake. A producer that does not receive a result republishes
the same request with the same `request_id`; the journal coalesces it.

## Real Redis evidence (2026-10-10)

Redis 7.0.15 ran TLS-only on loopback with the `default` user disabled and
restricted ACL users: `gw-sub` (`&airc.requests +subscribe`), `gw-pub`
(`&airc.responses +publish`), `producer` (`&airc.requests +publish`), `consumer`
(`&airc.responses +subscribe`) and `outsider` (other channels only). A scratch
`aircd` ran on its own port and state directory, and `airc-gateway serve` used a
configuration with no response signing key and a principal with no bearer token
or signing key. With `redis-cli --tls` as `producer`:

- `PUBLISH airc.requests '{"version":1,"request_id":"pubsub-r1","operation":"send","target":{"kind":"room","name":"#probe"},"message":"hello from plain pubsub 世界"}'`
  produced a plain JSON durable Send result on `airc.responses`, and the
  independent observer `airc history '#probe'` printed
  `2026-10-10T23:14:09Z #probe probe-author: hello from plain pubsub 世界`.
- A `history.read` request returned a plain JSON page containing both messages
  posted during the run, with author, target and decoded body intact.
- A `search` request (not in the route's operations) and a non-JSON payload
  produced no result and no message. `PUBLISH` as `outsider` was refused by Redis
  with `NOPERM`.
- SIGTERM stopped the gateway promptly with empty stderr.

Scripted TLS peer tests (`internal/gateway/adapter/redis*_test.go`,
`internal/gateway/service/redis_test.go`) cover plain admission as the route
principal, journal attribution, replay and `id_conflict`, refusals for target,
operation, malformed and unknown-field bodies, configuration revocation and
journal revocation, plain JSON result shape and capacity fallback, lost-result
reconnect replay, backoff and joined shutdown. Earlier 2026-10-07 checkpoints in
[testing.md](testing.md#redis-pubsub-service-integration-checkpoint) exercised
the superseded signed-envelope model; their framing, connection and lifecycle
controls still describe the current code, but their authority and response-signing
statements no longer apply.
