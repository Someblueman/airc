# System V queue gateway boundary

Plain author requests: a listener without return routes takes plain-profile
frames in AGD1 messages and answers with plain frames on its `reply_to` queue
key, with no principals or keys; requests without an author may use
`bound_author`. Exercised on Linux AMD64 against a current aircd. See [local
authors](local-authors.md).

The System V listener is implemented on Darwin ARM64 and Linux ARM64 through
public configuration and Run, using schema 18 journal ownership and existing
protected packet framing. Actual startup/refusal and joined restart tests pass
on both platforms. Full normal/race acceptance and static gates passed on the
frozen activation source; see [the exact checkpoint](testing.md#system-v-service-activation-acceptance).
Actual native Unicode delivery, protected whole cursor/history/watch replies,
authentication refusals, bounded pressure, lifecycle replacement and pre-admission,
accepted and pending-native process-death recovery pass on Darwin/Linux ARM64.
[Integrated acceptance](testing.md#system-v-native-qualification-acceptance)
includes full Darwin normal/race and all eight actual Linux native tests.
Linux AMD64 is also implemented; its runtime and migration acceptance are deferred.
Other architectures remain unsupported.
Kernel and packet evidence remains in [ipc-platform-evidence.md](ipc-platform-evidence.md).

## Linux AMD64 implementation

The selected additional target is Linux AMD64 using the native 64-bit System V
syscalls. It shares the Linux ARM64 adapter because both UAPIs place `ipc64_perm`
inside a 120-byte `msqid64_ds` with the same fields consumed here: key at 0,
owner/creator UID at 4/12, mode at 20, sequence at 24, change time at 64 and
queue capacity at 88. Message type remains an eight-byte native long. The
architecture's `x/sys/unix` syscall numbers select the correct kernel entrypoints;
32-bit/x32 layouts are not enabled.

Journal schema 23 transactionally rebuilds only the System V ownership table
to allow `linux-amd64`. Existing Darwin/Linux ARM64 records retain every identity,
key, boot and namespace field. Existing jobs, receipts and replay tables are
unchanged. The migration needs space for the replacement table; failure leaves
the previous transaction/version intact. A journal whose listener already owns
a queue on another recorded platform is still refused: enabling a platform is
not permission to adopt or remove another machine's queue.

The normal configuration, protected packet protocol, fixed return queues and
queue lifecycle rules below apply. Existing package/service/native acceptance
sources now include Linux AMD64. A migration case covers retained ARM64 and
Darwin ownership, a retained job and the added AMD64 constraint. These tests
were not executed during implementation. Production compilation is separate
from actual IPC, restart, namespace and AIRC qualification, which remains on
[ROADMAP.md](ROADMAP.md).

The layouts are defined by the Linux [generic IPC permission UAPI](https://github.com/torvalds/linux/blob/v6.12/include/uapi/asm-generic/ipcbuf.h),
[generic message UAPI](https://github.com/torvalds/linux/blob/v6.12/include/uapi/asm-generic/msgbuf.h)
and [x86 message UAPI](https://github.com/torvalds/linux/blob/v6.12/arch/x86/include/uapi/asm/msgbuf.h).

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

## Darwin accepted and pending process-death qualification

The accepted-send and pending-native crash tests now execute on both supported
Darwin/Linux ARM64 platforms. Actual Darwin normal/race runs passed in
5.249/9.028 seconds, including journal reopening, exact ingress lifecycle,
protected replay and independent native author/target/whole-body observation.
Accepted replay preserves one native attempt and message ID; pending recovery
preserves the stable upstream ID. Affected vet/staticcheck and diff gates passed
with observed terminal exit 0. Existing Linux runtime evidence above remains
applicable: only build constraints and filenames changed. This supersedes those
two sections' earlier absence of Darwin process-death evidence.

Darwin pre-admission destructive-dequeue, service queue pressure and replacement
qualification remain open. This source increment follows full acceptance and
does not establish the separate post-native-send/pre-receipt crash boundary.

## Darwin service pressure qualification

The shared pressure test now runs on Darwin/Linux ARM64. Darwin verifies actual
SIGSTOP state through `/bin/ps`; Linux retains the existing `/proc` witness.
Actual Darwin normal/race runs passed in 8.408/12.406 seconds: stopped ingress
full-queue deadlines, protected full-response expiry after independent native
processing, whole filler retention, healthy-call recovery and one-attempt native
receipt. Darwin vet/staticcheck, Linux-targeted vet, formatting and diff checks
passed with observed terminal exit 0. Linux assertions and its stopped-state
witness remain unchanged from recorded actual runtime acceptance.

This supersedes the earlier lack of Darwin service-pressure qualification.
Darwin replacement and pre-admission destructive-dequeue still remain open;
this test increment follows the recorded full acceptance source.

## Darwin service replacement qualification

The shared replacement test now runs on Darwin/Linux ARM64. Darwin removes only
the isolated fixture's retained ingress ID with `/usr/bin/ipcrm -q`; Linux uses
the unchanged `IPC_RMID` syscall. Actual Darwin normal/race qualification passed
in 4.131/5.751 seconds: old-producer refusal, joined error shutdown, original
journal's restart refusal before readiness, preserved replacement/response
lifecycles and no durable/native intake. Darwin vet/staticcheck, Linux-targeted
vet, formatting and diff checks passed with terminal exit 0. Linux assertions
and removal operation are unchanged from its recorded runtime acceptance.

This supersedes the earlier lack of Darwin replacement qualification. Darwin
pre-admission destructive-dequeue remains open; this test increment follows
recorded full acceptance.

## Darwin uncertain-dequeue qualification

The pre-admission crash test now executes on Darwin/Linux ARM64. Darwin uses
`/usr/bin/ipcs -q -o` to witness whole queued bytes for the exact isolated ingress
ID and their complete destructive dequeue; unavailable accounting fails the
test. Linux retains its exact `/proc/sysvipc/msg` implementation. Both retain
a held real SQLite writer, actual stopped-state witness, SIGKILL joining, no
durable admission/response, exact lifecycle restart and same-intent retry with
one exact independently observed native message.

Actual Darwin normal/race runs passed in 4.974/6.209 seconds. Darwin vet and
staticcheck, Linux-targeted vet and diff checks passed with terminal exit 0.
Linux assertions/accounting remain unchanged from recorded actual runtime
acceptance. This supersedes the earlier lack of Darwin pre-admission evidence.
The increment follows recorded full acceptance; no separate post-native-send/
pre-receipt crash qualification follows from it.
