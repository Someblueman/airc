# Paired POSIX FIFO contract

Catalogue row 32 uses one dedicated private pair of POSIX FIFOs per gateway
child and one provisioned signed/encrypted sender identity. The private descriptor adapter, authenticated framed service activation, native
signed/encrypted delivery and complete bounded cursor reads are verified on
Darwin, including accepted replay through a fresh child and pending termination
recovery. Linux runtime qualification remains unfinished. Remaining work lives
only in [ROADMAP.md](ROADMAP.md).

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
interrupts both nonblocking read waits and blocked poller writes, and releases the
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

## Writer close during a waiting read

An additional real Darwin control, `TestFIFOBlockedReadSeesWriterClose`,
exposed a boundary missing from the initial descriptor tests: after first
contact, a Go poller read already waiting on an empty named FIFO did not wake
when the writer closed. It returned the configured one-second read-deadline
error. The earlier EOF control closed the writer before calling Read and did
not cover this case.

FIFO input now uses actual nonblocking descriptor reads separated by at most
ten-millisecond waits that wake on close or a deadline change. It checks the
unchanged bootstrap/frame deadline before reading and before waiting. After
first contact, a zero-byte OS read returns EOF immediately. This avoids relying
on a Darwin poller close notification while retaining the configured deadlines;
FIFO response writes still use owned pollable duplicates. Owned close errors
from the raw descriptor API are normalized to `os.ErrClosed`.

The new writer-close control and complete adapter race suite passed with
terminal exit 0 in 23.508 seconds on Darwin arm64 Go 1.27.1. Adapter vet,
staticcheck and diff checks passed. This fixes descriptor-level shutdown; it
does not qualify authenticated service/native acceptance or Linux runtime.

## Configuration and service activation

Use `airc-gateway serve-fifo --file CONFIG` with exactly one enabled `fifo`
listener. Its address names the provisioned private root, not either node:

```json
{
  "id": "pair",
  "protocol": "fifo",
  "address": "/private/owner/fifo",
  "enabled": true,
  "operations": ["send", "receipt_get", "history_read"],
  "fifo": {"principal": "device"}
}
```

The named principal must be active and have a provisioned signing key. While the FIFO listener is enabled, every
other configured principal must be revoked: this dedicated return pipe cannot
serve independent active sender authorities. The existing principal binding,
operation and target grants still govern every verified signed/encrypted frame.
A cursor key is required for bounded reads under the existing configuration
rules. FIFO options on another protocol, implicit operations, TLS/origins,
relative roots, additional enabled listeners and overlapping journal,
credential or local-listener paths are rejected. Configuration does not create
FIFO nodes or accounts.

The producer opens `output.fifo` for reading first, starts the child, then
connects its serialized writer to `input.fifo`. Both producer and child bound
initial contact. The child opens/validates the pair and acquires exclusive root
ownership before opening the journal. Ordinary `serve` rejects a FIFO listener
before journal ownership; use the dedicated command. The child emits no stdout
readiness record: authenticated response frames travel only through the return
FIFO, while startup diagnostics use stderr.

The local-stream service uses the existing authenticated framed dispatcher,
four-correlation admission bound, serialized complete response writes and
joined delivery/journal lifecycle. EOF between frames drains admitted handlers
under the existing grace; cancellation joins the stream and delivery workers
before closing the journal. Malformed or incomplete frames retain the existing
failure bounds. No filename, peer open or local UID authenticates a sender.

`TestFIFOCLIAuthenticationRefusalEOFAndSignalsJoinOwnership` uses the real CLI
and actual paired FIFOs. Four outstanding signed/encrypted invalid envelopes
produce distinct correlated authentication refusals. EOF, SIGINT and SIGTERM
each exit cleanly, preserve empty durable admission and release journal/root
ownership. `TestFIFOStartupRejectsBeforeJournal` verifies missing return reader,
unsafe FIFO mode and ordinary-serve refusals without creating journal state.
These controls and existing socket-pair/inherited CLI controls passed under
race detection in 12.001 seconds. Complete config/service/CLI race packages
also passed, plus affected vet/staticcheck and diff checks. Those activation controls qualify
refusal/shutdown only. Their results alone do not establish native authenticated
delivery, complete bounded pages or durable recovery; native delivery/reading
has separate evidence below, and remaining recovery boundaries stay in the
roadmap.

## Native delivery and complete bounded reads

`TestFIFOAuthenticatedSendAndBoundedResume` runs the actual `serve-fifo` child
and private FIFO pair separately for signed and encrypted envelopes. Each
profile first rejects invalid authentication, then sends two Unicode/newline
messages through the provisioned sender binding. An independent native AIRC
observer verifies each accepted message's ID, author `fixture-author`, room
`#gateway-fixture` and whole body. Replaying the same envelope preserves the
accepted message ID with one send attempt.

The shared framed acceptance control reads retained history with limit one.
Both pages match the observed ID/author/room/body exactly, have nonempty
cursors and no gap, and report `more` then `ok`; a final cursor read is empty
and caught up. This verifies a complete explicit page boundary, not merely a
successful read. Signed and encrypted profiles each run this full control;
SIGINT/SIGTERM shutdown releases journal ownership afterwards.

The FIFO controls plus the unchanged socket-pair and inherited Unix-stream
controls passed with terminal exit 0 under race detection in 18.386 seconds on
Darwin arm64 Go 1.27.1. Integration vet, staticcheck and diff checks passed.
The shared test helper now accepts the actual deadline-capable framed stream
interface, without synthesizing a network connection for FIFOs. These results
qualify Darwin native delivery and bounded reading; they do not prove fresh
child replay, pending termination or Linux runtime behavior.

## Peer and frame-failure recovery

`TestFIFOFrameFailureAndFreshChildRecovery` uses the real FIFO child separately
for a two-byte partial header, a valid signed request missing its last body
byte, malformed JSON, a declared frame size of 16,385 bytes and a zero-length
frame. The producer half-closes only its input writer, preserving its return
reader. Each child exits with failure within the existing three-second join
bound and produces no return bytes. Because no initial response byte exists,
joining the child supplies the established-close fence before inspecting the
return reader's EOF. A read-only SQLite count checks every principal's jobs
and proves zero durable admission, rather than checking only one request ID.

After each failure, a fresh child uses the same private FIFO nodes and journal.
It completes encrypted native delivery, stable same-session replay and all
explicit bounded cursor pages under the independent native observer. This
proves that failure releases the pair/journal and leaves healthy authenticated
intake available; it is not replay of a previously admitted request.

`TestFIFOAbsentInputWriterExpiresAndFreshChildRecovers` opens the return reader
but never opens an input writer. The actual child exits with the unchanged
five-second bootstrap deadline error, no stdout and no admitted jobs, before
the test's eight-second outer process budget. Reopening the descriptor adapter
while the return reader remains present proves root ownership was released;
a fresh real child then completes encrypted native delivery and bounded pages.
Missing return readers and unsafe nodes retain the earlier before-journal
startup refusals, and established peer close retains the earlier clean EOF
control.

All FIFO integration controls passed together with terminal exit 0 under race
detection in 26.414 seconds on Darwin arm64 Go 1.27.1; integration vet,
staticcheck and diff checks also passed. The producer fixture now owns
close-on-exec pollable input/output duplicates independently and can half-close
its writer without closing its return reader. No production deadlines or
framing code changed in this checkpoint. Those failure controls alone do not
establish accepted-intent replay or pending termination recovery; accepted
replay has separate evidence below, and remaining boundaries stay in the roadmap.

## Accepted replay after a fresh child

`TestFIFOAcceptedReplayAfterFreshChild` runs the actual FIFO mode through the
shared local-stream restart control. A signed request reaches durable accepted
state and the independent native observer verifies its complete Unicode/newline
body, author, room and accepted message ID. After SIGINT shutdown, a reopened
journal confirms durable accepted state, one send attempt, that message ID and
a nonempty upstream ID.

A fresh `serve-fifo` child opens the same pair and journal. An encrypted envelope
replays the exact request ID and intent, including its explicit freshness
window. The replay remains durably accepted with the same message ID and one
attempt. After SIGTERM shutdown, reopening the journal verifies the same
accepted state, message ID, attempt count and upstream ID. Native AIRC CHECK
contains exactly one matching record with the observed author/room/whole body.
This is graceful accepted-intent replay, not pending crash recovery or a
lost-acknowledgement/power-loss durability claim.

FIFO, socket-pair and inherited Unix-stream restart controls passed together
with terminal exit 0 under race detection in 11.440 seconds on Darwin arm64
Go 1.27.1. Integration vet, staticcheck and diff checks passed. The concrete
mode startup selects actual FIFO endpoints for the FIFO case; widening the
shared replay helper does not substitute socket-pair evidence for this mode.

## Pending termination recovery

`TestFIFOPendingTerminationAndFreshChildRecovery` runs the actual FIFO mode
separately for SIGKILL, SIGINT and SIGTERM through the shared local-stream
recovery control. A signed send reaches durable recoverable admission while
an owned relay holds its upstream connection. The relay's actual accept fence
precedes termination; native AIRC CHECK has no message before the signal.

The test signals the live owned child, joins it within the existing three-second
bound, and verifies SIGKILL's exact signal wait status or clean graceful signal
exit. A reopened journal retains a durable recoverable state (queued,
dispatching, retry-wait or uncertain) and a nonempty upstream ID. The relay then
forwards new connections and a fresh FIFO child replays the same stable intent
through an encrypted envelope. The native observer verifies accepted ID,
author, room and the complete Unicode/newline body. After shutdown, reopening
the journal confirms durable accepted state, the same upstream ID, the accepted
message ID and the receipt's attempt count. Native CHECK contains exactly one
matching whole record.

All FIFO controls and the existing socket-pair/inherited pending recovery
controls passed together with terminal exit 0 under race detection in 48.960
seconds on Darwin arm64 Go 1.27.1. Integration vet, staticcheck and diff checks
passed. The FIFO cases start real named FIFO endpoints, while each parent-FD
case retains its own endpoint factory. No production code or deadlines changed
in this checkpoint. This proves recovery of admitted intent not yet witnessed
by native AIRC before termination; it does not prove lost-acknowledgement or
power-loss durability, and Linux runtime qualification remains open.
