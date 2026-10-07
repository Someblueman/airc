# Kafka ingestion contract

Kafka ingestion is not enabled. The implementation and real producer/broker/native
qualification remain in [ROADMAP.md](ROADMAP.md).

## Intent and record identity

Producers choose a stable AIRC request ID before signing the existing message
envelope. The record key must equal that signed request ID, and the value is the
compact signed envelope. The gateway verifies current signer authority, realm,
principal, configured topic/partition route, target, operation and freshness.
Kafka ACL credentials authorize broker access; they do not establish AIRC sender
authority. Existing signed intent and journal fingerprints must remain intact.

Kafka assigns the partition offset independently of producer intent. Record
identity is the tuple of configured logical broker-history namespace, literal
topic, partition and offset. `config.KafkaRecordID` is lowercase hexadecimal SHA-256 of
`"kafka-record\x00" + namespace + "\x00" + topic + "\x00" + partition + "\x00" + offset`,
with canonical nonnegative decimal partition/offset and no trailing separator.
The namespace uses the existing owner-ID syntax. Topic names follow
[Kafka's literal ASCII rules](https://github.com/apache/kafka/blob/3.9/clients/src/main/java/org/apache/kafka/common/internals/Topic.java):
1–249 bytes, ASCII letters/digits/dot/underscore/hyphen, excluding `.` and `..`.
The vector `broker/events/0/0` is
`8547fc741f73560b1ed93f5a8a142194a1f368bed04d446d51d3f077f14dc933`.
This identifier is distinct from the signed request ID. Repeating the
same signed intent at another offset must retain the same durable AIRC job;
changing intent under the same request ID must retain conflict rejection.
A durable record association must reject changed request identity or content at
an already retained record identity. Topic deletion/recreation or replacement
of logical broker history requires a new namespace; endpoint or owner renaming
must not change record identity. No gateway rewrites a signed request ID.

## Ownership and advancement

The initial mode explicitly assigns fixed topic partitions to a configured owner.
It does not rely on group rebalancing. Local validation must reject overlapping
ownership within the same logical history and consumption scope. Operators must
ensure another gateway/process does not concurrently own that scope. Every owner
has bounded sockets, request slots, fetch frames and retained state.

Offset advancement follows the ordered records returned for each partition.
Offsets need not be numerically consecutive: Kafka documents gaps after compaction
or transactions. A later accepted record cannot advance past an earlier returned
record that has not established the required durable outcome. Invalid, oversized,
unauthorized, conflicting or expired-unretained records block that partition;
they cannot be silently skipped. Other partitions must remain independently
bounded and able to progress.

The authoritative next position must be saved durably with its record outcome in
the gateway journal. Restart begins from that retained position. No automatic
commit or reset to latest is allowed. Lost replies and crashes must replay the
same record/intent identity. A retained expired Send may only prove an identical
existing outcome under current authority, without refreshing freshness, extending
replay retention or creating new work. Journal retention and quota behavior must
be specified before introducing durable record/offset state.

Broker offset storage, if used, follows the locally proven durable position;
it cannot be authoritative ahead of it. Broker acknowledgements never establish
native AIRC acceptance. Result publication and the next-position boundary must
retain whole bounded results, including read pages/cursors where the selected
return profile supports them. The selected result publication profile below preserves complete results.

## Protocol evidence and remaining decisions

The [Kafka consumer contract](https://downloads.apache.org/kafka/4.1.2/javadoc/org/apache/kafka/clients/consumer/KafkaConsumer.html)
documents offset gaps, manual partition assignment, manual commits of the next
position, and storing offsets together with results outside Kafka. These support
the ownership and durability requirements above; they do not qualify this gateway.
Journal migration, quota accounting and retention remain prerequisites to
durable admission/offset implementation.

On 2026-10-07 no Kafka producer/server executable or cached Go Kafka client module
was found. Docker inventory contained only Debian and Ubuntu base images. A pinned
isolated broker/client fixture requires separate owner authorization; no image,
client module or toolchain was downloaded. Scripted peers can prove prerequisites
but cannot replace real producer/broker and independent native observer evidence.

Canonical identity controls cover each tuple component, integer extremes, negative
positions, invalid namespace/topic text and topic length boundaries. Configuration
race tests passed on 2026-10-07 in 4.187s; affected vet/staticcheck, formatting and
diff checks passed. This prerequisite does not enable Kafka ingestion.

## Selected initial wire and return profile

The initial owner uses TLS 1.3 with verified certificates and SASL PLAIN over TLS.
Configured broker IDs map to explicit numeric IP/port endpoints and TLS names;
metadata cannot introduce an unconfigured destination or weaken verification.
The cluster ID must match an explicit configured ID. Namespace identifies logical
history independently of cluster ID. Each owner has one fixed request topic and
partition, one principal/target route, and one distinct result topic/partition.
Multiple owners provide independent partition progress. Ownership validation
includes the journal consumption scope; group membership is not used.

Use non-flexible request/response versions, checked against ApiVersions before
application commands. Unsupported versions fail closed without fallback:

| API | Key | Version |
|---|---|---|
| ApiVersions | 18 | 0 |
| SaslHandshake | 17 | 1 |
| SaslAuthenticate | 36 | 1 |
| Metadata | 3 | 8 |
| ListOffsets | 2 | 5 |
| Fetch | 1 | 11 |
| Produce | 0 | 7 |

The [Apache Kafka 3.9 schemas](https://github.com/apache/kafka/tree/3.9.0/clients/src/main/resources/common/message)
define these layouts. Fetch 11 and ListOffsets 5 carry leader epochs; Metadata 8
provides cluster ID and partition leader epochs. Metadata requests disable topic
auto-creation and authorization-operation listings. Fetch uses consumer replica
ID -1, read-committed isolation, no incremental session, one partition, 1000ms
maximum wait and bounded requested bytes. Produce uses no transactional ID,
acks=-1, one result record and a 1000ms broker timeout. Commands have one absolute
3-second local deadline, including writes and complete replies. Reconnect is
cancelable with bounded backoff; shutdown closes and joins raw sockets.

Every response is prefixed by a signed 32-bit length and contains the expected
correlation ID. Maximum complete frame body is 1 MiB; reject invalid lengths
before allocation. No frame resynchronization or unbounded collection allocation.
Application intent/result values are at most 32768 bytes and keys at most 64 bytes.
A fetch contains at most 64 batches and 256 records total. Validate CRC32C,
record/batch lengths, counts, offset arithmetic, timestamps, headers and complete
consumption of each frame before admitting any record from it. Fetch bounds are
also enforced locally because brokers may return an oversized first batch.

The selected producer profile uses magic-2, uncompressed, nontransactional data
batches. Transactional/control batches, compression and legacy magic are refused
without offset advancement; producers must use this documented profile. Batch
and record offsets may contain gaps. The owner serially processes bounded returned
records and stops its partition on an unaccepted record. Leader changes trigger
verified metadata refresh; out-of-range/truncated history must never reset position
silently. Initial position is explicit configuration, subsequently journal-owned.

Routes may explicitly authorize Send, ContextRead, HistoryRead and Search. AIRC
read pages are returned whole with their original metadata and cursor. Each result
record key is the original signed request ID; its value is a signed versioned
response binding realm, principal, binding, namespace, request/result topic and
partition, broker record offset/identity, target and original request ID. Clients
verify that scope before consuming the result. Whole output exceeding 32768 bytes
returns a bounded capacity failure and leaves the record position unchanged.

A completed result must be durably retained before publication, then published
before advancing the local authoritative position. Publication reply loss may
produce duplicate signed result records; clients correlate stable record identity.
Fresh signing of a retained result must still check current authority. Reads need
a retained outcome so replay after publication loss does not execute a changed
page. Durable Send means journal intake; native acceptance still requires worker
and observer evidence. Exact durable-state retention/quota design must be settled
in the admission checkpoint before changing the journal schema.

## Framing checkpoint

`kafkawire.Request` emits the selected non-flexible header version 1 with fixed
client ID `airc-gateway` and an owned body. `kafkawire.Response` reads response
header version 0, validates signed length and expected correlation before payload
allocation, and returns an owned command body without consuming subsequent frames.
Any error is terminal for the owning socket. Command schemas and socket deadline
policy remain separate prerequisites; framing alone does not enable ingestion.

Race tests passed on 2026-10-07 in 1.376s. Exact header bytes, input ownership,
maximum/oversized frames, negative header fields, fragmented reads, pipelining,
empty replies, mismatched correlation, invalid length and every truncation boundary
are covered. Affected vet/staticcheck, formatting and diff checks passed. Full
acceptance remains due at Kafka integration.

## API negotiation and SASL codec checkpoint

`VersionsReply` checks the complete selected version set, bounded API count,
duplicate keys and valid version intervals. Unknown well-formed API keys are
allowed. PLAIN handshake requires a successful bounded list containing PLAIN;
malformed, duplicate or trailing data is refused. Authentication emits the exact
SASL empty-authorization-ID/username/password sequence in an owned buffer. Username
is at most 255 bytes and password at most 4096 bytes; both are nonempty UTF-8 without
NUL separators. The socket owner must clear the emitted credential buffer after
writing over verified TLS. Authentication replies expose only the nonnegative
session lifetime, never raw broker error text. Positive lifetime requires owner
reconnect before expiry; zero does not request re-authentication.

Race tests passed on 2026-10-07 in 1.271s; affected vet/staticcheck, formatting and
diff checks passed. Controls cover exact command bytes, credential ownership,
version presence/ranges/duplicates, unknown API keys, credential bounds, malformed
mechanism lists, correlation-independent body truncation and authentication
failure/lifetime handling. These are body codecs only; verified socket
ownership and actual broker authentication remain outstanding.
