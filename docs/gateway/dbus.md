# Private D-Bus peer bridge

Plain author requests: the bridge forwards frame bytes unchanged, so a
plain-profile frame to a `mach_ipc` listener in author mode gets a plain
response frame back; no native source change is needed. Not yet run on Darwin
in this model. See [local authors](local-authors.md).

The Darwin ARM64 bridge carries encrypted unary gateway requests over a private
Unix socket using native GIO D-Bus connections. It forwards the exact bytes to
an existing [`mach_ipc` listener](mach-ipc.md), which continues to own envelope
authentication, principal mapping, durable admission, replay and AIRC delivery.

This is the D-Bus peer-to-peer profile. It does not require a bus daemon or use
system/session-bus names, routing or policy. The
[GIO connection flags](https://docs.gtk.org/gio/flags.DBusConnectionFlags.html)
distinguish a peer connection from a message-bus connection. Clients must use
the peer profile; ordinary bus clients that first call `org.freedesktop.DBus.Hello`
are not compatible with this endpoint.

## Build and use

The build requires the existing Apple SDK, `pkg-config`, and GLib development
files exposing `gio-2.0` and `gio-unix-2.0`. The builder creates two native
executables in a fresh private directory. They dynamically link the local GLib
libraries; the builder does not bundle those libraries or install a service.

```sh
./scripts/build-dbus-bridge.sh --output /absolute/path/dbus-bridge
mkdir -m 700 /absolute/canonical/path/private-dbus
/absolute/path/dbus-bridge/dbus-server \
  --socket /absolute/canonical/path/private-dbus/gateway.sock \
  --mach-service org.example.airc.gateway
```

Provision the Mach listener, its UID/principal mappings, encrypted ingress keys
and protected return routes as described in [mach-ipc.md](mach-ipc.md). The
server's upstream bootstrap name is fixed at startup. A request cannot choose
another upstream.

The socket's parent must be a canonical absolute directory owned by the server
UID with no group or other permissions. Existing socket paths, files and
symlinks are refused. The server creates its socket with mode `0600` and writes
`AIRC_DBUS_READY` to standard output after binding. To submit a retained
encrypted `FrameRequest`:

```sh
/absolute/path/dbus-bridge/dbus-client \
  --socket /absolute/canonical/path/private-dbus/gateway.sock \
  < retained-frame.json > sealed-response.txt
```

The client writes only the protected response token to standard output.
Authenticate and decrypt the token using the configured Mach return route and
verify its correlation ID, version and sequence. A successful D-Bus call alone
is not an AIRC receipt: inspect durable admission and use `receipt.get` to
establish native acceptance.

## Caller mapping and wire contract

The server allows only D-Bus `EXTERNAL` authentication and requires non-null
kernel peer credentials whose Unix UID equals its own effective UID. It checks
that UID again before forwarding. GIO exposes those credentials on the
[server side of an authenticated peer connection](https://docs.gtk.org/gio/method.DBusConnection.get_peer_credentials.html).
The Mach kernel then audits the bridge UID, and the existing Mach UID/signing
principal mapping controls the request. No request-supplied UID or additional
bridge signing key is accepted.

Mach sees the bridge PID rather than the D-Bus client PID. PID is diagnostic;
the original caller is identified at the UID level. Direct Mach requests
permitted by the listener remain permitted.

| Field | Value |
| --- | --- |
| Object path | `/org/airc/Gateway` |
| Interface | `org.airc.Gateway` |
| Method | `Exchange` |
| Input | `(ay)`: one byte array containing 1–16,384 bytes |
| Success | `(ay)`: one protected response token containing 1–65,536 bytes |
| Transport failure | A bounded D-Bus error or closed connection; no success receipt |

File-descriptor transfer is refused. The bridge preserves the encrypted frame
and protected response unchanged. Streaming remains unsupported by the upstream
[Mach contract](mach-ipc.md#native-wire-contract). Read calls return complete
bounded pages: verify each protected reply before advancing its opaque cursor,
and continue through the empty caught-up page.

## Retry and limits

After losing a connection or reply, retry the same signed intent and durable
request ID within its freshness window. A request may already have been
admitted. Replay ownership remains in the gateway journal across new clients
and bridge connections.

`--drop-reply` receives and validates the reply shape and size, then discards the
token. It models a caller losing its receipt; it does not establish loss inside
the D-Bus transport.

The CLI has an eight-second process alarm covering input, connection setup,
exchange, output and cleanup. It preserves inherited file-descriptor flags.
Mach send and receive share a four-second deadline; the public bootstrap lookup
is synchronous and cannot be canceled.

The server admits one connection, including authentication, and uses a listen
backlog of four. Authentication expires after five seconds. After authentication
the connection has a fixed thirty-second lifetime and may dispatch only one
method call. Further decoded messages are discarded. An application reply is
flushed asynchronously with a three-second cancellation deadline, then the
connection closes. SDK-generated errors for malformed method signatures can
leave the connection open until the client closes it or its lifetime expires.

SIGINT and SIGTERM stop admission and close the listener. Shutdown cancels
authentication and flushing, joins the Mach worker and connection completion,
then removes the owned socket only if its device/inode identity still matches.
It preserves a replacement at the socket path. An in-flight send can have been
admitted upstream even when shutdown prevents the caller receiving its reply.
The listener closes only after its pending asynchronous accept completes;
closing it immediately after cancellation leaves a stale poll descriptor in
the tested GLib implementation.

GIO parses a message before running a message filter. Therefore the 16 KiB
application limit does not cap the SDK's preceding decoding allocations. This
profile requires a trusted local UID as well as the existing encrypted request
authentication. Shared buses, foreign-UID processes, Linux and independently
installed service profiles require separate qualification.

## Native qualification

The Darwin ARM64 fixture builds and runs the actual native client and server
with strict C11 warnings, AddressSanitizer and UndefinedBehaviorSanitizer, with
sanitizer recovery disabled. GLib warnings are fatal. Qualification uses GLib
2.88.3, Apple Clang 21, macOS 26.6.2 and the macOS 27 SDK. If GLib development
files are absent, the optional native integration tests explicitly skip;
qualification requires those tests to run.

An independent native AIRC observer checks author, target room and exact Unicode
bodies. Complete paginated reads match independent native history through the
empty terminal page; a larger protected reply exceeds 16 KiB. Retrying the exact
frame after discarding its receipt produces one native send.

Refusal checks cover authentication, expiry, audience, profile, correlation,
streaming, UID/principal mapping, method signatures, argument count, size and
file descriptors. Rejected requests leave no journal admission or native post.
Real five-second authentication and thirty-second authenticated-idle expiry
checks prove that a following valid request recovers. Startup and shutdown
checks preserve existing sockets, symlinks and replacement inodes.

The pending-work regression uses a separate native Mach receiver which reports
when it has received the bridge's request, then holds its reply right. The test
disconnects the D-Bus client and terminates the bridge while that Mach call is
pending. The initial listener implementation reproduced a fatal invalid poll
descriptor; tracking asynchronous accept completion before closing the listener
fixes it. The bridge now joins the four-second Mach deadline and exits without
diagnostics. This matches the cancellation and source cleanup ordering in
[GIO's listener implementation](https://raw.githubusercontent.com/GNOME/glib/2.88.3/gio/gsocketlistener.c).
The helper is a lifecycle witness; delivery qualification uses the real gateway
and independent AIRC observer described above.

All 31 D-Bus test/subtest cases passed without D-Bus skips in both full normal
and race suites. The [acceptance checkpoint](testing.md#d-bus-acceptance-checkpoint)
records the unchanged source archive, terminal results and remaining platform
boundaries.
