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
