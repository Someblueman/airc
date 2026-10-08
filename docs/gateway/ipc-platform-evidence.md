# Local IPC platform evidence

These pre-implementation probes establish platform limits for catalogue rows
29, 30 and 32. The probes alone do not establish adapter support. Row 29 now
has a partial [Unix datagram implementation](unix-datagram.md); rows 30 and 32
remain pending. Probes used private temporary directories and closed all
descriptors; no live AIRC endpoint, account or state was used.

The tested host reports macOS 26.6.2 (25G83), Darwin 25.6.0. `sysctl net.local.dgram.maxdgram` reports 2048. Two actual bound Unix datagram sockets successfully exchanged whole 1200-byte and 2048-byte packets. Attempts to send 2049, 4096 and 16384 bytes failed with errno 40, `Message too long`. The test receiver therefore did not receive those packets. This is a host limit, not a claim about every Darwin installation or Linux.

Consequences for row 29: the shared 16 KiB request/64 KiB response framing cannot simply be placed in a single Unix datagram on this host. A bidirectional adapter needs an explicit packet budget and tested bounded fragmentation/reassembly or result paging that can preserve every supported operation and whole message. The datagram limit does not make the transport inherently send-only. Fixed authenticated return endpoints, truncation checks and sender lifecycle remain required. No datagram importer has been implemented by this probe.

Creating an `AF_UNIX`, `SOCK_SEQPACKET` socket failed with errno 43, `Protocol not supported`. This is concrete evidence that row 30 cannot run natively through that socket type on this host. Linux remains a separate implementation/runtime target; cross-compilation of another carrier does not verify sequenced packets. Do not substitute a stream socket and advertise sequenced-packet support.

Row 32 also remains pending. Although the anonymous-pipe descriptor helper can validate an `S_IFIFO` descriptor, it does not implement named FIFO creation/ownership, isolated responses, writer arbitration or restart semantics. A configured bidirectional request/response pair can support reads, receipts, replies and watches. Named FIFO implementation must establish owner-controlled paths and no-follow inode checks, bounded opening and backpressure, exact cleanup ownership and an explicit session protocol preventing cooperative clients from interleaving large frames. Signed/encrypted identity remains required: path ownership or possession of a cooperative lock does not prove the writer's principal or protect against another process with the same OS authority. Ingress-only operation cannot stand in for the bidirectional mode without a concrete underlying deployment limitation.

To reproduce the socket probe with Python's standard library:

```python
import socket
import tempfile

with tempfile.TemporaryDirectory(prefix="agw-ipc-probe-") as root:
    try:
        sequenced = socket.socket(socket.AF_UNIX, socket.SOCK_SEQPACKET)
        sequenced.close()
        print("sequenced socket creation succeeded")
    except OSError as error:
        print("sequenced:", error.errno, error.strerror)
    sender = socket.socket(socket.AF_UNIX, socket.SOCK_DGRAM)
    receiver = socket.socket(socket.AF_UNIX, socket.SOCK_DGRAM)
    try:
        sender.bind(root + "/sender")
        receiver.bind(root + "/receiver")
        receiver.settimeout(1)
        for size in (1200, 2048, 2049, 4096, 16384):
            payload = b"x" * size
            try:
                sent = sender.sendto(payload, root + "/receiver")
                data = receiver.recv(65536)
                print(size, sent, len(data), data == payload)
            except OSError as error:
                print(size, error.errno, error.strerror)
    finally:
        sender.close()
        receiver.close()
```

## System V queue capability on Darwin ARM64

On 2026-10-08 an isolated native C probe created an exclusive `IPC_PRIVATE`
message queue with mode 0600. `IPC_STAT` reported effective owner UID 501,
mode 0600 and a 2,048-byte queue capacity. A whole 1,000-byte type-1 packet
passed unchanged. Receiving into 999 bytes returned `E2BIG` without consuming
the packet; a subsequent full receive still returned it. Empty nonblocking
receive returned `ENOMSG`. `IPC_RMID` removed the queue, and inspection through
its former ID returned errno 22. The executable returned exit 0 and changed no
host IPC settings. Apple clang 21.0.0 compiled it with
`cc -std=c11 -Wall -Wextra -Werror`.

A separate pure Go probe used the actual Darwin `MSGGET`, `MSGSND`, `MSGRCV`
and `MSGCTL` syscalls, without cgo, and passed the same whole 1,000-byte
send/receive with terminal exit 0. Deferred cleanup removed its private queue.
Commands were `cc -std=c11 -Wall -Wextra -Werror
/tmp/airc-system-v-probe.c -o /tmp/airc-system-v-probe`, followed by that
executable, and `go run /tmp/airc-system-v-go-probe.go`.

C source SHA256: `52fd6b1f6bf2b07f2471d12f5492f9096f8172d1ddb464bea7b67004e493e591`.
Go source SHA256: `62bcf5727b107cf31483907ee164a069882ccddd917edcdd6a3943c6687ec203`.
These establish a usable local syscall path and host packet/queue observations,
not authenticated gateway ingress, native AIRC delivery, crash-safe admission,
Linux/other-architecture support or general IPC qualification. Queue ownership
and message type do not prove the application producer's principal. The
System V implementation and its complete acceptance remain on the roadmap.

## Private Darwin System V queue prerequisite

`internal/gateway/sysvqueue` now provides Darwin ARM64 exclusive positive-key
queue creation, verified opening and bounded packet I/O. It uses libSystem
trampolines following the existing `x/sys` Darwin convention, without cgo. The
initial experimental raw syscalls remain probe evidence; production helpers
replace that deprecated interface. Native C header inspection and actual
metadata retrieval established the selected 116-byte packed `msqid_ds` profile:
UID offset 0, creator UID 8, mode 16, sequence 18, key 20, queue capacity 48
and last-change time 88. This is a platform-specific ABI, not a portable layout.
See [Apple's System V implementation](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/kern/sysv_msg.c).

Opening/using a queue requires current effective UID ownership and creator UID,
mode 0600, the exact key, and capacity from 1,000 through 65,536 bytes. The
retained identifier, sequence and last-change metadata are checked before I/O
and owned removal. System V identifiers are not pinned file descriptors: these
checks establish the observed lifecycle at each operation, not an atomic lease
against another process with the same OS authority. Neither these metadata nor
message type grants application identity.

Messages must contain 1–1,000 bytes with type 1. Receives use a full-sized kernel
buffer and never `MSG_NOERROR`; oversized head messages return `E2BIG` without
truncation or consumption. Queue-full and empty waits poll nonblocking calls
under caller cancellation. Close marks the handle closed; blocked users return
on their next poll, and the caller must join them. Successful receive removes the kernel record. Enqueue
is not durable intake, and cancellation racing enqueue/receive can leave an
uncertain outcome requiring stable authenticated-intent retry. Opening a peer
handle does not own removal. Existing keys are not adopted by exclusive create.
Removed handles refuse replacement queues; closed/zero handles are unusable.
No host queue limits are changed.

Linux ARM64 uses direct kernel syscalls and the 120-byte asm-generic
`msqid64_ds` layout: UID 4, creator UID 12, mode 20, sequence 24, key 0,
capacity 88 and last-change time 64. An actual isolated Linux `IPC_STAT` probe
verified ownership, mode, capacity and current timestamp against these offsets.
See the [Linux message-queue UAPI](https://github.com/torvalds/linux/blob/master/include/uapi/asm-generic/msgbuf.h)
and [IPC permission UAPI](https://github.com/torvalds/linux/blob/master/include/uapi/asm-generic/ipcbuf.h).
The bounded lifecycle and message operations are shared with Darwin ARM64;
pointer-bearing syscall wrappers retain escaping buffers and explicit liveness.

This private layer is unused by the gateway service. Platforms other than
Darwin ARM64 and Linux ARM64 explicitly return unsupported. Authenticated encrypted application framing,
protected result routing, durable ownership/recovery and real AIRC qualification
remain open. System V queue IDs persist beyond owner-process death, so this
prerequisite does not establish crash cleanup or safe restart adoption.

## Private System V packet routing prerequisite

The adapter wraps verified System V handles as a whole-fragment `net.PacketConn`
for the existing AGD1 driver. Its local address is a copied configured positive
queue key; received messages carry that shared ingress address, never a producer
identity. Writes accept only the copied map of provisioned System V response
keys and refuse another address type, an ingress key or an unknown key. The
existing packet driver's destination validation, address copying and mailbox
watch mode recognize this explicit carrier. No configured listener uses it yet.

POSIX and System V packet handles share the existing deadline/cancellation and
joined-close implementation. Their constructors retain the concrete platform
checks and queue ownership rules. Constructor failure transfers no handles;
successful construction owns supplied handles. Close cancels active reads and
writes, closes queues and joins every registered operation. Opened response
handles leave external queues intact; an exclusively created ingress is removed.
Reads require a full 1,000-byte buffer and never silently truncate a fragment.
Changing a deadline wakes an in-flight operation; clearing a deadline permits
waiting again. A cancellation racing destructive receive or enqueue remains an
uncertain transport outcome, requiring stable authenticated-intent retry.

Final Darwin affected packet tests passed normal in 0.841 seconds and race in
1.941 seconds on 2026-10-08. The cgo-disabled Linux ARM64 test executable passed
actual System V and existing POSIX packet tests in the cached isolated Ubuntu
fixture, including whole fragments, fixed routes, constructor refusal, mutable
deadlines, pressure and shutdown with registered blocked readers/writers.
Repository build, vet, staticcheck, affected formatting and diff gates passed
with terminal exit 0. This is packet-layer evidence, not authenticated native
AIRC delivery, durable recovery or full repository acceptance.

## Private System V recovery prerequisite

`sysvqueue.Resume` accepts an exact recorded key, kernel ID, sequence and
last-change time only in the same current boot/IPC epoch. It checks the epoch
before and after verified opening, then grants removal ownership. Refusal closes
only the opened peer handle and never removes an unknown or replacement queue.
The caller must hold its exclusive journal lock and load the ownership record
from that journal; this function does not make a caller-supplied record trusted.
Durable records and service startup wiring remain open.

The shared `ipcepoch` helper verifies Linux boot-ID reads originate from procfs
and the opened IPC namespace descriptor from nsfs, retaining its device/inode.
The existing POSIX recovery profile now uses that same checked kernel helper,
while preserving its ownership type and invalid-profile error. Darwin ARM64
uses the read-only `kern.bootsessionuuid`, normalized to a bounded lowercase
UUID; it does not invent a Linux namespace for Darwin's shared kernel IPC space.
See [XNU's read-only boot-session sysctl](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/kern/kern_sysctl.c).
Zero UUIDs and malformed boot IDs are refused. Neither boot metadata nor a queue
identity proves application authorization.

Native tests kill a real separate queue owner after it enqueues a complete
1,000-byte packet and reports its identity/epoch. Recovery preserves every byte
and its acquired removal ownership removes that exact queue on close. Stale
boot/namespace records and changed ID/sequence/time are refused while the
original remains usable. Removing and replacing a key refuses recovery and
leaves the replacement usable. These are kernel recovery controls; they do not
prove durable intake, authenticated AIRC delivery or receive-before-admission
replay. A crash between exclusive creation and durable registration can leave
an unknown queue which service startup must refuse, requiring owner cleanup.

On 2026-10-08 final Darwin System V tests passed normal in 0.610 seconds, race
in 2.590 seconds and cgo-disabled in 0.645 seconds. The shared epoch package
passed normal/race/cgo-disabled in 0.177/1.177/0.241 seconds. Actual cached Linux
ARM64 System V tests passed, including the killed-owner test; existing Linux
POSIX killed-owner/replacement recovery and both packet adapters also passed.
The fixture retained no network, read-only root/artifact mounts, no capabilities
and no-new-privileges. Repository build, vet, staticcheck, affected formatting
and diff gates passed with terminal exit 0. Linux race, other architectures,
full repository acceptance and authenticated gateway qualification are not
established by this private-layer checkpoint.
