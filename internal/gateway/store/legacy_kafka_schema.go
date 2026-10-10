package store

// Historical schema only: Kafka ingestion has been removed. Keep these tables
// and their contents so upgrades preserve existing delivery and replay evidence.
const migration15 = `
CREATE TABLE kafka_positions (
 namespace TEXT NOT NULL CHECK(length(namespace) BETWEEN 1 AND 64),
 topic TEXT NOT NULL CHECK(length(topic) BETWEEN 1 AND 249),
 partition INTEGER NOT NULL CHECK(partition BETWEEN 0 AND 2147483647),
 cluster_id TEXT NOT NULL CHECK(length(cluster_id) BETWEEN 1 AND 255),
 principal TEXT NOT NULL, binding TEXT NOT NULL, target BLOB NOT NULL,
 result_topic TEXT NOT NULL CHECK(length(result_topic) BETWEEN 1 AND 249 AND result_topic<>topic),
 result_partition INTEGER NOT NULL CHECK(result_partition BETWEEN 0 AND 2147483647),
 next_offset INTEGER NOT NULL CHECK(next_offset>=0),
 leader_epoch INTEGER NOT NULL CHECK(leader_epoch BETWEEN -1 AND 2147483647),
 PRIMARY KEY(namespace,topic,partition)
) STRICT;
CREATE TABLE kafka_intents (
 principal TEXT NOT NULL, binding TEXT NOT NULL, request_id TEXT NOT NULL CHECK(length(request_id) BETWEEN 1 AND 64),
 fingerprint TEXT NOT NULL CHECK(length(fingerprint)=64),
 request BLOB NOT NULL CHECK(length(request) BETWEEN 1 AND 32768),
 operation TEXT NOT NULL CHECK(operation IN ('send','context.read','history.read','search')),
 replay_until INTEGER NOT NULL CHECK(replay_until>=0),
 admitted_at INTEGER NOT NULL CHECK(admitted_at>=0),
 retention_seconds INTEGER NOT NULL CHECK(retention_seconds>0),
 outcome BLOB CHECK(length(outcome) BETWEEN 1 AND 32768),
 outcome_at INTEGER CHECK(outcome_at>=admitted_at),
 CHECK((outcome IS NULL AND outcome_at IS NULL) OR (outcome IS NOT NULL AND outcome_at IS NOT NULL)),
 PRIMARY KEY(principal,binding,request_id)
) STRICT;
CREATE TABLE kafka_records (
 namespace TEXT NOT NULL, topic TEXT NOT NULL, partition INTEGER NOT NULL,
 offset INTEGER NOT NULL CHECK(offset>=0),
 record_id TEXT NOT NULL UNIQUE CHECK(length(record_id)=64),
 principal TEXT NOT NULL, binding TEXT NOT NULL, request_id TEXT NOT NULL,
 expected_position INTEGER NOT NULL CHECK(expected_position>=0 AND expected_position<=offset),
 admitted_at INTEGER NOT NULL CHECK(admitted_at>=0),
 retention_seconds INTEGER NOT NULL CHECK(retention_seconds>0),
 completed_at INTEGER CHECK(completed_at>=admitted_at),
 PRIMARY KEY(namespace,topic,partition,offset),
 FOREIGN KEY(namespace,topic,partition) REFERENCES kafka_positions(namespace,topic,partition),
 FOREIGN KEY(principal,binding,request_id) REFERENCES kafka_intents(principal,binding,request_id)
) STRICT;
CREATE UNIQUE INDEX kafka_pending_partition ON kafka_records(namespace,topic,partition) WHERE completed_at IS NULL;
CREATE INDEX kafka_record_intent ON kafka_records(principal,binding,request_id);
CREATE INDEX kafka_record_retention ON kafka_records(completed_at);
PRAGMA user_version=15;
`
