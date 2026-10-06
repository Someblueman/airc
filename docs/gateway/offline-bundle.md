# Offline signed-message bundle import

The signed-message importer is implemented and qualified through the real CLI
on Darwin and Linux. This is a send-only offline carrier with a local intake/
receipt report; conversation reads require a later return path. Real removable-
media verification remains in [ROADMAP.md](ROADMAP.md#add-request-and-mailbox-connections).

A version-1 JSON object contains only these fields:

```json
{"version":1,"envelopes":["COMPACT_SIGNED_ENVELOPE"]}
```

There must be 1–32 envelopes. Each is an opaque string of at most the existing
32 KiB signed-envelope limit. The entire UTF-8 document is at most 1 MiB,
including JSON encoding and whitespace. These limits apply independently:
32 envelopes at their individual maximum exceed the whole-file limit.
Duplicate/unknown/case-aliased/null fields, non-string entries, invalid UTF-8,
trailing documents, unsupported versions and empty lists/tokens are refused.
There are no attachment, executable or archive fields.

The container is unsigned and grants no authority. Each compact signed envelope
separately establishes the existing configured identity, audience, target,
operation and freshness before journal admission. Format parsing alone does not
verify signatures, reject durable replays or establish AIRC acceptance.

`ReadOfflineBundle` currently supports Darwin and Linux. It requires an absolute
normalized path, checks a regular file before opening, opens read-only with
final-component no-follow/nonblocking/close-on-exec flags, then verifies the
opened file still has the same identity and is regular and bounded. Reads are
limited to 1 MiB + 1, so growth beyond the limit is refused. Parent-path aliases
are allowed; this is not a private-directory ownership gate. Input ownership
and executable mode do not authenticate a sender. Loading never renames,
deletes, acknowledges or executes the source; the byte buffer is cleared after
parsing. Other platforms have an explicit unsupported file-loader result.

## Format and file-boundary evidence

Focused Darwin tests passed under race detection in 1.675s. They exercise
independent entry/token/whole-file limits, exact whole-file limit and limit + 1,
strict malformed-input refusals, executable-mode opaque shell text, unchanged
source content, and symlink/FIFO/directory/oversize/path refusals.
Adapter vet and staticcheck and current command builds passed. The adapter
tests also cross-compiled for Windows, retaining the unsupported file-loader
result; no Windows runtime qualification is claimed.

The same three controls passed on actual Ubuntu 24.04 ARM64, Docker 29.7.2 and
kernel `7.0.12-linuxkit`, terminal exit 0. The existing container used no network,
a read-only root/artifact mount and an automatically removed anonymous local
filesystem volume. Go 1.27.1 cross-compiled the adapter tests with CGO disabled;
these Linux controls have no race instrumentation. Their executable SHA256 was
`910f9570c914fadc21323ec2d651ee4ddef8ff6cf96b7cf944b7088f4eb33ba5`.
The Go/module source digest (sorted tracked/untracked paths and content, each
NUL-separated) was
`2250a1f285c9cc5f2004cb3af05ce4e8de51dfe90d713db50606faee47878268`.
Raw evidence and manifest are
`/tmp/airc-linux-native-159afc4/offline-bundle-format.log` and
`/tmp/airc-linux-native-159afc4/offline-bundle-format-provenance.json`.

Those initial controls establish the format and file boundary only. No real
removable medium was exercised. The preceding
full normal/race acceptance in [testing.md](testing.md#linux-portability-integration-checkpoint)
applies to its recorded source; the new importer will require fresh integration
acceptance after implementation.


## Import command and durable outcomes

Configure the sole enabled ingress as:

```json
{"id":"offline","protocol":"offline_bundle","enabled":true,"operations":["send"]}
```

It has no address, TLS or origins. Existing bindings and principals still own
AIRC account identity and target permissions; provision each sender's signing
public key in that principal's existing `signing_keys`. The gateway does not
create an account or infer identity from a filename or filesystem owner.
Envelopes use the existing [signed-message v1 profile](signed-message-v1.md).
Bearer-only, encrypted-only, read and other mutation entries are refused.

```sh
airc-gateway import-bundle --file CONFIG --input /absolute/path/bundle.json
```

Extra arguments and missing flags fail. Ordinary `serve` refuses an enabled
offline ingress before journal creation. Capabilities advertise its explicit
send operation; enabled file import is unavailable outside Darwin/Linux.

All entries authenticate before opening the journal. Audience, signature,
revocation, configured target/operation and signed validity are checked.
Duplicate principal/binding/request IDs within one bundle refuse the whole
bundle before intake. An invalid later entry cannot admit an earlier valid
entry. There is no expiry extension for offline transport: signature lifetime
remains bounded by configured retry lifetime, and journal admission requires an
unexpired signed mutation.

Intake is durable per entry, not an atomic batch. Stable principal/binding/
request identity and the existing journal fingerprint coalesce prior work.
Existing requests return `imported:false` and `replay_rejected:true`; changed
intent under the same identity fails rather than replacing the existing job.
Replays can resume delivery of the original pending job, but cannot create a
second job. Original accepted receipts remain available in the replay report.

The version-1 JSON report contains `entries` in input order. Each entry names
its verified `principal`/`binding`, `imported`/`replay_rejected` flags and the
existing full `result` schema, including request ID, durability, native receipt
facts, state and attempt counts. `imported:true` means durable intake; only a
native accepted result establishes AIRC acceptance. Delivery gets a bounded
five-second grace, then workers join before journal release. Pending jobs remain
durable for a later import. On interruption, results are the latest successfully
read snapshots, not a guarantee of final state.

The command exits nonzero on any rejected replay or error. It still prints the
durable admitted prefix if a later entry fails capacity/storage checks. A mixed
replay/new batch can reject the replay and admit its new entries; inspect every
entry rather than treating the exit code as batch rollback. Invalid preflight
input emits no intake report and creates no journal. The source remains intact.
SIGINT/SIGTERM join workers and exit with failure; hard kills leave committed
intent for recovery. This does not establish power-loss durability.

## Native importer acceptance

Darwin affected race controls passed: initial native acceptance/replay and
preflight controls in 13.448s, partial-capacity/mixed-replay and three pending
termination/recovery controls in 9.316s, then the strengthened revoked-sender/
exact-body-bound preflight control in 8.082s. Full affected CLI/configuration/
service race packages passed (2.070s/1.664s/5.600s), as did vet, staticcheck,
command builds and diff checks. Windows gateway cross-build passed; offline
file import remains explicitly unavailable there.

Actual Linux native controls completed with terminal exit 0 on the same isolated
Ubuntu/LinuxKit fixture used above. The native baseline/archive was rechecked
and reused from the [Linux FIFO fixture](fifo.md#linux-native-delivery-bounded-reads-and-recovery).
All four controls passed:

- Two complete signed messages with independently observed author, target,
  multiline Unicode body and native receipt; fresh-process replay rejection,
  unchanged receipt/one attempt and exactly two native records (0.52s).
- Durable prefix reporting at capacity, followed by mixed replay/new intake
  with exactly two native messages and one attempt each (0.25s).
- SIGKILL/SIGINT/SIGTERM while committed work waits behind a held backend,
  bounded child exit, released journal, stable upstream identity and fresh
  import recovery of exactly one whole native message (1.70s combined).
- Invalid signature/expiry/future time/audience/target, encrypted/read entries,
  duplicate ID, oversized body and revoked sender refused before journal
  creation; matched 4,096-byte whole-body native control passed (0.13s).

Go/module source digest (sorted tracked/untracked paths and content, each
NUL-separated):
`1ad4e06025514dd74176e0d1d47442d36d3f9ab8f36edf101430b934c4078018`.
Actual gateway SHA256:
`f35a97a01c44a97f7f9f68c81a9a4c83d49a4e8274ae847de1f8ae3238c8ccbd`.
Integration executable SHA256:
`1f9c92f5b9b454603d7940de074e78143b4a7f19f11168cbf64d53c81d1ee781`.
Raw evidence and manifest are
`/tmp/airc-linux-native-159afc4/offline-bundle-native.log` and
`/tmp/airc-linux-native-159afc4/offline-bundle-native-provenance.json`.
Linux executables have no race instrumentation. Full final-source integration
acceptance remains required; physical removable-media evidence remains absent.
On 2026-10-06, `diskutil list external physical` completed successfully with
no external physical disk listed. A real removable device and an authorized
mounted test path are required to qualify that carrier.
