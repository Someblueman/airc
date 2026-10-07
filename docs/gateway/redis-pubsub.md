# Redis Pub/Sub broker ingestion profile

This is the selected implementation contract. Implementation and native
qualification remain in [ROADMAP.md](ROADMAP.md).

The gateway subscribes to an explicitly configured Redis broker. It does not
implement a Redis server. The selected wire profile is
[RESP2](https://redis.io/docs/latest/develop/reference/protocol-spec/), with exact
channel subscriptions rather than patterns, sharded Pub/Sub or cluster routing.

## Publisher authority and return path

Redis [Pub/Sub messages](https://redis.io/docs/latest/develop/pubsub/#format-of-pushed-messages)
contain a channel and payload, without authenticated publisher identity. Broker
credentials authenticate the gateway to Redis; they do not authorize an AIRC
sender. Each publication must contain the existing compact signed JWS request
envelope, establishing an active principal, binding, target, operation, request
ID, audience and validity interval. A fixed route requires that verified principal
and target and an explicit subset of Send, ContextRead, HistoryRead and Search.
Channels and subscription counts confer no publisher authority. A broker or
another Redis client cannot turn an unsigned payload into an AIRC request.

Each route specifies distinct literal request and response channels. Grants must
prevent collisions across both channel roles. Channels are bounded to 255 UTF-8
bytes, with nonempty names and no control characters; the gateway never requests
pattern subscriptions. Bounded context/history/search results use the response
channel and retain their entire dispatcher representation, including opaque
cursors, status and gap. A publisher subscribes to that channel before submitting
its request and checks the signed result's request ID and exact route scope.

Results use the existing Ed25519 response signer and envelope. The inner result
binds version, protocol `redis/pubsub`, verified request ID, ordered request and
response channels and configured target, with exactly one result or failure.
The response route scope will hash those ordered fields under a Redis-specific
domain and retain realm, principal and binding in the existing envelope scope.
Subscription and connection identifiers never replace durable request identity.
Oversized results become bounded signed capacity failures; pages are not sliced.

## Broker connection ownership

The configuration must express outbound broker connections explicitly rather
than treat the broker address as an inbound listening address. Broker credentials
stay in private files, with strict schema and existing private-file checks. The
profile requires authenticated TLS with verified server name and CA trust; no
cleartext credential transport or certificate-verification bypass is selected.
No broker credentials or raw broker error text appear in ready/error output.

RESP2 subscription mode restricts commands. Each broker owner therefore needs a
dedicated subscription connection and a separately authenticated result-publisher
connection. Both are bounded by existing global socket capacity, acquired without
leaking a partial pair. AUTH, subscription establishment and each started frame,
dispatch and result publication have absolute deadlines. Idle subscription waits
must be cancelable without treating quiet channels as delivery failures.

One owner handles one publication and its result at a time, without an unbounded
application queue. Unexpected channel/type/count replies terminate the affected
connection pair. Reconnection must authenticate and reestablish the exact fixed
subscriptions before intake, with bounded backoff and joined cancellation. Any
return publication count is protocol metadata, not an application receipt.

## Framing prerequisite

The codec handles bounded RESP2 simple strings, simple errors, signed 64-bit
integers, bulk strings and arrays, preserving type distinctions. Commands encode
as arrays of bulk strings; no inline-command parser is needed. Bulk strings are
binary-safe, including zero length, CRLF, NUL and Unicode bytes. Null bulk strings
and null arrays are represented distinctly from empty values so the owner can
refuse them where a concrete field is required.

Profile bounds are 32,768 bulk bytes, 4,096 simple/header-line bytes, 128 array
elements, four aggregate nesting levels and 65,536 total encoded bytes per frame.
The decoder checks declared lengths, aggregate count/depth and total-frame budget
before allocating or consuming declared bodies. Unsupported RESP3 types,
malformed CRLF, numeric overflow and truncation are terminal framing failures.
Buffered bytes after a complete frame remain available for the next frame.
Encoders enforce the same bounds. The owner adds command-specific channel,
subscription count, credential and signed payload checks.

## Delivery meaning and qualification

Redis Pub/Sub is [at most once](https://redis.io/docs/latest/develop/pubsub/#delivery-semantics).
Offline subscribers and failures before journal intake can lose publications.
Reconnection cannot recover those messages. PUBLISH's subscriber count and
subscription confirmations do not establish durable gateway intake or native
AIRC acceptance. A signed durable result establishes the actual journal outcome;
lost results remain uncertain and replay uses the same signed request ID and
intent. Existing journal replay, revocation and delivery rules remain authoritative.

Qualification requires an actual Redis server and client plus independent native
AIRC observation of author, target and exact body. It must exercise deliberate
offline/pre-intake loss, lost-result/restart replay, complete bounded reads,
signature/route/freshness refusals, framing/resource bounds and joined shutdown.
Codec tests and simulated Redis peers do not qualify the mode. No Redis executable
was found on the local PATH and existing cached images are only Debian Bookworm
Slim and Ubuntu 24.04 on 2026-10-07. The owner has been asked to authorize a pinned
isolated fixture or provide an existing one; that qualification remains unfinished.
