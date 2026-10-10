# Fresh OSCORE context provisioning

The owner can now generate a fresh context and initialize its matching
[durable sequence/replay record](oscore-state.md):

```sh
airc-gateway oscore init --file /absolute/gateway.json \
  --principal device --context-file /absolute/private/device-oscore.json
```

The configuration must identify an existing authorized principal and binding,
and an existing private state directory. The context file must be new and its
parent must already be owner-private. The command starts no listener and contacts
no AIRC backend. Success is printed only after private file creation/sync,
durable state creation, and journal close. Secret material and its digest are
never printed. There is no import, reset, reseed, or overwrite option.

Each invocation generates an independent 256-bit master secret, 128-bit master
salt, and 128-bit context ID with the system cryptographic random source. The
version-1 profile fixes AES-CCM-16-64-128 (COSE algorithm 10), HKDF-SHA-256
(algorithm -10), server sender ID `01`, peer sender ID `02`, replay width 64,
and sender reservation frequency 8. Appendix B.1.2 remains enabled and B.2 is
disabled. Profile changes require deliberate versioning and key rotation; the
canonical server profile defines the persisted digest.

The private JSON file contains version, owner realm/principal/binding, and
lowercase hexadecimal secret, salt, and context ID. Its reader uses bounded
strict JSON and the existing native private credential checks; unknown,
duplicate, case-aliased, null, malformed, oversized, symlinked, and publicly
readable files are refused. The cryptographic digest excludes owner labels and
JSON formatting. Changing a file's owner mapping cannot reset or rebind the
matching journal record.

File creation precedes journal provisioning. If the journal write fails, the
fresh private file is retained for inspection and the command reports failure;
it must not be used without matching journal state. This includes durable
revocation, which remains authoritative after reopening the original active
configuration. Failed provisioning cannot reset an existing record. A later
attempt must generate fresh material in a new file rather than import or reseed
that retained context. Provisioned files must remain paired with their current
journal; restoring an older journal backup with the same keys is unsafe.

The native interface emits caller-owned secret configuration bytes for either
role. The peer role reverses sender/recipient IDs and derives matching directional
keys and a common IV. Peer applications must persist their own sender and replay
state; a peer configuration file alone is not a safe restart mechanism.

## Verification

Private credential tests cover reload, fresh-key separation, canonical digest,
peer orientation, malformed JSON/material, file permissions, symlink refusal,
and overwrite refusal. The real CLI test verifies the matching initial journal
record, preservation of an existing reservation on attempted reprovisioning,
and durable revocation without creating state for the rejected context.

After the [native build](../../native/oscore/README.md), validate generated
profiles against the pinned library with:

```sh
AIRC_OSCORE_CONTEXT_CHECK=/absolute/build/oscore-context-check \
  go test -race ./internal/gateway/auth \
  -run '^TestOSCOREContextNativeConfiguration$' -count=2 -timeout=90s
```

The owned C executable checks actual native context creation, directional key
agreement, distinct sender keys, common IV, algorithm IDs, reservation frequency,
replay width, and Appendix B.1.2 setting under ASan/UBSan. This test is explicitly
skipped when the native executable is not supplied. It is key-configuration
acceptance, not packet exchange, initial Echo acceptance, gateway delivery, or
native replay rejection after gateway restart. The listener and those acceptance
requirements remain on the roadmap.

On Darwin, 2026-10-06, the explicit native configuration control passed twice
under the Go race detector with the ASan/UBSan native executable. All gateway
packages passed under race detection with that control enabled; CLI race tests
passed (2.291 seconds). Command builds, repository vet and staticcheck, native
build/CTest, formatting, and diff checks passed. Full-module final normal/race
acceptance remains unfinished.
