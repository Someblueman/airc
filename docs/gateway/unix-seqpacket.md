# Unix sequenced-packet platform boundary

Gateway implementation and native AIRC acceptance remain outstanding. This
checkpoint establishes only which available host can support the connector.

A standalone Go 1.27.1 probe opened a real filesystem `unixpacket` listener and
client in a private temporary directory. On Darwin ARM64/macOS 26.6.2,
`net.ListenUnix` failed with `protocol not supported`; the probe returned its
explicit unsupported status 77. Darwin cannot qualify this mode through the
existing Unix-stream implementation.

The same source was cross-compiled with `CGO_ENABLED=0 GOOS=linux GOARCH=arm64`
and ran successfully in the existing Ubuntu 24.04 ARM64 container, Docker
29.7.2 and kernel `7.0.12-linuxkit`. With no network, a read-only root/artifact
mount and an automatically removed anonymous local-filesystem volume, it
verified complete 16 KiB and 64 KiB sequenced records and `MSG_TRUNC` on an
oversize 16 KiB + 1 record read into a 16 KiB buffer. Terminal exit was 0.

Probe source SHA256:
`23bb529ef8791710d98d9102b37f87632f473f532e8b9bd64826db0af55ef8ab`.
Linux executable SHA256:
`714a82ea6af6be53f79036ab2c14eed8feb9253e694d98b4e54d71cea786f5cb`.
Raw artifacts are `/tmp/airc-seqpacket-availability.go` and
`/tmp/airc-linux-native-159afc4/seqpacket-availability{,.log,.json}`.

The implementation target is Linux filesystem Unix sequenced-packet sockets.
It still needs configured sender authority, private socket ownership and peer
credentials, bounded complete-record authentication, whole response records,
joined lifecycle and independent native delivery/read/replay evidence.
The availability probe does not establish any of those gateway properties.


## Target contract

This is the implementation target, not an enabled gateway mode or acceptance
claim. The roadmap separates the independently testable record adapter from
configuration/service integration and native AIRC acceptance.

The protocol name will be `unixpacket`, available only on Linux. It uses a
filesystem `AF_UNIX`/`SOCK_SEQPACKET` connection, never an abstract socket or a
Unix byte-stream fallback. The configured address must be absolute, normalized
and at most 107 bytes. The adapter will reuse the existing owner-controlled
parent/ancestor checks, refuse existing socket paths and symlink components,
create a 0600 socket and remove it only if the pathname still identifies its
created inode. Retaining the old inode in a replacement test avoids accidentally
testing inode-number reuse as a distinct replacement.

Linux `SO_PEERCRED` must establish the gateway's effective UID before a
connection is served. This is local process ownership, not AIRC principal
authentication: every request still requires the existing encrypted detached
envelope with an authenticated configured sender and authorized operation/
target. Same-UID processes are not mutually isolated by the directory gate.
Responses are clear records on the accepted local connection, as with the
existing owner-only Unix stream carrier. No TLS, return-route provisioning or
AIRC account creation is introduced.

Each request record will contain exactly one UTF-8 JSON `FrameRequest`, with
no stream length prefix. Its maximum is the shared 16 KiB request-frame bound.
The read must inspect truncation flags before JSON or envelope processing;
empty, truncated and malformed records, and records with observable ancillary
data or ancillary truncation, fail without intake.
Two partial JSON records cannot be combined into one request. Existing strict
JSON, correlation, encrypted-profile, operation/target and durable request-ID
checks remain in the shared correlated dispatcher.

Each `FrameResponse` will be serialized into one whole record, bounded by the
shared 64 KiB response-frame limit. A partial or failed write closes that
connection rather than emitting a second record containing a suffix. Concurrent
responses serialize writes and retain their correlation IDs. Capabilities must
name the record transport's supported watch mode rather than claiming a framed
byte stream. Bounded cursor pages, native acceptance receipts and stream budgets
remain governed by the existing dispatcher and journal.

Service ownership must preserve the global connection/request caps and the
accepted connection's record methods. Cancellation closes the listener and
accepted connections, cancels their request work, joins it and releases the
journal afterward. Configuration/capabilities must reject enabled `unixpacket`
on Darwin and other unsupported platforms, while retaining an honest disabled
inventory entry. The Linux availability probe above establishes none of this
application behavior; each acceptance requirement remains in the roadmap.
