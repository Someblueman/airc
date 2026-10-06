# Parent-owned Unix socket pairs

Catalogue row 34 includes parent-owned socket pairs. The descriptor adapter
`OpenSocketPairStream` accepts a dedicated inherited endpoint only when both
ends are connected, unnamed AF_UNIX SOCK_STREAM sockets. It rejects regular
files, pipes, TCP sockets, datagram pairs and named Unix connections before any
service or journal startup. It does not create or connect a socket path.

The adapter returns a Go network-poller connection with read/write deadlines,
using an owned close-on-exec duplicate. Closing that connection preserves the
source descriptor; the caller owns the source and must close its inherited copy
when appropriate. Nonblocking state can be shared through the open file
description, so the descriptor must be dedicated to this session. The parent
must not pass an endpoint concurrently used by another reader or writer.

A parent-owned descriptor establishes the local channel boundary, not an AIRC
identity. Each frame requires existing provisioned signed or encrypted authentication;
inherited descriptor access does not authorize a sender. Responses remain clear
on the dedicated parent/child channel, as with stdio. A parent able to read that
channel can read responses.

Start a dedicated child with `airc-gateway serve-socketpair --file CONFIG` and
pass exactly one socket-pair endpoint as inherited descriptor 3 (the first
`exec.Cmd.ExtraFiles` entry). Configure exactly one enabled listener with
`protocol: "socketpair"`, empty address and explicit allowed operations. Address,
TLS and origin settings are rejected. Ordinary `serve` rejects this listener
before opening the journal. Diagnostics go to stderr; no readiness record or
frames are written to stdout. The parent owns the other endpoint and must read
responses while writing requests.

The service reuses the existing authenticated framed-stream dispatcher and
joined delivery/journal lifetime. Clean EOF between frames drains admitted work
and allows the existing bounded five-second durable delivery grace. Partial
frames, I/O failures and signal cancellation cancel and join handlers/workers.
Frame, correlation, request and stream limits remain those in
[stdio.md](stdio.md) and [framed-stream-v1.md](framed-stream-v1.md).

Darwin arm64 adapter tests use real socket pairs for bidirectional I/O, read
deadline expiry and duplicate ownership, plus real rejected descriptor types and
both ends of a named Unix connection. Race count-three tests, package vet and
staticcheck passed. Linux runtime qualification and the remaining service/native
acceptance boundaries remain in [ROADMAP.md](ROADMAP.md).

Actual Darwin CLI controls pass under race detection for invalid-signature
refusal followed by clean EOF and fresh journal ownership, plus missing/regular
inherited descriptor and ordinary-command refusal before state creation. The
existing stdio controls also pass after service lifecycle sharing. These checks
are activation/refusal evidence and do not establish native delivery alone.


At executable source `604504a`, Darwin arm64 Go 1.27.1 native socket-pair
acceptance passed under race detection (9.074 seconds). The actual child gateway
binary SHA-256 was
`8e7922c461ca4a11584cd7c3570e5996c879efe5d7fa2bc1876240f2e6ba2a76`.
The checksum-verified native daemon baseline was
`96f16c3bbb8a3faac2e69684f591af63ceb58450`, archive SHA-256
`4f25ee66941bd79c29f936b8deabc120dc0451e5d1b747afee4bbdf593b32829`.

Signed and encrypted profiles each refused invalid authentication and then
admitted two Unicode/newline sends. An independent native observer verified
exact author, room, body and accepted message IDs. Identical envelope replay
retained the same message ID and one send attempt. History limit-one pages
returned both records with exact ID, author, room and body, explicit `more` then `ok`, nonempty advancing
cursors and no gap; a final cursor request returned an empty caught-up page.
The signed child joined SIGINT and the encrypted child joined SIGTERM within
three seconds, emitted no stdout and released journal ownership for reopening.
Package vet and staticcheck also passed. That control exercises in-process replay and clean signals; it does not test
child restart, pending termination or framing failures.


At source `4fc07fd`, the actual socket-pair framing failure and fresh-child
recovery controls passed under race detection together with signed/encrypted
acceptance (17.943 seconds). The child executable SHA-256 was
`f4f27d18f4c09d63cd2da866fa4e8334b9b40b0850b979f4fc078ad59d47ef61`.
Partial header, partial valid signed body, malformed JSON, a 16,385-byte declared
body and zero length each closed the return channel without data, exited with
an error within the three-second join bound and left zero jobs across all
principals. For each case, a fresh child using that same journal then completed
encrypted native delivery, stable replay and complete cursor reads, with the
independent observer checking exact records. Vet and staticcheck passed.
This proves failure recovery and reuse of journal ownership, not replay of an
already-admitted job after restart or recovery of pending work after termination.


At source `f3169d5`, the accepted-intent fresh-child replay control passed under
race detection (7.861 seconds). The actual child executable SHA-256 was
`561e55d691d8e062b9382e853209ad06da39d7684572b33363b5c897230a6a97`.
A signed send reached native acceptance with one attempt, and an independent
observer checked exact author, room, body and message ID. After joined SIGINT,
a fresh journal owner verified the persisted accepted state and nonempty stable
upstream ID. A new child replayed the same intent with an encrypted envelope,
retaining accepted state, message ID and one attempt. After joined SIGTERM, the
journal still held the same upstream ID, and an independent native history
query contained exactly the original record. Vet and staticcheck passed.
That accepted-intent control does not exercise pending intent at termination.


At source `5419f4a`, pending socket-pair recovery passed under race detection
for SIGKILL, SIGINT and SIGTERM (12.662 seconds). All six socket-pair CLI/native
controls then passed together in 26.791 seconds, including the existing fixture
callers. The actual child executable SHA-256 was
`efffd5845927aaf92d22ff1cfae0a6723f955126f68a0906dcbd8cd7551eb72e`.
Vet and staticcheck passed.

Each case held an owned upstream connection after durable signed intake and
verified zero native records before termination. SIGKILL was checked through
the actual signaled wait status; SIGINT/SIGTERM exited successfully, and all
children joined within three seconds. A fresh journal owner found durable,
unaccepted intent and its nonempty upstream ID. A fresh socket-pair child
replayed the same intent with an encrypted envelope after the relay allowed
new connections. It reached acceptance, retained the upstream ID and produced
exactly one native record with the independently observed author, room and
body. This is termination of pending intent before native acceptance; it does
not simulate lost native acknowledgement or power failure. Linux runtime
qualification remains open.


## Linux descriptor checkpoint

Source `8f1f9c30f95885bc2db9cd92a2648d49af483e1f` was cross-compiled with
installed Go 1.27.1 using `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go test -c
-p=1 ./internal/gateway/adapter`. The resulting test executable ran in the
already-installed Ubuntu 24.04 ARM64 image
`sha256:33ceb71981b602c1a7443a53469e4dba065f7503eab3078a2d7a57a2ab987517`
on Docker 29.7.2 Linux aarch64, with no network, a read-only root and disposable
`/tmp`. No image or toolchain was downloaded.

The combined `^(TestFIFO|TestSocketPair|TestInheritedUnix)` selection exited 1:
all FIFO and inherited Unix-stream tests passed, but
`TestSocketPairStreamOwnershipAndDeadlines` rejected a real unnamed socket pair
with `socket pair requires connected unnamed Unix endpoints`. Socket-pair
refusal tests passed. The captured output is
`/tmp/airc-gateway-linux-adapter-8f1f9c3.log`.

This is actual Linux descriptor evidence without race instrumentation. It
does not qualify authenticated native AIRC delivery, complete cursor reads,
or durable restart acceptance on Linux. Those roadmap items remain open.


### Linux address-length diagnosis

An isolated standard-library Go diagnostic, cross-compiled and run in the same
Ubuntu container, inspected the actual `getsockname` and `getpeername` lengths.
For a real `AF_UNIX/SOCK_STREAM` socket pair, both lengths were 2 bytes
(the address family only), while the decoded `SockaddrUnix.Name` was `"@"`.
A connected socket using the empty abstract name also decoded as `"@"`, but
its named endpoint length was 3 bytes; the unbound endpoint remained 2 bytes.

Consequently, accepting decoded `"@"` as unnamed would weaken the selected
contract. Linux qualification requires checking the kernel-returned address
length for both endpoints, preserving rejection of abstract named connections
including the empty abstract name. The diagnostic did not change repository
executable source and is not native AIRC acceptance evidence.


### Linux unnamed socket-pair validation fix

The adapter now checks the kernel-returned local and peer address lengths on
Linux. Each must contain only the `AF_UNIX` family; decoded name `"@"` is
not accepted as evidence of an unnamed endpoint. Darwin retains its existing
empty-name check. Connected ordinary and empty abstract names remain refused
from both client and server descriptors.

The actual Linux ARM64 adapter test selection
`^(TestFIFO|TestSocketPair|TestInheritedUnix)` passed with terminal exit 0 in
the same existing isolated Ubuntu image, including the previously failing
ownership/deadline test and new abstract-name refusal cases. Output is
`/tmp/airc-gateway-linux-adapter-socket-fix.log`; the executable was host
cross-compiled with `CGO_ENABLED=0`, so this is not Linux race evidence.

Affected Darwin adapter race tests passed in 6.828 seconds. All actual
socket-pair/inherited CLI, authenticated native send and cursor, frame failure,
accepted replay and pending-termination recovery tests passed under race
detection in 50.350 seconds. Darwin and Linux adapter vet, Darwin staticcheck
and diff checks passed. Linux native AIRC acceptance and final full gates
after executable changes remain open in the roadmap.
