# Inherited connected Unix-stream descriptors

Catalogue row 34 also includes inherited descriptors. This connector's concrete
contract is a dedicated, already connected AF_UNIX SOCK_STREAM endpoint passed
by the parent. Named Unix connections and unnamed socket pairs are accepted;
TCP, datagram sockets, pipes, files and unconnected Unix sockets are rejected.
The adapter opens no socket path and establishes no connection itself.

`OpenInheritedUnixStream` validates socket type and both Unix endpoints, then
returns an owned close-on-exec duplicate attached to Go's network poller. Reads
and writes support deadlines. Closing the duplicate preserves the source
handle; the caller must close its inherited copy when ownership transfers.
Nonblocking flags may be shared through the open file description, so the parent
must dedicate that endpoint to the child without concurrent reading or writing.

Descriptor possession defines the local channel boundary, not an AIRC identity.
Service activation uses existing signed/encrypted frame authentication,
bounded requests and joined delivery/journal ownership. Clear responses on this
local channel can be read by processes that can access the endpoint. The parent
owns connection establishment and any socket-path permissions. Arbitrary files,
network endpoints and general-purpose FD forwarding are outside this contract.

Darwin arm64 adapter controls use a real named Unix connection for bidirectional
I/O, deadline expiry and duplicate ownership. Shared descriptor controls exercise
both inherited-stream and strict socket-pair entry points with nil/file/pipe/TCP/
datagram refusals; an additional unconnected Unix endpoint is refused. The
existing socket-pair named-connection refusal remains intact. Race count-three,
package vet and staticcheck pass. These descriptor controls alone do not establish native delivery.
Native acceptance and Linux runtime evidence remain in [ROADMAP.md](ROADMAP.md).


Activate the service with `airc-gateway serve-inherited --file CONFIG`. The
parent passes the connected endpoint as descriptor 3 (the first
`exec.Cmd.ExtraFiles` entry). Configure exactly one enabled listener with
`protocol: "inherited"`, empty address and an explicit operations allowlist.
TLS and origin options are rejected. Ordinary `serve` refuses this listener
before journal startup. The gateway owns its inherited source descriptor and
its pollable duplicate until exit. The parent owns its other endpoint and must
read responses while writing requests; no readiness or frames go to stdout.
Diagnostics go to stderr.

This mode shares authenticated framing and joined workers/journal with stdio
and socket pairs. Clean EOF drains admitted handlers and retains the bounded
five-second durable-delivery grace. Partial framing, transport failures and
signal cancellation cancel and join work. Size, correlation and time bounds
remain those in [stdio.md](stdio.md) and [framed-stream-v1.md](framed-stream-v1.md).

Actual Darwin arm64 Go 1.27.1 CLI activation/refusal controls passed under race
detection with socket-pair and stdio startup boundaries (7.985 seconds). The
child executable SHA-256 was
`4098b8428d85e3c2da8bf63724fd352ecc5acef21e52d6b5997d766a4365cc77`.
A parent-established named Unix endpoint was inherited, invalid authentication
received its correlated refusal, and clean EOF exited successfully with no
stdout and released journal ownership for a fresh owner. Missing/regular FD 3
and ordinary-command startup were rejected without journal creation. Full
affected configuration/service/CLI race suites, vet, staticcheck and command
builds also passed. Native delivery is a separate remaining qualification.
