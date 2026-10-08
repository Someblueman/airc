# System V queue gateway boundary

The private queue, packet routing and exact-lifecycle recovery layers are
implemented on Darwin ARM64 and Linux ARM64. Schema 18 ownership records have
passed full normal/race acceptance and required static gates. A configured System V listener is not yet available;
authenticated native delivery, bounded reads and service crash/replay
qualification remain in [ROADMAP.md](ROADMAP.md). See
[the kernel and packet evidence](ipc-platform-evidence.md) for verified claims.

## Service implementation target

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

## Startup and recovery target

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
