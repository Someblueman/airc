# System V queue gateway boundary

The System V listener is implemented on Darwin ARM64 and Linux ARM64 through
public configuration and Run, using schema 18 journal ownership and existing
protected packet framing. Actual startup/refusal and joined restart tests pass
on both platforms. Full normal/race acceptance and static gates passed on the
frozen activation source; see [the exact checkpoint](testing.md#system-v-service-activation-acceptance).
Actual native Unicode delivery, protected whole cursor reads and stable-intent
replay now pass on Darwin and Linux ARM64, as recorded below. Pressure/refusal
and service-process crash/replay qualification remain in [ROADMAP.md](ROADMAP.md). Kernel and packet
evidence is recorded separately in [ipc-platform-evidence.md](ipc-platform-evidence.md).

## Configured service boundary

Use the explicit `system_v_queue` listener/return-route protocol. Listener
`address` and route `destination` are canonical decimal strings for positive
32-bit System V keys, from `1` through `2147483647`; reject signs, leading zeros,
spaces, hexadecimal spellings and `IPC_PRIVATE` key zero. They are neither
filesystem paths nor network endpoints. Do not substitute POSIX queues or
another carrier on unsupported platforms. The service uses existing kernel
capacity without changing host IPC limits; opening requires mode 0600, current
owner and creator UID, and bounded capacity sufficient for a complete fragment.

An enabled listener requires existing encrypted detached ingress recipient
keys, configured principals and response signing credentials. Require exactly
one fixed protected return route for each configured principal, bound to that
listener and principal's existing binding/response recipient key. Every response
key must differ from every ingress key. Keys are selected from configuration;
received fragments cannot supply a response destination. TLS certificates,
browser origins and POSIX queue depth settings do not apply to this carrier.

Each kernel message carries one complete existing AGD1 fragment, from 1 through
1,000 bytes with System V type 1. Never truncate an oversized record or enable
`MSG_NOERROR`. Assemble only within the existing request, fragment-count, active
request and memory limits. Treat the ingress key as one shared transport source,
never a principal. Queue metadata, message type, fragment ID and enqueue/receive
success cannot establish authentication or AIRC acceptance. Authenticate the
assembled encrypted request with the existing verifier and admit it to the
shared durable journal under the mapped existing AIRC identity before native
submission. Preserve exact whole-body text and existing stable request IDs.

Responses must use the existing signed/encrypted whole-result framing to the
principal's fixed provisioned queue. Bounded read pages must retain their full
items and explicit cursor/page boundary; queue pressure may cause an uncertain
reply outcome, not partial-page success. Existing retry/status behavior resolves
that uncertainty without inventing a new AIRC operation or weakening grants.

## Startup and recovery boundary

Retain the store's exclusive process lock throughout queue ownership. Open and
verify externally owned response queues before creating ingress. Their opened
handles must never acquire removal ownership. Start no listener or readiness
notification before all credentials, routes and kernel handles are verified.
On any startup failure, close all acquired handles and remove only a newly
owned, still-matching ingress queue.

Load the complete ownership record for the listener and pin its key/platform.
Create ingress exclusively. Only successful exclusive creation may insert a
new record or compare-and-replace its complete previous record. A known live
queue may resume only through the existing exact identity and current
boot/namespace checks; an unknown existing key, changed lifecycle or stale epoch
must be refused without removal. A crash between exclusive creation and durable
registration can leave an unknown queue requiring explicit owner cleanup;
startup must not adopt or delete it merely because its numeric key matches.

Successful dequeue is destructive and can precede durable admission. A producer
must preserve the same authenticated intent/request ID when retrying an uncertain
outcome. A crash after admission retains the job's existing native-delivery
state and replay evidence; restarting must not recreate its native attempt.
Qualification must exercise actual process death before admission and after
admission, journal reopening, whole native body/author/target observation and
protected whole read replies. The kernel killed-owner prerequisite alone does
not prove those service guarantees.

Shutdown cancels and joins listener/packet users before releasing credentials
and journal ownership. Queue removal must verify the retained lifecycle; never
remove a replacement or an external response queue. Recovery and shutdown tests
must also cover failed registration, unknown/stale ownership records, full
queues, permission changes, cancellation, and unsupported-platform refusal.

## Native send, protected reads and replay evidence

`TestSystemVQueueNativeUnicodeCursorAndReplay` starts an actual gateway process
and isolated pinned AIRC server, with a separate native observer. It verifies
exact author, target and Unicode body for two sends, including a 2,800-byte
Unicode fragment body delivered through reordered whole AGD1 queue messages.
It discards an actual response and retries the same signed intent, requiring
accepted durable receipts with one native attempt and exactly two native
messages. Protected history pages have a one-item limit, explicit nonempty
cursors and an empty final `ok` page; concatenated whole messages must exactly
match an independent native history read. Existing shutdown checks join the
fixture gateway process.

Darwin ARM64 normal/race tests passed in 21.789/39.142 seconds. The Linux ARM64
cgo-disabled test binary passed in 0.61 seconds in the existing cached Ubuntu
fixture, with network disabled, read-only artifacts/root, no capabilities and
automatically removed local temporary volume. The supplied native server digest
was checked before launch and by the fixture against recorded baseline
`96f16c3bbb8a3faac2e69684f591af63ceb58450`; the current gateway artifact was
also checksum-verified. Provenance and actual native output:
`/tmp/airc-system-v-native-93a280d/provenance.json` and `native.log`.
Linux race execution is not claimed.

Build, vet, staticcheck, affected formatting and diff checks passed with terminal
exit 0. This test-only checkpoint follows the full activation acceptance; its
new integration tests are not included in that frozen full-run hash. Fresh final
full acceptance remains on the roadmap. Queue pressure, authentication/size
refusals, ownership replacement and killed-service crash boundaries are still
required before complete mode qualification.
