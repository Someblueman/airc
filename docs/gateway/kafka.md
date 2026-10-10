# Kafka removed

Kafka ingestion is no longer supported. The consumer, wire codecs, configuration,
service owners, dispatcher and journal APIs, and Kafka-specific tests have been
removed. The former implementation had scripted peer tests but no demonstrated
real producer/broker-to-native-AIRC delivery.

Remove the top-level `kafka` field from existing gateway configuration before
starting this version. Strict configuration decoding rejects that field, including
an empty array, instead of silently pretending to run a consumer. Other transports
continue using their existing configuration and identities.

Historical SQLite migration 15 and its three tables remain unchanged. Existing
positions, intents, outcomes and publication records are preserved on reopening;
normal maintenance also retains jobs awaiting a historical Kafka publication.
No consumer advances those positions or publishes those results. Preserve a backup
of the old journal if manual reconciliation is needed. Kafka-only reads no longer
consume active pending-job capacity.

Older Kafka entries in [testing.md](testing.md) describe historical checkpoints,
not a supported connector or current acceptance claim. There is no Kafka
qualification item on the active roadmap.
