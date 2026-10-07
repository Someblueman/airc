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

## Metadata and offset codec checkpoint

Metadata v8 requests include one or two explicit distinct topics and disable
creation/operation listings. Replies require the configured cluster ID and exact
requested topic set. The codec bounds brokers to 32, partitions to 256 total and
replica/ISR/offline lists to 32 each. It rejects broker/topic/partition duplicates,
unknown leader/replica references, invalid text/ports/epochs, internal topics,
broker errors, trailing bytes and incomplete frames. Returned host/topic strings
are owned. Advertised broker addresses confer no dial authority; the socket owner
must still resolve IDs through the configured verified endpoint map.

ListOffsets v5 encodes one fixed partition with read-committed isolation and a
leader epoch. Replies require exactly that topic/partition, a successful available
nonnegative position, valid timestamp/epoch and complete bounded consumption.
Earliest/latest/timestamp queries exist at the codec boundary; the owner may not
use them to silently reset retained journal positions.

Wire/configuration race tests passed on 2026-10-07 in 1.310s/1.844s; affected
vet/staticcheck, formatting and diff checks passed. Controls cover exact request
bytes, reply ownership, every truncation boundary, scope mismatch, count limits,
invalid references, unavailable/error outcomes and trailing data. Socket ownership,
record batches, durable positions and native qualification remain unfinished.

## Magic-2 record checkpoint

`RecordBatch` emits one owned uncompressed nontransactional record with absent
producer identity and broker offset/epoch placeholders. `Records` validates an
entire bounded record set before returning any owned key/value bytes. The layout
and Castagnoli CRC range follow [Apache Kafka's batch implementation](https://github.com/apache/kafka/blob/3.9.0/clients/src/main/java/org/apache/kafka/common/record/DefaultRecordBatch.java).
It bounds batches to 64 and records to 256 total; keys to 64 bytes, values to
32768 bytes, headers to 32, header keys to 255 UTF-8 bytes and header values to
32768 bytes. Empty/null header values are permitted. Application keys/values must
be nonempty; null/tombstone intent cannot establish intake.

Canonical zigzag integers, lengths, flags, CRC, producer fields, epochs, monotone
record/batch offsets and timestamp/offset arithmetic are checked. Compaction gaps
and empty compacted batches preserve the declared last offset separately from
remaining records. Broker append-time batches use their maximum timestamp even
when producer base time differs. Compression, transactional/control/delete-horizon
flags and legacy magic fail closed. No partially validated batch set is returned.

Race tests passed on 2026-10-07 in 1.285s; affected vet/staticcheck, formatting and
diff checks passed. Controls cover independently specified record bytes, owned
input/output, CRC failure, every truncation boundary, compacted gaps/empty batches,
append-time timestamps, unsupported formats, malformed fields, frame/batch/record
and value limits, whole-set refusal, headers, canonical varints and arithmetic
overflow. Durable admission, socket ownership and actual
producer/broker/native qualification remain outstanding.


## Fetch and produce codec checkpoint

`kafkawire.Fetch` encodes one fixed partition using Fetch v11, read-committed
isolation, a one-second maximum wait, no incremental session and a bounded byte
budget. Replies require the exact topic/partition, successful top-level and
partition status, no active session, consistent log-start/stable/high-watermark
positions and complete frame consumption. Null or empty records are accepted;
nonempty aborted-transaction lists fail closed under the nontransactional record
profile. Preferred replica IDs are parsed without granting endpoint authority.
Every returned batch passes the whole-set validator and ends below the stable
offset. Batch bases may precede the requested position: the eventual owner must
filter retained records and preserve compaction gaps without resetting its
journal position.

`kafkawire.Produce` encodes one magic-2 batch in Produce v7, no transactional ID,
all-replica acknowledgements and a one-second timeout. Replies validate the exact
partition, broker status, offset/timestamp boundaries and complete consumption.
A successful reply confirms broker publication only. A lost reply can cause
republication; signed result identity must correlate duplicates.

The affected package passed race tests (1.278 s), vet, staticcheck, formatting and
diff checks. Independent byte fixtures cover the selected request layouts,
reply truncation, scope/error/boundary refusal, empty fetches and malformed record
CRC. These codecs do not establish real producer/broker or native qualification.

## Fixed-owner configuration checkpoint

The optional `kafka` array accepts up to 32 disabled owners. Each owner requires
an ID, logical history `namespace`, expected `cluster_id`, 1–32 allowlisted
`brokers`, SASL `credential` and one `route`. Broker fields are nonnegative `id`,
numeric non-wildcard `address`, verified `tls_server_name` and optional absolute
`tls_ca_file`. The credential names an explicit `username` and absolute private
`password_file`. Trust files may be shared by brokers; password and trust files
must not alias. File checks preserve the existing regular-file, private-password
and state/credential separation rules.

Route fields are `principal`, `topic`, `partition`, explicit nonnegative
`initial_offset`, distinct `result_topic`, `result_partition`, existing `target`
and explicit `operations`. The maximum int64 initial offset is refused because
it cannot advance. Omitting or nulling the initial position cannot default to
zero. The active signing principal must authorize the exact target and every
operation; history/search require a cursor key and every owner requires a
response signer. Kafka remains refused when `enabled` is true until integration.

Owners sharing a cluster must use the same broker-ID/address/TLS/trust allowlist,
independent of ordering. One endpoint cannot represent two clusters and one
logical namespace cannot span clusters. Request/result partition roles cannot
collide within a physical cluster, even when owners choose different history
namespaces. This validates local ownership; operators must prevent another
process from owning the same partition.

Affected config and wire race tests passed; the final config suite including
32/33 owner and broker boundaries passed in 4.033 s. Vet, staticcheck, formatting
and diff checks passed. Tests cover signed grants, explicit positions, IPv4/IPv6,
allowlist order, duplicate roles/endpoints, cluster conflicts, strict JSON round
trip, public password refusal and trust/password aliasing. This configuration
checkpoint does not enable sockets or qualify an actual Kafka cluster.

## Command I/O checkpoint

The serial `adapter.kafkaCommand` exchange writes every byte of one bounded
request and reads one correlated response under a single absolute deadline,
clamped to three seconds. It refuses expired deadlines, incomplete/zero writes,
malformed replies and correlation mismatches with a generic connection error.
The owned request buffer is cleared on every return, including SASL credentials;
caller-owned input remains unchanged. This helper expects an already verified TLS
socket. The upcoming socket owner must close the raw connection on cancellation
or any exchange/command-validation error and join cancellation before releasing
shared capacity.

Focused adapter race tests passed in 1.624 s using real pipe connections, including
partial writes, retained wire-buffer clearing, malformed replies, blocked writes
and a reply header whose promised body never arrives. Adapter vet/staticcheck,
formatting and diff checks passed. TLS/SASL setup, endpoint checks, reconnect,
durable admission and real Kafka qualification remain unfinished.

## TLS/SASL socket checkpoint

`adapter.openKafka` reserves one shared socket slot before dialing a configured
numeric non-wildcard endpoint. It clones the supplied TLS configuration, enforces
TLS 1.3 and verifies the configured server name with certificate verification
required. Dial, TLS handshake, selected API-version negotiation, PLAIN mechanism
negotiation and SASL authentication share one three-second setup deadline. No
mechanism/version fallback occurs and no credential is sent before verified TLS.
Owned authentication bodies and request frames are cleared on return.

Each connection has one serial command owner and increasing correlation IDs;
exhaustion closes it rather than wrapping. Commands share the bounded I/O layer.
Positive SASL session lifetimes are measured conservatively from authentication
request start, bound command deadlines and cancel the connection at expiry;
overflowing or already expired lifetimes fail closed. A zero lifetime needs no
expiry timer. Cancellation closes the raw socket, and idempotent close joins the
cancellation callback and expiry timer before releasing the slot. Dial/setup
failures also join and release capacity. Command errors are terminal; subsequent
command-specific validation errors must likewise close the owner.

Focused Kafka adapter race tests passed in 4.918 s, including exact setup command
order/bytes, verified TLS success, certificate refusal, API/mechanism/auth failures,
correlation mismatch/exhaustion, blocked setup cancellation, the real setup
deadline, session expiry, repeated close and shared-capacity refusal. Adapter vet,
staticcheck, formatting and diff checks passed. Tests use isolated local scripted
TLS peers; they do not qualify a Kafka broker. Cluster metadata verification,
leader reconnect, durable admission and service integration remain unfinished.

## Pinned metadata checkpoint

`kafkaConn.metadata` requests only the configured request/result topics with
creation and operation listings disabled. The bounded v8 parser requires the
expected cluster ID and validates the whole response. The adapter then requires
every advertised broker ID to exist in the configured allowlist and checks its
port and host. Numeric hosts must equal the pinned dial IP, including equivalent
IPv6 spellings; DNS hosts must equal the configured TLS name, ignoring DNS case
and a trailing dot. No advertised host is resolved or dialed.

The configured topic/partition pair must each have one advertised leader and a
nonnegative epoch. Returned leader plans contain copies of the configured broker
endpoint/TLS/trust fields, never advertised dial authority. Other partitions of
the requested topics do not change ownership. Any codec or trust failure closes
and joins the connection before returning an empty plan.

Focused Kafka adapter race tests passed in 4.893 s; adapter vet/staticcheck,
formatting and diff checks passed. Tests cover cluster/ID/host/port/partition/epoch
refusal, duplicate/missing leaders, IPv4/IPv6 and DNS normalization, plus scoped
metadata commands through authenticated scripted TLS peers. An advertised host
mismatch closes the socket and releases capacity without another dial. Joined
leader reconnect, durable admission, service integration and real Kafka
qualification remain unfinished.

## Joined leader reconnect checkpoint

`openKafkaPair` authenticates a configured bootstrap broker and obtains a pinned
leader plan. It reuses the bootstrap socket when it is the request leader;
otherwise it closes and joins bootstrap before opening that leader. Colocated
request/result leaders share one serial socket. Split leaders use at most two
shared slots. Newly opened leader sockets independently authenticate and verify
cluster metadata against the complete initial leader/epoch plan; a change during
setup rejects and joins the entire pair. Expired or canceled connections cannot
be returned as a ready pair. Readiness compares each positive expiry directly
with the current time, independently of cancellation-timer scheduling; command
entry and completion use the same readiness check.

`runKafkaConnections` snapshots broker/route configuration, TLS configurations and
owned password bytes. It rotates configured bootstrap seeds, closes each pair
before waiting, and clears owned credentials on return. Failure backoff doubles
from 100 ms to five seconds; a successful authentication alone does not reset it.
Only verified record-session progress resets backoff. Parent cancellation
interrupts socket work and the backoff timer, and return follows joined socket
closure. The session callback is the boundary for the pending durable record
owner; this loop does not admit records or move positions.

Focused Kafka adapter race tests passed in 5.437 s; adapter vet/staticcheck,
formatting and diff checks passed. Scripted TLS tests cover shared/split leaders,
a non-request bootstrap, epoch change during setup, second-socket capacity
failure, repeated pair close, authentication-failure backoff, successful-session
reconnect, cancellation of maximum backoff and expiry refusal before the timer
callback executes. Durable record admission,
service integration and actual Kafka/native qualification remain unfinished.

## Durable journal design

The next additive migration is schema v15. Existing v1–v14 jobs, replay marks,
authorization, OSCORE, queue ownership, syslog and SNMP state remain intact. The
migration creates empty Kafka tables inside the existing initialization
transaction; it does not infer positions from jobs or broker commits. Unsupported
newer schemas remain refused. Existing migration fixtures must actually remove
newer tables when constructing an older database.

Three tables express the required boundaries:

- `kafka_positions` identifies namespace/topic/partition and pins cluster,
  principal/binding, target and result topic/partition. It stores the authoritative
  next offset and last verified leader epoch. Owner ID, endpoint, credential and
  TLS-file paths are absent. First provisioning uses the explicit initial offset;
  later provisioning returns the stored position and refuses a changed route or
  cluster. Positions survive retention and owner removal, capped at 32 per journal.
- `kafka_intents` identifies principal/binding/original signed request ID, with the
  original normalized request and existing intent fingerprint. It retains one
  complete bounded semantic outcome, independent of broker record metadata.
  Result publication wraps that outcome in the record-specific signed scope.
  Concurrent copies of an identical read may compute a result, but the first
  committed whole outcome wins and every publisher uses the retained winner.
  Conflicting intent never overwrites it. This also preserves a whole read result
  when a producer republishes the same signed intent at another offset.
- `kafka_records` associates the canonical record ID and immutable offset tuple
  with that intent. It stores the expected prior position and publication state.
  One unfinished record per partition blocks later intake. Records cannot change
  principal, binding, request ID or content under an existing record identity.

All state APIs require the store lock, closed-state checks and current durable
principal authorization where an operation is involved. The configured route
must match the provisioned scope. Broker credentials and record IDs never mint
an AIRC identity. Trusted verified codec output supplies freshness/replay bounds;
raw Kafka values cannot call these state APIs directly.

Fresh Send reservation and existing `admitInTx` run in one transaction: job,
record association and intent reservation commit together or all roll back.
Reads reserve their authenticated intent/record without inventing a delivery job,
then use existing bounded read authorization. An expired Send takes a separate
lookup-only path: compare current authority and the original stored fingerprint,
match an identical retained durable job, and associate its outcome without
admitting or extending job/replay lifetime. Expired reads and unknown expired
Send intent remain refused. A reserved record without an outcome is restartable
and cannot advance the partition.

Persist the complete semantic outcome before publishing it. Each serialization
and final signed publication must satisfy the selected whole-result bound;
capacity failure leaves the record unfinished and the position unchanged.
Retries reseal the retained semantic outcome using the current response signer
and current response freshness, retaining the original read page and cursor.
They never rerun a read after an outcome has committed. Broker response bytes do
not determine journal contents. Broker publication uncertainty may duplicate a
signed result, correlated by its record and original request IDs.

Only after a successful all-replica Produce reply may one transaction mark the
record published and advance the position to its offset plus one. The compare
against the saved prior position and unfinished record prevents advancement past
a different unaccepted record. Numerical offset gaps are permitted. A crash
before that commit republishes the retained whole outcome; a crash afterward
starts at the saved next offset. No native acceptance is inferred from Produce.

A fully validated compacted range containing no remaining unprocessed records
may advance only its proven empty range, with no unfinished record, the expected
current position and a nondecreasing verified epoch. This creates no job or
sender outcome. It must not skip any returned unaccepted record. Metadata epoch
regression, log start beyond the retained position, out-of-range positions and
truncation fail closed; no path resets to earliest/latest automatically.

Quota and retention are fail-closed. The existing `max_page_count`, reserved
WAL/index budget and pre-write truncating checkpoint apply to every Kafka
transaction. Intents and record associations are each capped at 16384 retained
rows; unfinished read intents also consume the existing total/per-principal
pending budget alongside delivery jobs. Send uses the existing job budget,
without charging the same intent twice; duplicate records share the intent.
Unfinished records are separately bounded to one per provisioned partition.
A pending record/intent/outcome is never pruned by age. Pending Send
associations protect the matching job from terminal-result cleanup. Completed
record associations become eligible only after their stored terminal-retention
window. An intent becomes eligible only when no record references it, its stored
retention has elapsed and its original replay window has passed. Cleanup and
position state commit transactionally; cleanup never rolls a position backward
or removes its ownership pin. Expired lookup does not extend any retention window.
A full journal or row budget blocks that partition without acknowledging,
publishing a replacement partial result, evicting unexpired state or moving its
position. Retention policy may need operator adjustment for throughput; tests do
not establish a performance claim.

The schema and partition-provisioning implementation follows below. Record/intent
state APIs, transactional admission, adapter sessions and runtime acceptance
remain unfinished.


## Schema v15 and position provisioning checkpoint

The additive v15 migration creates the three Kafka tables with strict row types,
bounded lengths/positions, outcome-state consistency, foreign-key associations
and one unfinished record per partition. Existing data is not rewritten or used
to invent Kafka positions. Older migration fixtures remove v15 tables before
constructing their requested historical schema.

`Store.ProvisionKafka` resolves the configured owner and current durable
revocation state, pins its namespace/topic/partition route and seeds only a new
position from the explicit initial offset. Existing positions override changed
initial offsets; cluster, principal/binding, target and result partition changes
conflict. Owner/endpoint renaming retains the same position. Stored positions are
permanent and capped at 32; a full cap preserves retired scopes and refuses new
provisioning. New writes require the existing checkpoint and SQLite transaction.
Store opening snapshots Kafka configuration, including initial-offset pointers.

Focused store migration/provisioning race tests passed in 5.986 s, with affected
vet/staticcheck, formatting and diff checks. Tests cover v14 migration preserving
jobs/OSCORE, empty initial Kafka tables, older migration paths, restart positions,
route conflicts, owner/endpoint renaming, revocation, closed state, input snapshots
and retired-position capacity. Full unchanged-source normal/race acceptance and
build, formatting, vet, staticcheck and diff gates subsequently passed; each
suite recorded 3772 passes, 53 skips and no failures. The first full normal
attempt had an HTTP/3 inactivity timeout that did not reproduce in its immediate
focused rerun or the fresh full run. Exact commands, source digest and retained
failure evidence are in [the acceptance checkpoint](testing.md#kafka-schema-v15-checkpoint).
Record/intent APIs, retention/quota integration, publication advancement and
Kafka service enablement remain unfinished.

## Atomic reservation checkpoint

`Store.ReserveKafka` snapshots and normalizes trusted signed dispatcher output,
checks its current route, durable authority, signed lease and exact saved
position, then reserves the original intent and record in the same transaction
as `admitInTx` for Send. Reads create no delivery job. A retained record with
changed intent conflicts; an unfinished partition refuses later records.
Reservation never advances a position or publishes a result.

`Store.ReserveRetainedKafkaSend` matches the original fingerprint of a retained
durable Send under current authority before associating its record. It does not
call admission or extend job/replay/intent lifetime. Unknown historical Send
remains refused. New historical intent rows inherit the job's original admission,
replay and retention fields.

Both retained tables refuse new rows at 16384 without evicting existing state.
Unfinished read intents share the ordinary total/per-principal pending budget;
identical copies across partitions consume one charge. Send uses its existing
job charge. Maintenance protects a job while any unfinished Kafka association
references it. Position pins and pending Kafka records remain untouched.

Focused Kafka race tests passed with rollback, restart, conflicts, revocation,
shared read budgets, retained Send cleanup protection and both retained-row caps.
The complete store race suite passed in 54.914 s. Full unchanged-source normal
and race acceptance, build, formatting, vet, staticcheck and diff checks also
passed; each suite recorded 3784 passes, 53 skips and no failures. Exact evidence
is in [the acceptance checkpoint](testing.md#kafka-atomic-reservation-checkpoint).
Whole-outcome persistence, publication advancement, completed retention cleanup
and service integration remain unfinished.
