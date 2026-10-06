# Paired POSIX FIFO contract

Catalogue row 32 will use one dedicated private pair of POSIX FIFOs per gateway
child and one provisioned signed/encrypted sender identity. This is a selected
implementation contract, not an implemented or qualified connector. Remaining
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
before child startup, then connects its input writer. The child must bound
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
