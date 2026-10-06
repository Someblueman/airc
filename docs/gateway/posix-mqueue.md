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
Its integrated Go/module SHA256 is
`1d0a90484b6a118db6df45c902f5da6c6a0c1655a125d5b5c106d93911fa449d`,
identical to the tested overlay. Integrated adapter race tests passed on Darwin
(23.656s), alongside Darwin and Linux ARM64 vet, command builds, and integrated
adapter staticcheck for Darwin and Linux ARM64, all with terminal exit 0.
Formatting and diff checks passed. No queue configuration or service is enabled.

The preceding full normal/race checkpoint at `96ac834` used source
`92430d1ec90977d963a273e88e11965003a876c4555dc412752e54a1bcc1b4f5`;
it excludes this backend. See the
[recorded full acceptance](testing.md#sequenced-packet-integration-checkpoint).
A subsequent full integration checkpoint remains required.

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
