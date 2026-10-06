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
are activation/refusal evidence; native signed/encrypted delivery and complete
cursor reads remain unfinished.
