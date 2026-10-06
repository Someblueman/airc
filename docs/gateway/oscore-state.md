# OSCORE durable sequence and replay state

Journal schema 11 adds OSCORE state independently of delivery jobs and their
stable request IDs. Version 10 journals migrate transactionally without changing
their jobs, identity pin, authorization, or existing replay markers.
This is an internal store boundary; the native OSCORE gateway listener and owner
provisioning command remain unimplemented.

The trusted provisioning caller must generate fresh cryptographic material and
calculate a digest from its canonical context, including algorithms, secret,
salt, sender/recipient IDs, and context ID. Formatting, labels, or an owner name
must not affect that digest. The store keeps only the digest and its principal
and binding; it stores no secret. The forthcoming provisioning path must prevent
reuse of prior key material. An arbitrary supplied digest does not establish
freshness or safe cryptographic key derivation.

`ProvisionOSCORE` is exclusively a trusted owner operation for a new context.
It refuses existing digests, identity rebinding, unknown/revoked principals, and
more than 1024 lifetime contexts. State records are never pruned or reset.
Provisioning checks both configured and durably recorded revocation under the
store lock; reopening with the original active configuration cannot undo a
recorded revocation.
`LoadOSCORE` refuses missing state and mismatched identity; listener startup must
fail instead of provisioning a replacement row. Losing state requires freshly
generated keys at both peers. Restoring an older backup with retained keys is
unsafe and cannot be detected by this store alone.

`ReserveOSCORESender` commits a strictly increasing reservation end before native
encryption. On restart the native sender must begin at the stored end, skipping
unused sequence numbers. The exclusive ceiling matches the pinned libcoap
implementation. `SaveOSCOREReceiver` must run after authenticated decryption and
before application dispatch. It distinguishes an uninitialized recipient from
an initialized sequence-zero/window-one state, rejects sequence regression and
clearing retained seen bits, and permits identical saves or adding seen bits for
out-of-order packets. Bits for impossible negative sequence numbers are refused.
The unsigned 64-bit window is encoded as eight big-endian bytes so its top bit
round-trips through SQLite. Native gateway contexts must set `replay_window=64`
(`store.OSCOREReplayWindow`); the library's smaller default is incompatible with
this retention contract and must not be used by the forthcoming provisioning path.

All operations use the existing private owned local journal, single connection,
exclusive process lock, WAL with `synchronous=FULL`, configured page limit, and
checkpoint backpressure. A persistence error must make the native callback
return failure and poison that context through the
[patched dependency](../../native/oscore/README.md); an ambiguous write must
never permit encryption or plaintext dispatch.

Store tests exercise real private SQLite files, read-only write failure,
monotonic sender reservations, replay-bit retention, sequence-zero initialization,
unsigned-window encoding, corrupt-window rejection, context capacity, and
version-10 migration with an existing delivery job. A subprocess commits ends
8 and 16 plus receiver sequence zero/window one, then is killed with SIGKILL.
Reopening restores those exact values, rejects a competing owner, refuses
reservation reuse, and permits the next end 24. This proves process-restart
behavior of the store, not native packet replay after gateway restart or physical
power-loss durability. Native AIRC acceptance remains on the roadmap.

Verification on Darwin, 2026-10-06: during the `9c78bf5` implementation, all
`internal/gateway` packages passed under the race detector, including the journal
tests (33.893 seconds). The
OSCORE state tests passed twice under race detection. A native plain-CoAP-TCP
send/cursor-read control passed against the migrated journal (10.156 seconds),
gateway CLI race tests passed (1.878 seconds), and command builds, repository vet,
and staticcheck passed. Final full-module normal/race acceptance remains open.
After correcting durable provisioning revocation, all OSCORE tests passed twice
(3.712 seconds), the complete journal race suite passed (31.657 seconds), and
journal vet and formatting checks passed.
