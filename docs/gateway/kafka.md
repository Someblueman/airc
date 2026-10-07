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
topic, partition and offset. Its canonical serialization/hash must be defined
and tested before use. It is distinct from the signed request ID. Repeating the
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
return profile supports them. The exact read/result publication profile remains
to be selected alongside command versions and wire bounds before coding.

## Protocol evidence and remaining decisions

The [Kafka consumer contract](https://downloads.apache.org/kafka/4.1.2/javadoc/org/apache/kafka/clients/consumer/KafkaConsumer.html)
documents offset gaps, manual partition assignment, manual commits of the next
position, and storing offsets together with results outside Kafka. These support
the ownership and durability requirements above; they do not qualify this gateway.
Concrete API versions, TLS/SASL mechanism, verified endpoint allowlist, record
batch/compression bounds, result topic and journal migration remain to be defined.

On 2026-10-07 no Kafka producer/server executable or cached Go Kafka client module
was found. Docker inventory contained only Debian and Ubuntu base images. A pinned
isolated broker/client fixture requires separate owner authorization; no image,
client module or toolchain was downloaded. Scripted peers can prove prerequisites
but cannot replace real producer/broker and independent native observer evidence.
