# Unix sequenced-packet platform boundary

The Linux record adapter is implemented and has actual platform boundary
controls below. Configuration/service integration and native AIRC acceptance
remain outstanding. The initial checkpoint established platform availability
only.

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
The record adapter below supplies private path/peer ownership, encrypted-profile
record handling, whole bounded responses and joined connection lifetime. Mode
configuration/service integration and independent native delivery/read/replay
acceptance are still outstanding. The initial availability probe does not
establish those gateway properties.


## Target contract

This is the connector contract, not an enabled gateway mode or native acceptance
claim. The record adapter is implemented below; configuration/service integration
and native AIRC acceptance remain separate roadmap work.

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
application behavior; full mode qualification remains in the roadmap's
integration item.


## Ancillary descriptor boundary probe

An additional standalone kernel probe passed with terminal exit 0 on actual
Linux ARM64, Docker 29.7.2 and kernel `7.0.12-linuxkit`. On an accepted
filesystem sequenced-packet connection it sent 64 complete records, each
carrying `SCM_RIGHTS` for one owned temporary file descriptor. Each receiver
call used `ReadMsgUnix` with a bounded 16 KiB data buffer and no ancillary
buffer. Every read returned the complete payload, zero delivered control bytes
and `MSG_CTRUNC`, without `MSG_TRUNC`. `/proc/self/fd` contained 11 descriptors
before the sequence and 11 afterward.

This supports a no-ancillary-buffer receive policy that rejects `MSG_CTRUNC`
before dispatch, without installing received file descriptors that would need
cleanup. It does not prove rejection of control data ignored by the kernel,
or implement gateway authentication, request intake or AIRC delivery. Record
adapter controls follow below; native mode acceptance remains outstanding.

The Go 1.27.1 probe was cross-compiled with CGO disabled and ran in the same
existing Ubuntu image with no network, a read-only root/artifact mount and an
automatically removed anonymous local filesystem volume at `/tmp`. It has no
race instrumentation. Probe source SHA256:
`b12233d1e70e4551cc6a15c2a5166582c78f05505cee71ba756d13b2531b20ae`.
Executable SHA256:
`c0700ea14dafb6587ca5385cfb71aa6c5eaa0366a35b2c2ebd885ae6a4146608`.
Source is `/tmp/airc-seqpacket-ancillary-probe.go`; executable, raw log and
private manifest are
`/tmp/airc-linux-native-159afc4/seqpacket-ancillary-probe{,.log,.json}`.


## Record adapter controls

`ListenUnixPacket` and `ServeUnixPacket` implement the Linux record boundary.
The listener shares the existing private-path/inode cleanup and same-UID peer
gate with Unix streams. Every dispatched record requires the existing encrypted
detached profile. One `ReadMsgUnix` with no ancillary buffer reads a bounded
request; truncation/control flags are rejected before parsing or authentication.
One `WriteMsgUnix` emits each serialized response with the production three-
second write deadline. Oversize responses fail before output; failed/partial
writes cancel the connection. Existing correlated dispatch owns permissions,
request identity and request/stream budgets. Watch-mode naming is
`long_poll`/`unixpacket_records`.

Configuration and the service do not yet enable this mode. These controls prove
adapter boundaries and refusal/lifetime behavior, not a successful authorized
AIRC send, complete native conversation read or whole-service journal release.
Those requirements remain in the roadmap's integration item.

Nine new controls and four request-boundary subcases passed on actual Linux
ARM64 in the existing isolated Ubuntu fixture, terminal exit 0:

- Exact 16 KiB request and 64 KiB whole response records; request limit + 1,
  empty record, ancillary descriptor and response limit + 1 refusals.
- Partial JSON records, byte-stream length prefixes, duplicate fields and
  invalid UTF-8 cannot be combined or dispatched as a request.
- Unencrypted signed-profile and global capacity refusals preserve an occupied
  request slot, followed by joined connection cancellation.
- Accepted connections are observed through real record responses, then joined
  on listener cancellation; their socket closes and no unencrypted job exists.
- A real UID-1 child is denied by `SO_PEERCRED`, followed by a successful owner
  connection; fixtures temporarily permit traversal solely to reach that gate.
- Private 0600 socket ownership, active-path refusal, retained distinct-inode
  replacement preservation, close cleanup, unsafe-parent/symlink/existing-file
  refusal and the exact 107-byte pathname bound.
- An unread peer exerts whole-record pressure and reaches the unchanged
  three-second write deadline (3.00s control).

Five existing Unix-stream controls also passed, including its independent real
foreign-UID check. Linux used CGO-disabled cross-compilation and has no race
instrumentation. The new draft was built through Go's file overlay while the
preceding full checkpoint's repository source stayed frozen, then copied into
the repository with a matching source digest. A first Linux run exposed a test
assertion that treated a negative byte count with a read timeout as output; the
corrected assertion requires a timeout and no positive byte count. Production
code was unchanged by that correction, and the original failure artifact/log
remain preserved.

After integration, the full Darwin adapter package passed under race detection
in 23.468s (309 test/subtest passes, one privileged foreign-UID skip). Darwin
explicitly tests the unavailable record API; it does not qualify Linux behavior.
Adapter vet passed for Darwin and Linux; the
[staticcheck build matrix](https://staticcheck.dev/docs/running-staticcheck/cli/build-tags/)
passed for both variants, command builds passed, and formatting/diff checks were
clean. Windows adapter cross-compilation passed with the explicit unavailable
API; no Windows runtime acceptance is claimed.

Integrated Go/module source digest (sorted tracked/untracked paths and content,
each NUL-separated):
`34f4bbd4c190d3788e693160e424d716f90dfc111402e5bd366e56efaf237864`.
Linux adapter-test executable SHA256:
`ffad9bd2df9b7eb8801087b5fc8166c70370ba18ea9089ae689cd1d5f96a28e3`.
Executable/raw log are
`/tmp/airc-linux-native-159afc4/unixpacket-adapter-draft-v3{.test,.log}`.
Overlay/source manifests, integrated Darwin race log and staticcheck output are
under `/tmp/airc-seqpacket-adapter-draft-13c5db7`. The preceding full checkpoint
in [testing.md](testing.md#offline-importer-integration-checkpoint) covers its
recorded source and excludes this adapter. Fresh full acceptance after service
integration remains required.
