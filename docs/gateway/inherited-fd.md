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
Service activation must use existing signed/encrypted frame authentication,
bounded requests and joined delivery/journal ownership. Clear responses on this
local channel can be read by processes that can access the endpoint. The parent
owns connection establishment and any socket-path permissions. Arbitrary files,
network endpoints and general-purpose FD forwarding are outside this contract.

Darwin arm64 adapter controls use a real named Unix connection for bidirectional
I/O, deadline expiry and duplicate ownership. Shared descriptor controls exercise
both inherited-stream and strict socket-pair entry points with nil/file/pipe/TCP/
datagram refusals; an additional unconnected Unix endpoint is refused. The
existing socket-pair named-connection refusal remains intact. Race count-three,
package vet and staticcheck pass. This primitive does not yet activate a CLI or
listener; service/native acceptance and Linux runtime evidence remain in
[ROADMAP.md](ROADMAP.md).
