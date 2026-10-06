# Unix sequenced-packet platform boundary

The Linux record adapter is implemented and has actual platform boundary
controls below. Explicit Linux configuration and service ownership are also
implemented. Independent Linux native AIRC acceptance is verified below. The
initial checkpoint established platform availability only.

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
acceptance are verified below. The initial availability probe alone does not
establish those gateway properties.


## Target contract

This is the connector contract. The record adapter and explicit Linux service
mode are implemented below; native AIRC acceptance has separate evidence below.

The protocol name is `unixpacket`, available only on Linux. It uses a
filesystem `AF_UNIX`/`SOCK_SEQPACKET` connection, never an abstract socket or a
Unix byte-stream fallback. The configured address must be absolute, normalized
and at most 107 bytes. The adapter reuses the existing owner-controlled
parent/ancestor checks, refuses existing socket paths and symlink components,
creates a 0600 socket and removes it only if the pathname still identifies its
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

Each request record contains exactly one UTF-8 JSON `FrameRequest`, with
no stream length prefix. Its maximum is the shared 16 KiB request-frame bound.
The read must inspect truncation flags before JSON or envelope processing;
empty, truncated and malformed records, and records with observable ancillary
data or ancillary truncation, fail without intake.
Two partial JSON records cannot be combined into one request. Existing strict
JSON, correlation, encrypted-profile, operation/target and durable request-ID
checks remain in the shared correlated dispatcher.

Each `FrameResponse` is serialized into one whole record, bounded by the
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
application behavior; the native qualification below supplies separate evidence.


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

These adapter controls prove refusal and lifetime boundaries. Whole-service
ownership and successful authorized native AIRC sends and complete conversation
reads are verified separately below.

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
  three-second write deadline (3.01s control).

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


## Configuration and service ownership

Linux explicitly enables `protocol: "unixpacket"` through the ordinary `serve`
command. The address is an absolute normalized filesystem socket path of at
most 107 bytes. Origins and TLS are refused. Enabled configurations are rejected
on Darwin and other unsupported platforms before opening the journal; disabled
capabilities report `disabled_unavailable`. Linux capability discovery reports
`enabled_unixpacket_partial` and only configured implemented operations without
creating a socket or state directory.

The service preserves Unix record methods through the global connection limiter.
Closing its wrapper releases the connection slot once. Requests use the existing
global request budget. Linux service tests exchanged a complete encrypted-profile
refusal record over the actual enabled listener, kept the accepted connection
open through cancellation, observed EOF after the joined shutdown, and verified
socket removal and journal reopening. A later listener bind failure likewise
removed the earlier sequenced-packet socket and released the journal. The two
existing Unix-stream lifecycle controls passed independently.

Configuration, read-only CLI discovery (five subcases), and four service lifecycle
controls passed with terminal exit 0 on actual Linux ARM64 in the same existing
isolated Ubuntu image described above. Go/module source digest:
`d6d0cc7a7e1cc2d867ab4e123429d57f31d5a408ba69452831bcd67f1df5822e`.
Service executable SHA256:
`17ce5d91b50518550ff073a3632c350c83cc3e7009b1308edccd17de0c00e63f`.
Artifacts and executable hashes are in
`/tmp/airc-linux-native-159afc4/unixpacket-service-v4-provenance.json` and
`unixpacket-service-controls-v4.log`. The earlier failed test assertion is retained
in `unixpacket-service-controls.log`: it incorrectly required all receive flags
to be zero, including Linux's non-truncating `MSG_CMSG_CLOEXEC`. The corrected
assertion checks payload and ancillary truncation; production behavior did not
change for this correction.

Affected config, CLI and service packages passed Darwin race tests. Darwin/Linux
vet, the two-platform staticcheck matrix, command builds and diff checks passed.
Linux execution was CGO-disabled and has no Linux race claim. These checks do
not establish native author/target/body, cursor continuation or replay; the
following controls supply that evidence. The final full-source gate remains open.


## Native acceptance

Two independent Go-client controls passed on actual Linux ARM64 in the same
existing isolated Ubuntu image, terminal exit 0. The client uses one
`WriteMsgUnix` per request and one bounded `ReadMsgUnix` per response, checks
payload/ancillary truncation and never assembles a byte stream. The tests run the
current gateway process and checksum-verified pinned native AIRC daemon with
separate state, accounts, keys and observers for each control.

`TestUnixPacketNativeAcceptanceRestartReplayAndBoundedHistory` passed (0.81s):

- Two encrypted intents reached native acceptance. The independent AIRC observer
  checked exact `fixture-author`, room target and multiline Unicode body for
  each accepted message ID.
- Each send had one physical attempt. The whole gateway was stopped and restarted
  before replaying the same signed intent with fresh encryption and a new
  connection; durable acceptance retained the same message ID and attempt count.
  Independently read native history contained exactly two messages.
- Before each shutdown an additional connection exchanged a signed-profile
  refusal, proving it was accepted. It remained open until cancellation; the
  joined gateway exited successfully, that connection observed EOF and the
  socket path was removed. Restart reused the journal successfully.
- Limit-one history reads returned exact native records in order, explicit `more`
  then `ok`, no gaps and nonempty opaque cursors. Explicit continuation reached
  an empty bounded end page; every complete page occupied one response record.

`TestUnixPacketNativeRecordSizeProfileAndTargetRefusals` passed (0.27s):

- Signed-only ingress returned `invalid_request`; an encrypted unauthorized
  room target returned `forbidden`, with matching correlations.
- A valid encrypted request padded to 16 KiB + 1 and a partial JSON record each
  closed before intake. Read-only journal checks found no refused intent under
  any principal, and independent native history stayed empty.
- A valid encrypted request padded to exactly 16 KiB then reached acceptance
  with one attempt. The observer checked its exact author, target and Unicode
  body; independent native history contained precisely that one message.

Source Go/module SHA256:
`92430d1ec90977d963a273e88e11965003a876c4555dc412752e54a1bcc1b4f5`.
Current Linux gateway SHA256:
`31f0b34a18bae9ed605326245a2c9ff74cfd631db629ce676c54bd652400f123`.
Linux integration executable SHA256:
`8b2d0439f1d62b57c4d9ac1fda86e3b277cdb0b08fd0a27f6eebd626b2385779`.
Native daemon SHA256:
`1f3aa4a566a23d4326e2a2b3ed81942b5384e17521dc1bf38572511778d7e85f`.
Its source baseline is `96f16c3bbb8a3faac2e69684f591af63ceb58450`, with source
archive SHA256
`4f25ee66941bd79c29f936b8deabc120dc0451e5d1b747afee4bbdf593b32829`.
The native daemon and archive hashes were independently verified before running.
Artifacts are under `/tmp/airc-linux-native-159afc4`: `unixpacket-native.log`,
`unixpacket-native-provenance.json`, `unixpacket-native.test` and
`unixpacket-native-gateway`. The fixture copies and verifies executable SHA256,
Go program identity and Linux ARM64 build settings before executing owned copies.

Linux vet and the Darwin/Linux integration staticcheck matrix passed. Linux used
CGO-disabled cross-compilation; no Linux race instrumentation is claimed. These
controls qualify the local Linux native record mode with the earlier adapter,
peer and service lifecycle evidence. Darwin remains explicitly unsupported.
They do not qualify a remote carrier, watch parity, every optional AIRC operation
or the still-required fresh full-source normal/race acceptance checkpoint.
