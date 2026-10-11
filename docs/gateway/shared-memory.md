# POSIX shared-memory message queue

The `shared_memory` listener carries [plain frames](local-authors.md) in
[fragments](author-network-carriers.md#fragments) through named POSIX shared
memory on Darwin ARM64 and Linux ARM64, and answers on its `reply_to` queue
name. Each request names its author, or runs as the listener's
`bound_author`. Unix ownership restricts access to the local carrier; a queue
name or process UID never selects an AIRC account. The author model is
compiled but has not yet been run on ARM64.

Configure one pre-provisioned ingress name and a private lock-directory root:

```json
{
  "id": "shared",
  "protocol": "shared_memory",
  "address": "/airc-ingress",
  "enabled": true,
  "shared_memory": {"lock_directory": "/absolute/private/queues"},
  "reply_to": "/airc-replies",
  "operations": ["send", "receipt.get", "history.read", "watch"]
}
```

The normal `airc-gateway serve --file CONFIG` command owns this listener.
`reply_to` is a distinct queue name; requests cannot choose a destination.
History and watch also need the ordinary cursor key. TLS and origins do not
apply to this carrier.

Each name is slash plus an ASCII component, at most 31 bytes including slash.
The first component byte is alphanumeric; later bytes may also be `_`, `-` or
`.`. Every object must belong to the gateway's effective UID, have mode `0600`
and be exactly 16,384 bytes. Darwin rounds native shared-memory sizes to a
memory page, so the mapped size and the message-size bound are separate.

For each object `/NAME`, provision `lock_directory/NAME/` as a canonical private
`0700` directory and its existing `transaction.lock` as a regular, single-link
`0600` file owned by the same UID. Ancestors must satisfy the gateway's private
directory checks. One object name must use one lock directory across all peers.
The consumer holds an exclusive directory lock for its lifetime; writers take
only the transaction lock. The gateway opens response queues first and never
creates, resizes or unlinks objects or lock files.

All peers must cooperate with this lifetime rule: provision, replace, resize
or remove resources only after every participant closes its handles. The
gateway retains open descriptors and checks their ownership, permissions and
size before queue access. Darwin does not expose a usable shared-memory object
identity in `fstat`, so these checks do not establish protection against a
same-UID process that replaces a live name. A malicious same-UID peer can also
deny service by modifying or truncating shared memory, or read requests and
tokens in it.

## Slot format and recovery

One 16 KiB mapping contains one bounded whole fragment. The provisioner zeroes
the mapping and writes `AGQ1` at bytes 4 through 7 before publishing the name to
participants. The supported targets are little-endian; all participants must
use aligned, lock-free 32-bit state atomics and the same transaction lock.

| Offset | Bytes | Meaning |
| --- | --- | --- |
| 0 | 4 | Atomic state: 0 empty, 1 incomplete, 2 ready |
| 4 | 4 | Immutable magic/version `AGQ1` |
| 8 | 4 | Little-endian fragment length; 1–1000 when ready, 0 when empty |
| 12 | 4 | Immutable reserved zero bytes |
| 16 | 1000 | Fragment storage; only the declared length is a message |
| 1016 | 15368 | Unused mapping space |

All queue access takes the transaction lock. A writer waits while the slot is
ready, publishes state 1 before changing length or body, and publishes state 2
only after the complete fragment is present. A consumer copies a valid ready
record into local memory, publishes state 1 before clearing its length, then
publishes state 0. Go uses sequentially consistent atomic state operations.
Native producers must preserve the same ordering.

The kernel releases a process's transaction lock after death. A later owner
discards state 1 and can use the slot again. State 2 remains a whole record;
death after copying but before emptying it can cause replay. Invalid magic,
reserved bytes, state or ready length fail visibly instead of being repaired
as a different protocol. Full slots and contended locks are polled with
cancellation and deadlines; closing wakes waiters before unmapping.

The queue is volatile, including across reboot. Removing a fragment from the
slot is not durable AIRC admission. A producer retains its request and stable
request ID until it receives a durable receipt, and retries
that same intent after an uncertain result. Existing journal admission and
replay handling provide the delivery guarantee after intake.

Fragment framing, request and response size bounds, source admission,
whole page/cursor responses and delivery policy are shared with
[UDP packet framing](udp.md). A transport write or a ready slot is never
reported as native AIRC acceptance.

## Native qualification

Dated records follow. Where they mention principals, bindings, signed or
encrypted envelopes, signing keys or return routes, they describe the
[removed identity model](removed-identity.md), which no longer exists.

On 2026-10-09, Darwin ARM64 and Linux ARM64 passed native qualification using
the real `serve` process, independently opened POSIX shared-memory objects and
a separate native AIRC observer. The tests verified Unicode and fragmented
sends under the configured author and room, lost-response replay with one send
attempt, complete one-record cursor pages through exhaustion, and refusal of
invalid credentials, signed-only traffic and a different realm before intake.

Native queue checks covered the 1000-byte whole-message bound, full-slot
deadlines without overwriting the pending message, exclusive consumer ownership
and cancellation. Invalid magic, reserved bytes, states and lengths were
rejected without modifying the object. Separate child processes were killed
while holding the transaction lock: partial writes and interrupted consumer
resets recovered, while a ready record remained whole. The parent verified
actual lock contention before each kill, reaped the child and then used the
same queue again.

Gateway restart checks retained an accepted outcome without another native
send. A separate test held a real SQLite writer transaction, observed every
request fragment leave the single-slot ingress, killed the gateway before
journal admission, and confirmed no job, response or native message existed.
Retrying the same signed request after restart produced exactly one accepted
native message. Shutdown also joined a response writer blocked by an unread
multi-fragment history page, and released the consumer directory lock.

The Darwin native family passed under race detection in 22.488 seconds; the
subsequently added lost-intake case and final shutdown helper passed together
under race detection in 10.401 seconds. Logs are
`/tmp/airc-shared-memory-darwin-race.log` and
`/tmp/airc-shared-memory-darwin-final-boundaries.log`. Config/CLI race tests,
relevant vet, staticcheck and diff checks passed. The Darwin provisioning
helper also passed strict C11 warnings, ASan and UBSan in isolation;
LeakSanitizer is unavailable on this host and was disabled explicitly.

The Linux run used host-cross-compiled, cgo-disabled binaries in cached Ubuntu
ARM64 image
`sha256:33ceb71981b602c1a7443a53469e4dba065f7503eab3078a2d7a57a2ab987517`.
It had no network, a read-only root and artifacts, no capabilities,
no-new-privileges and disposable local test state. The native family passed in
5.014 seconds including container startup; the final lost-intake and shutdown
cases passed in 1.297 seconds. Exact commands, binary hashes and logs are in
`/tmp/airc-shared-memory-linux-native-20261009/provenance.json` and
`final-boundaries-provenance.json` beside it. No image or package was obtained.
Linux race execution is not claimed. The unsupported Windows CLI still
cross-compiles, but this does not establish Windows runtime support.

These tests establish the stated process-crash and cooperative-peer behavior;
they do not establish reboot durability or protection from a hostile same-UID
process. [Full repository acceptance](testing.md#shared-memory-acceptance-checkpoint)
also passed on the unchanged source, including normal and race suites, build,
formatting, vet and staticcheck.
