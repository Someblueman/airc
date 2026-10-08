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

## Actual refusal boundaries

`TestSystemVQueueUnverifiedInputHasNoResponseOrNativeIntake` tests signed-only
unencrypted input, tampered encryption, wrong realm, denied target, destination
override, a 1,001-byte producer-API record, incomplete fragments and an unknown
signing key. Each case produces no protected response and no durable job; an
independent native read confirms no messages were posted. A subsequent valid
encrypted capabilities call succeeds through the fixed protected mailbox.
The producer-API size case covers explicit API refusal, not a raw oversized
kernel message injected outside that API.

Darwin ARM64 normal/race tests passed in 5.513/8.393 seconds. Actual Linux ARM64
qualification passed all eight cases in 1.39 seconds using the checksum-verified
existing native server and unchanged gateway artifact in the cached isolated
Ubuntu fixture. Output and provenance are in
`/tmp/airc-system-v-native-93a280d/boundary.log` and `boundary-provenance.json`.
Linux race execution is not claimed. Build, vet, staticcheck, affected formatting
and diff checks passed with terminal exit 0. No production code changed in this
checkpoint. Pressure, replacement, whole watch shutdown and killed-service
recovery remain independently required.

## Whole protected history, live watch and shutdown

`TestSystemVQueueWholeHistoryLiveWatchAndJoinedShutdown` compares
six complete large native messages against a protected retained-history reply
and the first live-watch page. A subsequent native message must appear as the
next whole watch page with exact metadata. SIGINT with an incomplete send joins
the gateway and native observer lease, removes only owned ingress, preserves
the external response lifecycle and leaves no admitted partial job. Exclusive
recreation confirms ingress removal.

Concurrent native queue traffic exposed Darwin mode `01600`: XNU sets
[`MSG_LOCKED` (`01000`)](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/sys/msg.h)
during queue I/O. The Darwin permission check now masks `0777`, retaining exact
`0600` access permissions and all other ownership/lifecycle checks. Linux is
unchanged. Actual Darwin normal (two runs)/race watch checks passed in
6.943/10.065 seconds; Linux ARM64 passed in 0.28 seconds using checksum-verified
existing native and gateway artifacts. Linux provenance/output:
`/tmp/airc-system-v-native-93a280d/watch-provenance.json` and `watch.log`.
Linux race execution is not claimed.

[Full normal/race and static acceptance](testing.md#system-v-native-watch-and-darwin-permission-acceptance)
passed on the unchanged source including this test and Darwin fix. Pressure,
replacement and killed-service recovery remain required for complete qualification.

## Linux input and response pressure

`TestSystemVQueueInputAndResponsePressureRemainBounded` stops the actual gateway
and confirms kernel stopped state, fills ingress with whole 1,000-byte records
until the caller's 150 ms deadline, resumes it and verifies a protected valid
call. It then fills the external response queue, submits another valid request
and waits for exact author/target/body delivery on an independent native observer
before waiting beyond the production three-second write deadline. Draining preserves
every whole filler; no late protected response appears. A subsequent call
succeeds and the protected receipt confirms the observed message with one native
attempt. Invalid ingress fillers created no mutation jobs. Queue capacity is discovered
through bounded real writes, with a 66-record ceiling matching the configured
maximum byte capacity.

Actual Linux ARM64 qualification with the processing witness passed in 4.38 seconds using the existing
checksum-verified native server and unchanged Linux gateway artifact in the
cached isolated Ubuntu fixture. Output and provenance:
`/tmp/airc-system-v-native-93a280d/pressure-replacement.log` and
`pressure-replacement-provenance.json`. The earlier pressure test lacked a
processing witness; this corrected run supersedes its response-expiry claim.
Linux race execution and Darwin service-pressure qualification are not claimed.
This test-only increment is subsequent to the recorded full acceptance source.
Build, Linux-targeted vet/staticcheck, formatting and diff checks passed with
terminal exit 0. Ownership replacement and killed-service recovery remain open.

## Linux service replacement refusal

`TestSystemVQueueReplacementIsPreservedAndNeverAdopted` removes the actual
ingress kernel identifier and creates a distinct lifecycle under the same key.
The old producer refuses writes and the gateway joins with an error. Shutdown
preserves the replacement. Restart with the retained original journal record
refuses before readiness and preserves both replacement and external response
queue. The journal contains no jobs and independent native history is empty.

Actual Linux ARM64 qualification passed in 0.05 seconds using the same
checksum-verified native/gateway fixtures; output and provenance are in
`/tmp/airc-system-v-native-93a280d/pressure-replacement.log` and
`pressure-replacement-provenance.json`. Linux-targeted vet/staticcheck,
compilation, formatting and diff checks passed with terminal exit 0. No Darwin
service-replacement or Linux race execution is claimed. This test increment
follows the recorded full acceptance source; killed-service recovery remains open.

## Linux uncertain dequeue and killed-owner retry

`TestSystemVQueueProducerRetryAfterUncertainDequeue` holds a real SQLite
`BEGIN IMMEDIATE` writer while an authenticated request arrives. It stops and
verifies the gateway's kernel state, witnesses queued bytes through
`/proc/sysvipc/msg`, resumes and witnesses complete destructive dequeue. SIGKILL
then joins the process; the still-held journal has no jobs and the response
queue is empty. After releasing the writer, restart must retain the complete
original ingress lifecycle. Retrying the same signed intent yields accepted
receipt with one native attempt and exactly one independently observed native
message with the correct author, target and whole Unicode body.

Actual Linux ARM64 qualification passed in 0.38 seconds using the existing
checksum-verified native/gateway fixtures. Output and provenance:
`/tmp/airc-system-v-native-93a280d/crash.log` and `crash-provenance.json`.
Compilation, Linux-targeted vet/staticcheck, formatting and diff checks passed
with terminal exit 0. This test-only increment follows recorded full acceptance.
No Darwin killed-service or Linux race qualification is claimed. Post-admission
process death remains independently required.

## Linux accepted-send replay after process death

`TestSystemVQueueAcceptedReplayAfterKilledService` submits a whole large Unicode
message, requires durable accepted receipt with one native attempt and independently
observes exact author, target and body. It kills and joins the gateway, restarts
against the same journal and verifies unchanged ingress lifecycle. Replaying the
same signed request must retain the accepted native message ID and one send
attempt; independent history must still contain exactly that single whole message.

Actual Linux ARM64 qualification passed in 0.32 seconds using checksum-verified
existing gateway/native fixtures. Output and provenance:
`/tmp/airc-system-v-native-93a280d/accepted-crash.log` and
`accepted-crash-provenance.json`. Compilation, Linux-targeted vet/staticcheck,
formatting and diff checks passed with terminal exit 0. This test increment
follows recorded full acceptance. Linux race and Darwin service-process death
are not claimed; pending-native-attempt crash recovery remains a separate boundary.

## Linux pending-native delivery recovery

`TestSystemVQueuePendingDeliveryAfterKilledService` fixes an owned upstream relay
before gateway startup and withholds native replies. A protected durable pending
receipt and an actual held backend connection precede SIGKILL. After joining,
the journal reopens with its pending job and nonempty stable upstream ID. Restart
retains the exact ingress lifecycle; forwarding and same-intent replay then yield
one independently observed whole native message with exact author, target and
Unicode body. Joined shutdown permits another journal read confirming unchanged
upstream identity. The held attempt sends no message to the native server; this
does not claim a crash after a native send but before its receipt.

Actual Linux ARM64 qualification passed in 0.54 seconds using the existing
checksum-verified native/gateway fixtures. Output and provenance:
`/tmp/airc-system-v-native-93a280d/pending-crash.log` and
`pending-crash-provenance.json`. The shared fixture's existing Darwin native
send/cursor/replay regression passed in 5.275 seconds. Linux-targeted vet and
staticcheck, compilation, formatting and diff checks passed with terminal exit 0.
This test-only increment follows recorded full acceptance. Darwin pending-service
crash and Linux race execution are not claimed.
