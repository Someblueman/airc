# Linux POSIX message-queue boundary

POSIX queue availability has been verified on actual Linux ARM64. The low-level gateway backend is implemented. Configuration/service integration
and independent native AIRC mode acceptance remain outstanding in [ROADMAP.md](ROADMAP.md). System V queues are a
separate API and require their own implementation and acceptance.

## Kernel controls

A standalone Go 1.27.1 probe, cross-compiled with CGO disabled, passed with
terminal exit 0 in the existing isolated Ubuntu ARM64 image
`sha256:33ceb71981b602c1a7443a53469e4dba065f7503eab3078a2d7a57a2ab987517`.
It used the container's private IPC namespace, no network, a read-only root and
artifact mount, and an automatically removed anonymous local volume for its
owned child executable. It changed no host queue limits or mounts.

The probe created a random exclusive queue with two message slots and an 8 KiB
message bound. It verified:

- Effective-UID ownership, mode 0600 and close-on-exec on its descriptor.
- Existing-name refusal with `EEXIST` rather than adoption.
- One complete 8 KiB message without truncation. An undersized receive buffer
  returned `EMSGSIZE` and left that message available to the full-sized read.
- An 8 KiB + 1 send returned `EMSGSIZE`. Empty nonblocking receive and a third
  send to the full two-slot queue returned `EAGAIN`.
- Higher-priority messages were received first. A producer priority is ordering
  metadata, not sender identity or authorization.
- A real child dropped to UID/GID 1 and was refused when opening the root-owned
  queue, returning `EACCES`; this was not a mocked ownership check.
- Unlink removed the name (`ENOENT` on reopening), while the existing open
  descriptor could still exchange a message. Cleanup closed that descriptor.

The actual host limits read by the probe were `msg_max=10`,
`msgsize_max=8192`, and `queues_max=256`. These are fixture observations, not
portable constants or permission to tune production limits.

Probe source SHA256:
`6341efc4c0769d08df7f05fe27cb9a940e079dba7cff4bbfcab3045c9a10430b`.
Linux executable SHA256:
`347672bdc03a35b1ad010bfa3544bf978532c3db35196d152c3fa568f23d2ee3`.
Source: `/tmp/airc-posix-mqueue-probe.go`. Executable, log and provenance are
`/tmp/airc-linux-native-159afc4/posix-mqueue-probe`,
`posix-mqueue-probe.log` and `posix-mqueue-probe-provenance.json`.
The raw attribute ABI in this probe is explicitly Linux ARM64; it does not
establish other architectures, Darwin support, gateway authentication, durable
intake, native delivery, cursor reads or crash recovery.

## Implementation target

The target is an explicit Linux POSIX queue mode with bounded ingress and fixed
owner-provisioned response queues. Queue names use the POSIX `/name` namespace,
not filesystem paths, URLs, principal IDs or names taken from incoming records.
The Linux syscall removes the leading slash at its ABI boundary; public config
and clients retain it. See the [Linux queue naming and descriptor documentation](https://man7.org/linux/man-pages/man7/mq_overview.7.html)
and [library/kernel naming difference](https://man7.org/linux/man-pages/man2/mq_open.2.html).

The queue backend must use exclusive creation for gateway-owned ingress, verify
effective-UID ownership and restrictive mode/attributes on opened queues, retain
descriptor ownership and refuse name replacement before sending to a configured
response queue. Cleanup must unlink only the queue inode it owns and join
receivers/writers before releasing journal ownership. Unsupported platforms
must fail explicitly without substituting another IPC carrier.

The selected wire target reuses the gateway's bounded AGD1 packet framing:
one fragment per queue message, at most 1,000 bytes, with bounded queue depth and
bounded partial assembly. This fits the observed 8 KiB kernel limit without
reducing the existing 16 KiB assembled request or protected response bounds.
Requests require the existing authenticated encrypted detached profile; results
must use the existing signed/encrypted response protection and configured
principal/binding/recipient route. The POSIX receive API returns bytes and
priority, not authenticated producer credentials. Queue ownership, priority,
fragment IDs and the ingress queue name must never become principal authority.
Fragment bookkeeping therefore treats the ingress queue as one shared source
and retains global and authenticated-principal admission limits.

Receive buffers must meet the queue's verified message-size attribute; a short
buffer is an error rather than a truncating packet read. Nonblocking operations
with bounded waits must implement pressure and cancellation without an unbounded
kernel wait. A response queue may fill between fragments; uncertainty and a
partial protected result cannot become a successful application result.

Successful queue send is kernel enqueue, and receive removes the message. Neither
establishes durable gateway intake or AIRC acceptance. The producer must retain
its stable signed intent and retry after uncertainty; the journal's durable
request ID and native acceptance receipt remain authoritative. A crash between
kernel dequeue and journal admission must be tested through actual producer
retry, with no invented acknowledgement-before-dequeue guarantee. See the
[destructive receive and buffer-size contract](https://man7.org/linux/man-pages/man3/mq_receive.3.html).

Mode qualification still requires actual native author/target/body, complete
bounded cursor pages over protected response fragments, stable-intent replay,
queue-full and incomplete-fragment behavior, sender/target refusals, ownership
and replacement boundaries, joined shutdown and uncertain-delivery recovery.
The availability probe above supplies none of those gateway claims.

## Integrated backend controls

The reviewed low-level backend is installed in `internal/gateway/adapter`.
At backend checkpoint `089c179`, its integrated Go/module SHA256 was
`1d0a90484b6a118db6df45c902f5da6c6a0c1655a125d5b5c106d93911fa449d`,
identical to the tested overlay. Integrated adapter race tests passed on Darwin
(23.656s), alongside Darwin and Linux ARM64 vet, command builds, and integrated
adapter staticcheck for Darwin and Linux ARM64, all with terminal exit 0.
Formatting and diff checks passed. No queue configuration or service is enabled.

The preceding full normal/race checkpoint at `96ac834` used source
`92430d1ec90977d963a273e88e11965003a876c4555dc412752e54a1bcc1b4f5`;
it excludes this backend. See the
[recorded full acceptance](testing.md#sequenced-packet-integration-checkpoint).
The subsequent [POSIX ownership full checkpoint](testing.md#posix-ownership-integration-checkpoint) passed; configured-mode acceptance remains outstanding.

The backend provides exclusive creation and verified opening of named queues,
1,000-byte whole-message I/O, queue depths from one to ten, and explicit
unsupported-platform errors. Linux ARM64/AMD64 build guards bound the native
64-bit `mq_attr` ABI. It pins the actual mqueue filesystem directory and queue
descriptor, verifies name/inode/owner/mode/attributes, and serializes descriptor
operations and close. Nonblocking syscalls and polls of at most 25 milliseconds
bound close/cancellation latency and prevent descriptor reuse during I/O.
Owned close preserves replacement inodes; opening a producer descriptor does
not acquire unlink ownership. Zero-value handles refuse operations and close.

Eight controls and two close subcases passed on actual Linux ARM64, terminal
exit 0, in the existing isolated image used above:

- Whole 1,000-byte transfer, short-buffer and zero/oversize message refusal,
  no message from invalid writes, and producer-close name preservation (0.03s).
- Full-queue deadline refusal and healthy recovery after draining (0.05s).
- Close while blocked on an empty read or a full write, each joining with the
  closed-handle error; repeated close remained safe (0.18s total).
- Exclusive-name refusal, wrong-depth and unsafe-permission refusal, recovery
  after restoring mode, and descriptor close-on-exec checks.
- A replacement created while the original inode remained open: old-handle
  reads/writes refused it, old close preserved its name, and replacement close
  removed only its own name.
- Kernel ownership changed to UID/GID 1: even root's otherwise permitted open
  was rejected by backend ownership validation; original-inode cleanup succeeded.
- Zero-value close/read/write refusal and queue-name grammar boundaries.

The actual foreign-UID kernel open denial remains separately verified by the
earlier platform probe; it is not inferred from the root ownership-change test.
Linux vet passed. Darwin name/unsupported controls passed under race detection
(1.605s). Final Linux AMD64 and Windows AMD64 cross-compiles passed; neither
establishes runtime behavior on those hosts. Linux has no race-instrumented claim.

Verified overlay and integrated Go/module SHA256:
`1d0a90484b6a118db6df45c902f5da6c6a0c1655a125d5b5c106d93911fa449d`.
Actual Linux ARM64 draft test executable SHA256:
`fcc4776b0105cb298acbe7e9b7e10d746c2f90e95567ba96aa221636452cd670`.
Per-file hashes and source boundary are in the draft's
`verified-v3-provenance.json`. Executable/log:
`/tmp/airc-linux-native-159afc4/posix-queue-backend-draft-v3{.test,.log}`.
An independent subagent reviewed the separate draft without actionable findings.
These are low-level backend controls. They do not establish packet framing,
configured response routes, authenticated dispatch, native AIRC acceptance,
crash recovery or full acceptance for a configured queue mode.

The same unchanged overlay subsequently passed the full affected adapter package
on actual Linux ARM64: 334 test/subtest passes, zero skips and zero failures,
terminal exit 0. This includes existing adapters as well as the queue controls;
it remains a separate-overlay result, not the repository-wide frozen checkpoint.
The log is
`/tmp/airc-linux-native-159afc4/posix-queue-backend-full-adapter-v3.log`;
counts and executable identity are in the draft's `full-adapter-result.json`.

The eight queue controls and two close subcases also passed ten repetitions with
Go `-gcflags=all=-d=checkptr=2` on actual Linux ARM64: 100 passes, zero skips and
zero failures, terminal exit 0. This checks unsafe-pointer transitions in the
new native syscall code. It does not provide Linux race instrumentation.
Instrumented executable SHA256:
`27f0c93cda01ad30d41b53e414ad073304b32929de27663c41f8e33ecb5fc2fc`.
Executable/log are
`/tmp/airc-linux-native-159afc4/posix-queue-backend-checkptr-v3{.test,.log}`.

## Packet bridge controls

`POSIXQueuePackets` implements `net.PacketConn` over one ingress descriptor and
fixed preopened response descriptors. Successful construction takes handle
ownership. Reads report the shared ingress queue as source and discard priority;
senders still require authenticated encrypted intents. Writes refuse unknown
names and other address types, retain whole-message bounds, and never open a
queue selected by a record. The shared packet driver accepts concrete queue
routes and retains AGD1 assembly, protected responses and admission limits.

Deadline changes cancel blocked I/O and retry against the current deadline.
Timeouts implement the `net.Error` contract. Close prevents new operations,
cancels blocked callers, closes all handles and joins I/O before returning;
externally provisioned response names retain their original unlink ownership.

Three actual Linux ARM64 controls passed ten repetitions (30 passes, no skips
or failures), terminal exit 0: whole-message transfer and fixed-route refusal,
mutable read deadlines and joined blocked read/write close, and extending a
blocked write deadline followed by healthy recovery after drain. The full Linux
adapter package passed 337 tests/subtests with no skips or failures. The same
bridge controls passed ten repetitions under `-d=checkptr=2` (30 passes).
Darwin adapter race tests passed (23.449s); Darwin/Linux ARM64 vet and adapter
staticcheck, command builds, formatting and diff checks passed. Linux race
instrumentation remains unverified.

Go/module source SHA256:
`8ae07e723042cbae95e57b4198e6064f1ae14d28c4a683fc7cf0c73f7d0302c8`.
Linux adapter executable SHA256:
`da64ccac8fac2929e63d636b23323d0f443aa8227fca319f6b8059e9a324ead2`.
Logs and executable hashes are recorded in
`/tmp/airc-linux-native-159afc4/posix-queue-packet-v1-provenance.json` and
`posix-queue-packet-{v1,full-v1,checkptr-v1}.log`. Independent review found no
actionable defects. Configuration/service crash ownership and native AIRC
qualification and a subsequent full integration checkpoint remain outstanding.

## Ingress recovery prerequisite

`ResumePOSIXQueue` can reopen a recorded ingress inode and acquire unlink
ownership only when its current Linux boot ID, IPC namespace device/inode and
queue device/inode match the supplied record. Boot ID is read from a verified
proc filesystem and the IPC namespace descriptor from verified nsfs. Queue
opening retains the existing owner/mode/depth/message-size/name checks. The
epoch is checked before and after opening; refusals close only the newly opened
descriptor and preserve the queue name. Unsupported platforms fail explicitly.

The caller must hold the exclusive gateway journal lock and obtain the record
from durable owner bookkeeping. These APIs do not persist records, infer
ownership from queue names, authenticate producers or implement whole-service
crash recovery. Queue creation before durable ownership registration can leave
an unknown orphan after a crash; it must be refused rather than adopted.

Two actual Linux ARM64 controls passed ten repetitions with `-d=checkptr=2`
(20 passes, zero skips/failures), terminal exit 0. A real subprocess exclusively
created the queue and reported its epoch/inode, then was killed before cleanup.
Altered boot, IPC namespace and queue identity records were refused without
removing the name; exact recovery exchanged a message and removed its owned
name on close. A separately created replacement while the original inode
remained open was refused and preserved. Darwin affected adapter race tests
passed (23.147s), along with Darwin/Linux vet and adapter staticcheck, command
builds, formatting and diff checks. Linux race instrumentation remains absent.

Go/module source SHA256:
`265aacb61273372a81bc6b3c04ee18abe592cf9df6227caf6ed3fda49aeff1d0`.
Instrumented Linux adapter executable SHA256:
`b2831572cd4886ba1e6a9ab41c2c927b0a9766240fbd66a149ce1ee66277d68d`.
Executable/log: `/tmp/airc-linux-native-159afc4/posix-queue-recovery-v3{.test,.log}`.
Independent review found no actionable defects. Journal persistence is described
below; configuration/service ownership and independent native acceptance remain
outstanding.

The full affected Linux adapter package passed under the same pointer
instrumentation: 339 test/subtest passes, 1 skip (the subprocess-only
helper outside its dedicated invocation), zero failures, terminal exit 0.
Log: `/tmp/airc-linux-native-159afc4/posix-queue-recovery-full-v3.log`;
counts and boundaries: `posix-queue-recovery-v3-provenance.json`.

## Journal ownership

Schema 12 adds a strict `posix_queue_ownership` table containing listener ID,
unique ingress name, depth, boot ID, IPC namespace device/inode and queue
device/inode. It stores no credentials or producer authority. Versions 1–11
migrate transactionally; existing delivery jobs, identity pins and OSCORE state
must remain unchanged. Durable records do not establish kernel enqueue/dequeue
or AIRC acceptance.

The trusted registration API requires a newly exclusively created ingress
handle held by its caller. Registration requires absence; replacement compares
the complete previous record under the existing exclusive process lock and
serialized store connection. Queue-name rebinding, stale replacement, invalid
records and loaded corruption fail closed. The owner record count is bounded
at 1,024; replacement remains available at that bound. A successful SQL write
uses the journal's existing full synchronous durability policy. No service calls
these APIs yet; configured ingress and crash recovery remain outstanding.

Verification initially caught a historical OSCORE migration fixture that marked
a schema-12 database as version 10 while leaving the version-12 table present.
The fixture now removes both newer tables before declaring version 10. Actual
Linux migration controls also exposed existing acknowledgement fixtures whose
`time.Unix` local-location values compared unequal to JSON-decoded UTC values
on a UTC host despite identical instants and retained JSON. Their four fixture
timestamps now use explicit UTC; exact acknowledgement comparisons are retained.
The failed Linux log is preserved at
`/tmp/airc-linux-native-159afc4/posix-queue-store-v2.log`.

Affected Darwin store race checks passed (32.517s), along with vet and
staticcheck. Actual Linux ARM64 ownership/migration controls passed with terminal
exit 0: 17 tests/subtests, zero skips or failures. This includes the schema 1–11
matrix, schema-11 OSCORE/job preservation, ownership restart/replacement,
corruption refusal and capacity/replacement-at-capacity controls. Linux vet and
command builds passed. Source and executable identities are recorded in
`/tmp/airc-linux-native-159afc4/posix-queue-store-v3-provenance.json`.

The [full normal/race checkpoint](testing.md#posix-ownership-integration-checkpoint)
finished with terminal exit 0 on frozen reviewed Go/module source
`fbbf3871c611124ff2cb42d0f577629f0cbece6c1bf28044b3e56cf137daf09d`.
Normal and race each passed 2,436 tests/subtests with 53 skips and zero failures;
build, formatting, vet, staticcheck and diff checks passed. That source excludes
the subsequent three-field UTC poll-fixture correction described below. No
configured queue mode is claimed.

The broader actual Linux store run for the frozen journal source failed in two
existing poll fixture tests (eight failed parent/subtest results), while all
ownership controls passed. An exact `a5a52d6` baseline overlay reproduced those
same failures. Its Go/module source SHA256 is
`265aacb61273372a81bc6b3c04ee18abe592cf9df6227caf6ed3fda49aeff1d0`.
Logs: `/tmp/airc-linux-native-159afc4/posix-queue-store-full-v3.log` and
`posix-queue-store-baseline-a5a52d6.log`.

A separate reviewed overlay makes three existing poll acknowledgement fixture
timestamps explicitly UTC without changing their instants or assertions. That
overlay passed the full actual Linux store package: 130 tests/subtests, three
subprocess-only helper skips, zero failures, terminal exit 0. Executable SHA256:
`d3b1e64789f91f7b919631698daddbb71e398da86c470957884a92b87cbcc566`.
Log: `/tmp/airc-linux-native-159afc4/posix-queue-store-poll-utc-draft-full-v1.log`.
The reviewed correction was subsequently applied after the full gate finished.
Integrated Go/module source is
`f09b491f3a6e8bd6a1cea2f8eb1d2ec54f939370bd66d37f43fd1602b50b2852`,
identical to the separately verified overlay. Integrated Darwin store race tests passed (33.599s); store vet/staticcheck,
Linux ARM64 vet, formatting and diff checks passed. The physically integrated
Linux executable passed the full store package with terminal exit 0: 130 test/
subtest passes, three subprocess-helper skips and zero failures. Source/binary
hashes and counts are recorded in
`/tmp/airc-linux-native-159afc4/posix-queue-store-integrated-v4-provenance.json`;
log: `posix-queue-store-integrated-full-v4.log`. Linux race remains unverified.
The journal prerequisite is complete; configuration/service ownership and native
AIRC qualification remain outstanding.
