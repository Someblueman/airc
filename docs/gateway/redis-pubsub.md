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

The independent admission prerequisite, `adapter.admitRedis`, verifies the
existing compact JWS through the dispatcher before resolving a response scope.
It matches the received literal channel, signed principal, exact signed target
and one of the four explicit route operations. No broker username or transport
identity enters that authority check. The binding for later signed results comes
from verified signed identity. Signing-only principals need no AIRC bearer token.
An invalid signature, freshness or route match yields no correlated request ID,
binding or result. Only after authority is established can capacity/execution
failures retain the verified request ID and binding.

Admission claims an existing bounded request slot without waiting, then calls the
unchanged detached dispatcher with an absolute three-second timeout bounded by
the parent. Send success requires journal durability. Stable request identity,
conflicting intent, signed validity, durable revocation and native-read authority
remain governed by the existing dispatcher/store. A changed channel does not mint
a new request identity, and an authorized refusal is not durable intake.

On 2026-10-07, `go test -race` with the `TestRedis|TestSigned` selection passed for
adapter (11.000s), auth (8.679s) and config (6.143s); affected vet, staticcheck,
formatting and diff checks passed. Controls cover replay across journal reopen
and changed channels with unchanged upstream identity, permanent ID conflicts,
configured and durable revocation, signature/principal/target/operation/channel
refusals, expiry/future validity, capacity and canceled-send refusal without
journal writes, signing-only principals, and parent cancellation of each supported
read through an actual native Unix socket. Socket cancellation retains no journal
mutation. This checkpoint establishes admission only; socket dispatch integration
and native Redis qualification are not claimed by it.

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
The response route scope hashes those ordered fields under a Redis-specific
domain and retains realm, principal and binding in the existing envelope scope.
Subscription and connection identifiers never replace durable request identity.
Oversized results become bounded signed capacity failures; pages are not sliced.

The independent signed-result prerequisite is implemented as `RedisResult` and
`redisResultMessage`. The route scope is the lowercase hexadecimal SHA-256 digest
of these exact concatenated bytes:

```text
redis-pubsub-response NUL request_channel NUL response_channel NUL target.kind NUL target.name
```

Here `NUL` is one zero byte and names retain their exact UTF-8 bytes. There is no
trailing separator. Realm, verified principal and binding remain separate fields
of the existing `auth.ResponseScope`. The fixed route vector `airc.request`,
`airc.response`, `room`, `#fixture` is
`e9f8f2683ac7db3ef268c575a6b18dbf81a6ead265e62a8d28015411c9d2dad3`.
Channel validation excludes NUL, so ordered channel fields cannot collide through
separator ambiguity. The domain is distinct from NATS, STOMP and AMQP response
scopes.

The inner version-1 `redis/pubsub` result retains the verified request ID, exact
ordered channel names, target and exactly one full detached result or failure.
Send result shaping requires durable intake and the matching request ID;
context/history/search retain their complete dispatcher result. Signing uses the
existing response key, validity interval and envelope. The entire signed token
must fit the 32,768-byte Redis bulk bound. Oversize returns no payload and
`auth.ErrResponseCapacity`, allowing the socket loop to publish a correlated
signed capacity failure without slicing the original page or cursor.

On 2026-10-07, the final `TestRedis|TestSigned|TestResponse` race selection passed
for adapter (9.930s) and auth (4.376s); affected vet, staticcheck, formatting and
diff checks passed. Controls cover the fixed scope vector, literal Unicode/glob
channels, channel order, target, realm/principal/binding, cross-protocol and
expiry refusals, result/error and durable-send shape, exact whole context/pages,
opaque cursors/status/gap and oversized-page refusal with an immutable original
and bounded signed failure. This is result representation evidence; service
socket dispatch and real Redis/native AIRC qualification remain outstanding.

## Broker connection ownership

The configuration must express outbound broker connections explicitly rather
than treat the broker address as an inbound listening address. Broker credentials
stay in private files, with strict schema and existing private-file checks. The
profile requires authenticated TLS with verified server name and CA trust; no
cleartext credential transport or certificate-verification bypass is selected.
No broker credentials or raw broker error text appear in ready/error output.

The optional top-level `redis_pubsub` array is separate from `listeners` and
accepts at most 32 brokers. Each entry has `id`, `enabled`, numeric-IP `address`,
required `tls_server_name`, optional absolute `tls_ca_file`, `subscriber` and
`publisher` credentials, and 1-128 `routes`. Each credential has an explicit ACL
`username` and absolute `password_file`; password contents are not configuration
values. Usernames are nonempty valid UTF-8, at most 255 bytes, without controls.
TLS names are DNS names or IP literals. Dial addresses reject unspecified IPs and
port zero. An omitted CA file selects system trust during service integration.

Each route has `principal`, `request_channel`, `response_channel`, exact `target`
and distinct explicit `operations`. Validation requires an active signing
principal whose existing binding, target and operation grants authorize the route;
an AIRC bearer token is not required for this signed-message mode. History and
search require the existing cursor key, and every broker requires the existing
response signer. Both channel roles are disjoint across all routes dialing the
same canonical IP/port, including equivalent IPv6 and numeric-port spellings.
Spaces, Unicode and glob characters are literal channel bytes under SUBSCRIBE.

This schema prerequisite is implemented, with strict unknown/duplicate/null field
refusal and no changes to existing configuration or bearer credential formats.
Entries must remain disabled: validation explicitly refuses `enabled: true` until
service integration is implemented. No broker is contacted by configuration
parsing. On 2026-10-07, affected configuration race, vet and staticcheck checks
passed, along with formatting and diff checks. Controls cover broker/route/channel
bounds, endpoint and TLS constraints, credential references, signed authority,
read cursor requirements, channel collisions and existing configuration parsing.
Service integration and native qualification remain pending.

`auth.ReadRedisPassword` loads 1-4,096 exact bytes from the referenced existing
private regular file. The existing descriptor-based ownership, file/parent
permissions and immediate symlink checks apply; platforms without those native
checks retain their existing platform refusal. No whitespace, newline, CRLF, NUL
or UTF-8 transformation occurs. A trailing newline is part of the password, so
operators must provision the exact broker password bytes. Redis passwords do not
use the AIRC bearer's 64-character hexadecimal format. The connection owner owns
the returned byte buffer and must clear it after use. Empty, oversized or unsafe
files return no credential, and errors disclose neither content nor path.

On 2026-10-07, authentication race tests passed in 4.573s; vet, staticcheck,
formatting and diff checks passed. Controls exercise exact one-byte and
4,096-byte passwords, binary and whitespace preservation, independent returned
buffers, empty/oversized refusal, regular-file/private-parent boundaries,
immediate file/parent symlinks, FIFO refusal and unchanged bearer-token validation.
These checks establish password loading, not broker authentication or service
startup.

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

The connection-pair prerequisite is implemented in `adapter/redis_pair.go` and
`redis_io.go`, with one application owner. It reserves two shared socket slots
before dialing, rolling back a partial reservation without opening a socket.
Publisher and subscriber use separate TLS 1.3 connections, each with its own
explicit ACL AUTH. Verified server name and trust are required; an insecure TLS
configuration is refused. The complete dial/TLS/AUTH/subscription setup shares
one absolute three-second deadline, bounded further by parent cancellation.

Subscriptions are pipelined in batches of at most 64 literal channels. Confirmation
frames must name distinct configured channels with the exact increasing count.
Well-formed publications for already confirmed channels during establishment are
discarded until every subscription is confirmed; this is explicit pre-intake loss,
without an application queue or journal claim. Unknown channels, unexpected types,
null fields and invalid counts terminate the pair. Established subscriptions wait
without an idle timeout, then apply an absolute three-second frame deadline after
the first byte. Results may use only configured response channels, retain the
codec payload bound and require a nonnegative integer PUBLISH reply. Zero is a
valid protocol count, not a durable receipt. Result write and reply share a single
three-second deadline.

Cancellation closes the raw sockets to interrupt handshake/read/write operations;
pair closure joins its cancellation callback before releasing both shared slots.
Closure is idempotent, route grants are copied, and broker error text is never
returned. Encoded AUTH buffers are cleared after writing. The caller supplies
validated configuration, trust and private password bytes, and owns clearing those
passwords. The reconnecting service loop and socket dispatch integration remain
pending.

On 2026-10-07, final affected Redis pair race tests passed in 5.217s; adapter vet,
staticcheck, formatting and diff checks passed. Actual isolated TCP/TLS scripted
peers exercise separate AUTH, exact binary payloads, route snapshots, zero-count
publication, partial capacity rollback, certificate/insecure-TLS refusal,
second-socket AUTH failure, malformed subscription/message/result replies,
parent setup deadlines, 128-channel batching and setup-time loss, slow-frame
deadlines, idle cancellation and blocked result-read cancellation. Failed pairs
release capacity while preserving an occupied foreign slot. These checks establish
the connection owner only; no real Redis server/client, service integration or
native AIRC acceptance is claimed.

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

The independent framing prerequisite is implemented in
`internal/gateway/rediswire`. Decoding owns payload storage and retains pipeline
bytes beyond the complete frame. Declared bulk and aggregate bounds, remaining
frame bytes and the minimum aggregate-child budget are checked before their
storage allocation or body consumption. Encoding preflights type-specific fields,
null representation, depth and total size before allocating the wire buffer.
Unsupported or malformed input is terminal for the connection; the owner must
not attempt parser resynchronization.

On 2026-10-07, final affected race tests passed in 1.304s; vet, staticcheck, formatting
and diff checks passed. A five-second two-worker fuzz run completed 439,617
executions in 5.339s without failure. Controls cover exact simple/bulk/array/depth
and 65,536-byte frame boundaries, one-byte overflow, minimum aggregate-child
budget refusal before consuming children, complete binary/null/empty round trips,
owned payloads, pipelining, representative truncations, signed-integer limits,
malformed/unsupported types and irrelevant encoder fields. These results establish
framing only; no broker authentication, service or native Redis qualification is
claimed by this checkpoint.

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
