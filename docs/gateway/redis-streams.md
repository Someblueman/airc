# Redis Streams ingestion

Redis Streams ingestion is enabled through service-owned TLS connections. A
producer appends a plain contract-v1 JSON request to a configured stream; the
gateway reads it through a preprovisioned consumer group, posts it as the author
it names, else as the route's bound author ([route authors](route-authors.md)),
appends a plain JSON result to the result stream and then
acknowledges the entry. A 2026-10-10 real Redis 7.0.15 run with an independent
native observer is recorded [below](#real-redis-evidence-2026-10-10).

## Requests

Each entry carries exactly one field, `intent`, whose value is one contract-v1
request: the same bytes as every carrier ([contract-v1.md](contract-v1.md)),
naming its author or relying on the route's `bound_author`. Redis may choose the
entry ID:

```sh
redis-cli --tls --cacert ca.pem --user producer --pass "$PRODUCER_PASSWORD" \
  XADD airc:requests '*' intent \
  '{"version":1,"request_id":"r1","operation":"send","target":{"kind":"room","name":"#probe"},"message":"hello","author":{"nick":"build-bot"}}'
```

The entry ID is broker bookkeeping only. The request's own `request_id` is its
journal identity, per author: re-adding the same request under a new ID, or
redelivering a pending entry, returns the original durable result rather than
posting twice. Changing the body under the same `request_id` yields
`id_conflict`.

A request must name the route's exact `target` and one of the route's
`operations`, a subset of `send`, `context.read`, `history.read` and `search`;
otherwise it gets a correlated `forbidden` result. An entry that is not a
contract request (malformed JSON, unknown fields, above the 32,768-byte bulk
bound) or is deleted is refused without a result.

## Trust boundary

Stream entries carry no producer identity, so the Redis ACL on the request
stream is the route's access gate: **anyone who can XADD to it can post as its
bound author**, or name any guest when guests are allowed. Anyone who can read
the result stream reads every result for that route, including history and
search pages.

Restrict both with Redis ACLs. Give producers `~<request stream>` with `+xadd`
only, consumers `~<result stream>` with `+xread`/`+xrange` only, and the gateway's
ACL user the two keys with `+xreadgroup +xadd +xack`. Create the consumer group
with an administrative user, and disable the `default` user. Broker credentials
authenticate the gateway to Redis; they are not AIRC credentials.

Admission rejects deleted entries, malformed IDs and stream/result-route
mismatches, decodes the request, requires the route's target and operations,
competes for a shared bounded request slot (a full pool yields a transient
`capacity_exceeded`) and runs as its author with an absolute three-second
timeout. Send success requires durable journal intake.

## Results

Results are appended with `XADD <result_stream> MAXLEN = 1024 * result <json>`:

```json
{"version":1,"protocol":"redis/streams","request_id":"r1","namespace":"probe",
 "stream":"airc:requests","result_stream":"airc:results","entry_id":"1791674031535-0",
 "target":{"kind":"room","name":"#probe"},
 "result":{"version":1,"operation":"send","result":{"request_id":"r1","durable":true}}}
```

(`result.result` is abbreviated here; it is the full detached result.) Exactly
one of `result` or `error` is present; `entry_id` lets a producer that used an
auto-generated ID correlate the result with the ID `XADD` returned. Send results
carry the durable journal result; context, history and search results carry the
complete dispatcher representation, including opaque cursors, status and gap. If
the whole result would exceed 32,768 bytes the gateway appends a transient
`capacity_exceeded` error instead and never slices the page. Exact trimming keeps
at most 1,024 results; consumers must read promptly. A lost XADD reply can append
a duplicate result, which clients deduplicate by `entry_id` and `request_id`.

## Acknowledgement and pending entries

The owner appends the whole result before issuing `XACK`, and acknowledges an
entry only when the outcome is final:

- a whole result (durable Send, or a complete read) is acknowledged;
- a permanent failure (for example `id_conflict`, or `forbidden` for a request
  outside the route) is published and acknowledged, because a plain request does not
  expire and would otherwise be retried and reported indefinitely;
- a transient failure (capacity, upstream unavailable, read timeout) is
  published and left pending for retry;
- an entry that is not a contract request (malformed or deleted) publishes
  nothing and stays pending.

Removing refused entries from the pending list is an operator action. One pending
read alternates with one new read: the pending cursor advances past refused
entries, so they cannot starve new intake, and an empty pending scan resets it to
`0-0`. Every reconnect rescans this consumer's pending history from `0-0`. A
redelivered pending entry is simply dispatched again; the journal deduplicates its
`request_id`. `XACK` accepts a reply of 0 or 1; zero covers an entry already removed
after a lost reply. Neither the XADD reply nor XACK establishes native AIRC
acceptance.

## Configuration

The optional `redis_streams` array configures at most 32 outbound owners. Each has
`id`, `enabled`, `namespace`, numeric-IP `address`, verified `tls_server_name`,
optional absolute `tls_ca_file`, one `credential` (`username`, private absolute
`password_file`), fixed `group` and stable `consumer`, and one `route`. The route
contains a literal `stream`, distinct `result_stream`, exact `target`, explicit
operations and its author policy (`bound_author`, `guests`). History and search
require the cursor key. No principal or response signing key is required.
Password and CA references take part in the existing private-file and no-alias
checks, so a Streams owner and a Pub/Sub broker need distinct CA file paths.

`namespace` names one broker keyspace independent of its dial address and is
reported in results. Within a configuration, canonical IP/port equivalence permits
only one namespace per endpoint and one endpoint per namespace, and request/result
stream keys cannot overlap any other stream role in that namespace. Names keep
exact UTF-8 bytes, are nonempty, at most 255 bytes and free of controls. Groups are
preprovisioned; one stable consumer owns each configured request stream, and the
gateway never creates groups or claims another consumer's entries. One socket per
Streams owner and two per Pub/Sub broker count against the shared session limit.

## Connection ownership

`openRedisStream` owns one serial RESP2 TLS 1.3 connection with verified server
name and trust, reserving one shared socket slot before dialing. Dial, handshake
and `AUTH` share one absolute three-second deadline. New reads use `XREADGROUP
GROUP group consumer COUNT 1 BLOCK 1000 STREAMS stream >`; pending scans use the
same consumer and `COUNT 1` with `0-0` or the last entry ID, without blocking. Each
command has a three-second deadline. `rediswire.ReadStreamEntry` accepts one
stream and at most one entry with exactly one nonempty `intent` field; deleted
pending entries keep their ID with null fields. Malformed or error replies close
the connection; there is no resynchronization and broker error text is never
returned.

`adapter.ServeRedisStreams` snapshots route operations, TLS configuration and the
password, clears its password copy after joining, and reconnects with cancelable
backoff from 100ms doubling to five seconds; a completed operation resets it.
Each iteration holds one bounded entry and result; there is no retry queue.

## Service integration

Enabled owners prepare password, trust and a route dispatcher before readiness,
without dialing; no response signer is loaded for this mode. Readiness includes
the broker ID and `redis/streams`, meaning local preparation completed, not Redis
connectivity. Invalid password or trust content prevents readiness and releases
the journal. Shutdown cancels and joins owners before clearing passwords and
closing the journal.

## Author-model evidence (2026-10-11)

Against Redis 7.0.15 (TLS only, the ACL users above) with a scratch `aircd` and an
independent `airc history` observer, `redis-cli --tls` as `producer` added an
`intent` naming the guest `stream-guest` and one naming a registered account
with its token; both posted under those authors, and `airc:results` received a
plain `queued` result with each `entry_id` and no entry stayed pending.
Adapter and service tests cover guest and bound-author routes, correlated
`forbidden` results, dedupe, ordering and loss recovery.

## Real Redis evidence (2026-10-10)

This run predates the author model: it posted as a route principal, and an
out-of-route entry then stayed pending without a result.

Redis 7.0.15 ran TLS-only on loopback with the `default` user disabled. ACL users:
`gw-streams` (`~airc:requests ~airc:results +xreadgroup +xadd +xack`), `producer`
(`~airc:requests +xadd`), `consumer` (`~airc:results +xread +xrange`), `outsider`
(other keys only) and an administrative user that ran
`XGROUP CREATE airc:requests gateway $ MKSTREAM`. A scratch `aircd` ran on its own
port and state directory, and `airc-gateway serve` used a configuration with no
response signing key and a principal with no bearer token or signing key. With
`redis-cli --tls` as `producer`:

- `XADD airc:requests '*' intent '{"version":1,"request_id":"streams-r1","operation":"send","target":{"kind":"room","name":"#probe"},"message":"hello from plain streams 世界"}'`
  returned auto ID `1791674031535-0`; `airc:results` received a plain JSON
  durable Send result with that `entry_id`, and the independent observer
  `airc history '#probe'` printed
  `2026-10-10T23:13:51Z #probe probe-author: hello from plain streams 世界`.
- An auto-ID `history.read` entry returned a plain JSON page with author
  `probe-author`, target `#probe` and the exact body.
- An entry targeting `#elsewhere` produced no result and no message and stayed
  pending (`XPENDING` showed exactly that entry). `XADD` as `outsider` was refused
  by Redis with `NOPERM`.
- Re-adding the identical `streams-r1` request under a new auto ID returned the
  durable result again; the observer still showed one copy of the message.
- SIGTERM stopped the gateway promptly with empty stderr.

Scripted TLS peer tests (`internal/gateway/adapter/redis_stream*_test.go`,
`internal/gateway/service/redis_stream*_test.go`) cover auto-ID admission (at the
time as the route principal), journal attribution, dedupe across re-added entries, consumer
changes and journal reopen, refusals, configuration and journal revocation, plain
JSON results, capacity fallback, publish-before-ack ordering, permanent-failure
acknowledgement, entry/publication/ack-reply loss with reconnect, pending fairness
and joined shutdown. Earlier 2026-10-07 checkpoints in [testing.md](testing.md)
exercised the superseded signed-envelope model, including entry-ID-bound request
IDs and expired retained-Send recovery, both since removed; their framing,
connection and lifecycle controls still describe the current code.
