# Linux Unix-datagram acceptance evidence

> Dated evidence. Runs recorded here predate the
> [identity removal](removed-identity.md): their principals, signed or
> encrypted requests and return routes no longer exist.

The existing Unix-datagram connector was qualified on Ubuntu 24.04 ARM64,
Docker 29.7.2 and kernel `7.0.12-linuxkit`. It uses owner-provisioned private
socket paths, encrypted authenticated requests and a fixed recipient-bound
response mailbox. Datagram source addresses do not establish identity or
select the response destination.

The run reused the pinned native daemon and production gateway from the
[Linux FIFO fixture](fifo.md#linux-native-delivery-bounded-reads-and-recovery),
production source `159afc40c4e7607fe871443002cd39596f3fb41e`, and the
[Unix-stream integration fixture](linux-unix-stream-testing.md). Its integration
Go/module source digest was
`b612d4afe18bb8115a6a51fc508c3effe8e9fb71a8e106389e6c20a9150e6657`;
the integration executable SHA256 was
`c87ff175f8bb9c8bd4a99cca933e9f2271fa00b61f86c8f9187ffdd08bafd5ce`.
Artifacts were digest-verified before execution. Host Go 1.27.1 built Linux
ARM64 executables with CGO disabled. The existing container used no network,
a read-only root/artifact mount and an automatically removed anonymous local
filesystem volume for journal state.

`-test.run '^TestUnixgram' -test.v -test.count=1 -test.timeout=180s`
completed with terminal exit 0. All five native controls passed:

- Oversize, tampered, signed-only and incomplete encrypted input cannot admit
  jobs; matched complete input delivers one independently observed whole native
  message (0.50s).
- Real configuration/capabilities CLI and the shared send/read
  conformance control (0.32s).
- Fragment reordering/duplication, deliberate response loss, source reconnect,
  stable accepted replay with one attempt and exactly one whole native message;
  complete limit-one history and empty caught-up resume (0.28s).
- Complete six-message history, whole live watch metadata and joined shutdown
  with lease/owned-ingress cleanup and client mailbox preservation (0.05s).
- Authenticated quiet-watch heartbeats, bounded lease expiry and cleanup (15.03s).

Three actual Linux service controls passed separately with terminal exit 0:
mailbox permission-change and distinct replacement-inode refusal, ingress
cleanup with mailbox preservation, Linux path canonicalization and shared-parent
refusal. The service executable SHA256 was
`6887dd9518e93e48d4cfdbcc93aab4edac954dbb8dcccb3c2c34b1efbfde6f32`.
The original replacement test failed after deleting the old socket: filesystem
inode-number reuse made that an unreliable distinct-identity control. It now
retains the old inode at another path and explicitly asserts distinct identity
before testing refusal. This changes only the test. Darwin service controls
passed under race detection in 1.560s; service vet passed.

Raw evidence is `/tmp/airc-linux-native-159afc4/unixgram-native.log` and
`/tmp/airc-linux-native-159afc4/unixgram-service-distinct.log`. The original
failed service run remains in `unixgram-service.log`; the integration artifact
manifest is `unix-stream-provenance.json` in the same directory.

This qualifies the existing Linux private-filesystem Unix-datagram mode on this
fixture. It does not establish per-packet kernel credentials or isolation
between hostile processes sharing the owner account. Device/inode checks do
not detect deletion followed by reuse of the same inode number; recipient-bound
encryption remains required. Linux runs have no race instrumentation and do
not establish power-loss durability. The unchanged-source full Darwin
normal/race checkpoint passed; see
[testing.md](testing.md#linux-portability-integration-checkpoint). Final
acceptance after future executable changes remains in the roadmap.
