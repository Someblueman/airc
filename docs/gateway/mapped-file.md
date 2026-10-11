# Memory-mapped-file message queue

The `mapped_file` listener carries [plain frames](local-authors.md) in
[fragments](author-network-carriers.md#fragments) through pre-provisioned
ordinary files on Darwin ARM64 and Linux ARM64, and answers in its `reply_to`
queue directory. Each request names its author, or runs as the listener's
`bound_author`. A file owner, process UID or directory name never selects an
AIRC account. The author model is compiled but has not yet been run on ARM64.

The ordinary `airc-gateway serve --file CONFIG` command owns this listener:

```json
{
  "id": "mapped",
  "protocol": "mapped_file",
  "address": "/absolute/private/queues/ingress",
  "reply_to": "/absolute/private/queues/replies",
  "enabled": true,
  "operations": ["send", "receipt.get", "history.read", "watch"]
}
```

`reply_to` is the response queue directory; requests cannot choose response
paths. History and watch also require the ordinary cursor key. TLS, origins
and `shared_memory` listener options do not apply to this carrier.

## Provisioning and ownership

Each queue address is a canonical absolute directory, at most 4096 bytes,
with mode `0700` and trusted ancestors. Provision two regular, single-link
files owned by the gateway's effective UID, each with mode exactly `0600`:

- `queue.dat`: exactly 16,384 bytes, initialized with the AGQ1 empty slot.
- `transaction.lock`: the existing cooperative transaction lock file.

Provision the ingress and response directories before starting peers.
The gateway opens response queues first; it never creates, truncates,
initializes, replaces or unlinks these files. Queue directories must be
distinct and must not contain or alias state, credentials, another queue or
another configured local resource. Configuration checks include resolved
ancestor aliases and file identities, including case-insensitive ancestor
identity when a referenced descendant does not exist yet. Runtime opens reject
symlinks, wrong owners, permissions, sizes and link counts, retaining descriptors for identity
and metadata checks before each transaction.

Supported filesystems are APFS/HFS on Darwin and ext4/XFS/Btrfs on Linux.
Other filesystem types, including network, FUSE and tmpfs mounts, are refused.
The actual native qualification below exercises APFS and ext4; it does not
claim separate runtime testing of every allowed filesystem.

All peers must cooperate: never resize, replace or unlink a queue directory,
its data file or its lock file while any participant has it open. Descriptor
checks detect replacement before access, but cannot prevent a same-UID process
from truncating a mapping concurrently or otherwise denying service. The
consumer holds an exclusive directory lock for its lifetime. Producers and
consumers serialize each transaction with the same `transaction.lock` flock.

## Slot format, recovery and delivery

The file uses the existing [AGQ1 slot layout and atomic state transitions](shared-memory.md#slot-format-and-recovery):
a 16-byte header, one whole fragment of at most 1000 bytes, and unused padding.
The provisioner zeroes the file and writes `AGQ1` at bytes 4–7 before publishing
it to peers. The supported targets are little-endian. Producers and consumers
must use aligned, lock-free 32-bit state atomics with the documented ordering.

A killed process releases its flock. An interrupted write or consumer reset
leaves state 1, which the next participant discards. A published state-2 record
remains whole after process death, including when no other mapping survives.
Invalid static headers, states and lengths fail without automatic repair.
Full slots apply bounded backpressure; cancellation and close wake waiters,
and shutdown joins mapped access before unmapping.

This is process-crash recovery, **not reboot or power-loss durability**. The
owner must initialize the queue file before peers start after each boot.
There is no `msync`/`fsync` delivery guarantee. Producers retain the request
and stable request ID until a durable gateway receipt, then use the
existing journal and replay contract. A successful slot write or dequeue is
not AIRC admission or delivery.

The shared [packet framing](udp.md) supplies bounded whole requests and
complete page/cursor replies. The mapped file does not introduce a separate
journal format.

## Native qualification

Dated records follow. Where they mention principals, bindings, signed or
encrypted envelopes, signing keys or return routes, they describe the
[removed identity model](removed-identity.md), which no longer exists.

On 2026-10-09, the real `serve` process and an independent native AIRC observer
verified mapped-file ingress on Darwin ARM64/APFS and Linux ARM64/ext4.
Protected Unicode and fragmented sends preserved author, room and body.
Complete one-record history pages reached cursor exhaustion. Lost replies and
gateway restart replay retained the same accepted intent with one native send.
Invalid credentials, signed-only traffic and a different realm were refused
before intake. A held SQLite writer transaction allowed the gateway to be
killed after dequeuing all fragments but before journal admission; retrying the
same request then produced exactly one native message.

Queue checks covered the whole-fragment size bound, full-slot backpressure
without overwrite, exclusive consumer ownership, cancellation, malformed slots
without repair, fixed response routes and joined shutdown with a blocked
multi-fragment response. A separate child was the only process mapping the
file during each crash test. The parent verified actual transaction-lock
contention, killed and reaped the child, then opened the unchanged file to
verify READY retention or interrupted-write/reset recovery and reuse.

Both mapped-file and refactored shared-memory native families passed together
under Darwin race detection in 37.553 seconds, with 42 test/subtest pass events,
no skips and empty stderr. The log is
`/tmp/airc-mapped-file-qualified-darwin-race.jsonl`. Focused adapter race and config/CLI race
checks also passed. The unsupported Windows AMD64 CLI cross-compiled; this is
not Windows runtime qualification.

Linux used fresh host-cross-compiled cgo-disabled binaries in the cached Ubuntu
ARM64 image
`sha256:33ceb71981b602c1a7443a53469e4dba065f7503eab3078a2d7a57a2ab987517`,
with no network, a read-only root and artifact mount, all capabilities dropped,
no-new-privileges and a disposable ext4 volume for test state. All 42 native
family cases passed with no skips in 6.027 seconds including container startup;
11 focused adapter test/subtest cases also passed. Commands, binary hashes,
source digest and terminal results are retained in
`/tmp/airc-mapped-file-qualified-linux-native-20261009/provenance.json` beside the logs.
No image or package was obtained. Linux race execution is not claimed.

The same source passed the full Darwin normal and race suites, build, formatting,
vet and staticcheck gates. Both suites passed 4608 test cases with no failures.
The exact source snapshot, terminal results and the earlier intermittent QUIC
handshake failure are recorded in the
[mapped-file acceptance checkpoint](testing.md#mapped-file-acceptance-checkpoint).
