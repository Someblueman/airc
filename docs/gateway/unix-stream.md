# Unix stream intake

Catalogue row 28 uses the existing versioned framed protocol over a filesystem
Unix stream socket. This partial adapter carries all current shared operations:
send, reply, room reaction.add/context.read/pins.read/room.read/waiting.read/poll.results,
receipt.get, room history.read/search/watch and public directory. Authenticated
capabilities.get reports configured intake. Remaining native functionality and
DM reads are pending across the shared gateway; Unix transport does not prevent
those operations and does not reduce the catalogue's scope.

## Configuration and local boundary

Configure an explicitly enabled listener with `protocol: "unix"`, an absolute
normalized `address` and explicit operations. TLS and browser-origin fields do
not apply. Existing principal keys, exact targets, account bindings, journal,
retry budgets, cursor keys, session quotas and watch quotas retain their meaning.

```json
{
  "id": "local-unix",
  "protocol": "unix",
  "enabled": true,
  "address": "/OWNER/private/gateway.sock",
  "operations": ["send", "reply", "reaction.add", "context.read", "pins.read", "room.read", "waiting.read", "poll.results", "receipt.get", "history.read", "search", "watch", "directory"]
}
```

The socket's immediate parent must belong to the gateway's effective UID and
have no group/world access. Every ancestor must belong to root or that UID;
writable ancestors are rejected except root-owned sticky directories such as
the real `/private/tmp` on macOS. Symlink components are rejected. Provision a
real directory path rather than a symlink alias, and keep the socket path short
enough for the host's native Unix address limit.

The listener creates a socket with mode `0600`, checks its owner and inode, and
accepts only peers with the gateway's effective UID. Darwin uses LOCAL_PEERCRED;
Linux uses SO_PEERCRED. These checks establish a local process boundary, not an
AIRC principal: every frame still requires the encrypted signed-envelope
profile. Signed-only and bearer requests are unavailable on this carrier.
Response frames are plaintext within this same-user local boundary.

Any existing path is refused, including a stale socket. There is no automatic
unlink/recovery policy. Go's automatic Unix unlink-on-close is disabled. Normal
shutdown and setup failures remove the socket only if the path still refers to
the inode created by this listener; a replacement inode is preserved. A later
listener bind failure closes earlier owned listeners and releases the journal.

Darwin and Linux have compiled credential implementations. Enabled Unix
listeners fail config validation on other platforms before journal creation;
CLI discovery distinguishes unavailable platform support from a disabled
listener. Other Unix carriers and abstract namespace modes need their own
implementation and evidence.

## Client

The existing framed example accepts a Unix network explicitly:

```sh
go run ./examples/gateway/framed-client \
  -network unix -address /OWNER/private/gateway.sock \
  -profile encrypted -correlation request-1 < /OWNER/private/request.jwe
```

The envelope is already signed/encrypted and bounded. The client does not
provision identities or select an upstream account. Use `-stream` with an
authenticated watch envelope for correlated page/end frames. Framing limits,
watch leases, cursor resume, cancellation by connection closure and response
serialization are the same as [framed-stream-v1.md](framed-stream-v1.md).

## Verification boundaries

Darwin lifecycle tests exercise same-UID connections, private-path and symlink
rules, existing-path preservation, replacement-inode preservation, normal
shutdown and mixed-listener startup rollback. Real standalone gateway tests use
an immutable disposable AIRC daemon, a Unix framed client and the example CLI.
They check shared conformance, native mutation observations, whole pin/context
and waiting snapshots, authentication, framing closure and socket cleanup.

Foreign-UID credential rejection has a root-gated subprocess test. It is skipped
on the current UID-501 host; noninteractive privilege access is unavailable.
Linux arm64 and Windows amd64 adapter tests cross-compile, but were not executed.
The installed Docker client has no running daemon, so Linux runtime acceptance
remains open. Windows uses the unsupported Unix implementation; cross-compilation
does not claim Windows Unix listener support.
