# Paired POSIX FIFO contract

Catalogue row 32 will use one dedicated private pair of POSIX FIFOs per gateway
child and one provisioned signed/encrypted sender identity. The private descriptor adapter is implemented and tested on Darwin; service
activation and authenticated native connector acceptance remain unqualified. Remaining
work lives only in [ROADMAP.md](ROADMAP.md).

The owner provisions a normalized absolute root, mode 0700, with separate
`input.fifo` and `output.fifo` nodes, mode 0600. The gateway must reject wrong
node types, symlinks, foreign ownership, aliases and changed descriptor/path
identity before opening the journal. An exclusive gateway owner lock prevents
two children serving the same pair. The producer must dedicate the pair to
one session and serialize its input frames; sharing a FIFO among independent
writers is outside this contract. The local OS owner is trusted, as with the
other private filesystem connectors.

The child reads input and writes output. The producer opens the output reader
before child startup, then connects its input writer. The producer must also
bound first contact on the return reader instead of treating its initial EOF
as an established child disconnect. The child must bound
startup and first-byte waiting; it must not mistake an unopened input writer
for a completed authenticated session. In particular, a nonblocking output
writer cannot silently wait forever for an absent return reader. No read/write
self-open endpoint is used to keep a missing peer artificially alive.

Every version-1 frame needs provisioned signed or encrypted authentication;
FIFO access cannot select an AIRC author. Configuration must make the pair's
single sender authority explicit and reject configurations that would share
its return channel among independently provisioned active senders. The
principal's existing binding, operation and target grants remain authoritative.
Gateway response writes must serialize complete bounded frames, preserving
correlation on the dedicated return pipe without interleaving. Responses are
clear to the trusted local owner; encrypted ingress does not make this pipe a
confidential channel against its OS owner.

After the first frame byte, existing framed-stream header/body and write
bounds apply. Peer close between completed frames must drain admitted handlers
and join delivery/journal ownership under the existing bounded grace. Partial
frames, I/O failure and cancellation must cancel and join work while retaining
admitted durable intent. Neither FIFO file names nor descriptor ownership
replace authentication, stable request identity or receipt uncertainty.

A real Darwin nonblocking FIFO probe established the startup ambiguity:

| Input state | `read(1)` result |
|---|---|
| Reader open, no writer has opened | Empty bytes (EOF) |
| Writer connected, no queued bytes | EAGAIN |
| Writer connected, one queued byte | That byte |
| Writer closed after queue drained | Empty bytes (EOF) |

The probe used a disposable private root and actual mode-0600 FIFO, then closed
both descriptors and removed its root. Initial writer absence and writer close
can therefore produce the same read result. Bootstrap must explicitly bound
first contact instead of treating the first EOF as established-peer completion.
This observation is Darwin evidence only; Linux behavior and the eventual
adapter still require their own controls. It proves no native AIRC delivery.


An isolated standard-library Go probe on Darwin arm64 Go 1.27.1 also used real
nonblocking FIFO descriptors and owned close-on-exec duplicates. It confirmed
that the initial read is specifically `io.EOF`, and subsequent connected-empty
reads honor a configured read deadline. After that EOF/deadline, a later queued
byte was read correctly, so the file handle does not cache initial EOF as a
permanent disconnect. Filling the return FIFO produced a write-deadline error;
closing its reader produced `EPIPE` on the duplicate above the standard FD
range. Closing the input duplicate preserved the original FIFO descriptor.

The probe configured 50-millisecond read/write deadlines only to exercise the
poller API; these are not production deadlines or a performance claim. Source
is `/tmp/airc-fifo-poller-probe-20261006.go`; its temporary FIFO roots and owned
descriptors were removed/closed after each terminal-success invocation. This
confirms Darwin primitive feasibility, not authenticated framing, native
acceptance, cancellation joining or Linux behavior of the eventual adapter.

## Descriptor adapter

`adapter.OpenFIFOStream` checks the normalized private root and trusted
ancestors, acquires its exclusive directory lock, and opens the fixed FIFO
nodes without following symlinks or blocking on peer open. Both nodes must
belong to the effective UID, have mode 0600 and one link, and retain their
path/descriptor identity across opening both endpoints. The root's identity
and private mode are rechecked before returning. A missing output reader
fails immediately with the OS open error; failure releases the root lock.
Pollable close-on-exec duplicates own stream I/O; temporary source handles are
closed before the constructor returns. The mailbox root validation/lock code
is shared in place without changing the mailbox's claim/publication behavior.

First-byte waiting has a hard five-second bound beginning at construction.
An earlier caller read deadline applies; clearing or extending that deadline
cannot extend the bootstrap bound. Initial EOF retries wait at most ten
milliseconds per iteration and wake on deadline changes or close. Once any
byte arrives, the caller's normal frame deadlines apply and EOF no longer
retries. These constants are protocol bounds, not throughput claims. Close
interrupts both bootstrap waiting and blocked poller I/O, and releases the
owned descriptors and directory lock once.

Real Darwin FIFO controls cover delayed first contact, connected-empty read
deadlines and recovery after a deadline, absent-writer expiry, shortened
deadlines, bootstrap cancellation, established EOF, return traffic,
backpressure deadlines, blocked-write close, closed-reader EPIPE, exclusive
root ownership, reopen after close/failure, close-on-exec, wrong types/modes,
symlinks and hard-link aliases. These are descriptor controls only: they do
not prove signed/encrypted framing, sender isolation, native AIRC acceptance,
durable restart recovery or Linux runtime behavior.

On Darwin arm64 Go 1.27.1, the complete adapter suite passed with terminal
exit 0 using `go test -race ./internal/gateway/adapter -count=1 -timeout=120s`
(23.093 seconds). This includes the existing mailbox, socket, pipe and framed
adapter controls after extracting the shared private-directory opener.
`go vet ./internal/gateway/adapter`, adapter staticcheck and `git diff --check`
also passed. The initial blocked-write test fixture used a producer FIFO handle
without deadline support; replacing that fixture handle with a pollable owned
duplicate fixed the test without changing production deadlines or behavior.
