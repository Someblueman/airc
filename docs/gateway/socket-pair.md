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
identity. Service activation must require the existing provisioned signed or
encrypted frame authentication and bounded framing. No new listener, command or
native delivery qualification is claimed by the descriptor primitive.

Darwin arm64 adapter tests use real socket pairs for bidirectional I/O, read
deadline expiry and duplicate ownership, plus real rejected descriptor types and
both ends of a named Unix connection. Race count-three tests, package vet and
staticcheck passed. Linux runtime qualification and the remaining service/native
acceptance boundaries remain in [ROADMAP.md](ROADMAP.md).
