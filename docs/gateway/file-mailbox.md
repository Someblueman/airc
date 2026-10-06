# Local message-file mailbox

The owner-private filesystem boundary and authenticated one-request CLI are
implemented and verified for Darwin and Linux, including native AIRC acceptance,
complete bounded cursor reads, stable restart replay, pending-delivery SIGKILL
recovery and joined SIGINT/SIGTERM shutdown. Remaining work lives in the roadmap.

Use one existing, clean absolute directory owned by the gateway's effective UID
with mode 0700. Ancestors must be trusted and must not permit group/other writes,
except root-owned sticky temporary directories. Symlink directories are refused.
The boundary opens an `os.Root`, checks the opened directory against its original
inode, and holds a nonblocking exclusive directory flock until close. A second
consumer cannot own that directory concurrently. Calls are sequential; the
caller owns and closes the boundary.

A producer writes a complete private temporary regular file, syncs it, then
renames it to `request.json` and syncs the directory. Publish only when the
previous input has been claimed and its response has been consumed; there is one
request slot and one result slot. Inputs must be mode 0600, owned by the same UID,
have one hard link, and contain 1 through 16,384 bytes. Empty files, oversized
files, symlinks, hard links, FIFOs, and directories are refused before claim.
Nonblocking/no-follow opens prevent a special-file input from blocking the reader.

Claim renames `request.json` to `processing.json`, syncs the directory and input,
and reads a bounded body. Existing `processing.json` takes precedence over a new
request and survives interruption. Claim alone is filesystem possession; it is
not verified identity, journal admission, or AIRC acceptance. Integration must
verify the complete signed/encrypted intent and preserve its stable request ID
before dispatch. It must replay that intent through the existing durable journal
when processing resumes. Every run revalidates the envelope; retaining a file does
not renew signed validity or bypass revoked credentials. An expired retained
intent is refused; a newly authenticated receipt lookup can still inspect any
already-admitted job.

Result publication requires a retained processing input and 1 through 65,536
bytes. It writes and syncs private `response.tmp`, exclusively links the complete
file as `response.json`, removes the temporary link and syncs the directory, then
removes the processing input and syncs the directory again. An existing response
is never replaced. A failed write leaves the processing input available for
replay. A leftover private temporary file can be discarded and rewritten; it is
never treated as a completed result. A crash after response publication but
before input removal can leave both files: consume the complete response, then
resume the retained signed intent through stable journal replay. An unread result
blocks new claims, bounding the consumer's result footprint.

The caller must distinguish a published durable intake result from a final AIRC
accepted receipt. The filesystem boundary deliberately treats content as opaque;
it does not itself parse a request, authorize a sender, or guarantee delivery.
Directory ownership and mode are rechecked before claim and publication. Private
local files protect clear result contents; no remote shared-directory or transfer
service trust is implied.

Darwin tests exercise exclusive ownership, exact input/result limits, interruption
through close/reopen, refusal without consuming unsafe producer inputs, unchanged
existing results, temporary-write recovery, and permission changes. Process
termination and separate Linux runtime qualification are recorded below.

The adapter package passed its full race suite on Darwin (9.815 seconds).
Adapter vet/staticcheck, formatting, and diff checks passed. The isolated mailbox
controls also passed after their final file organization (package 1.596 seconds).

Configure exactly one enabled listener:

```json
{
  "id": "files",
  "protocol": "file_mailbox",
  "address": "/absolute/private/mailbox",
  "enabled": true,
  "operations": ["send", "receipt.get", "history.read"]
}
```

The address must name an existing directory. TLS, origins, implicit operation
allowlists, watches, and other enabled listeners are refused. The configured
mailbox directory and fixed input/result paths must not alias state or credential
references. Provision signing keys and the usual principal/binding/target
permissions; history reads additionally require the existing cursor key.

Write a version-1 [framed request document](framed-stream-v1.md), without a length
prefix, to the request slot. Its profile must be `signed` or `encrypted`, and its
envelope must satisfy the existing [signed message rules](signed-message-v1.md),
including audience, validity, stable request ID, and authorized principal. File
ownership alone never authenticates a sender. Stream requests are refused.

```sh
airc-gateway mailbox-once --file /absolute/path/gateway.json
```

The command processes one claimed document and exits; it does not watch for new
files or emit readiness/stdout records. Ordinary `serve` rejects this mode before
opening the journal. `response.json` contains one correlated bounded
`FrameResponse`, including authenticated refusal where appropriate. Command
success means the result was published, so callers must inspect its `error` and
result state. An intake response may be durable but still pending. Retain the
signed request ID and query `receipt.get` through another invocation to establish
AIRC acceptance. `history.read` returns the ordinary bounded page and explicit
cursor in `result.page`; each next page requires a newly authenticated request.
Consume/remove the result before submitting the next request.

Handlers retain the ordinary four-second request deadline. The command owns the
journal and delivery runner, allows up to five seconds for pending journal work
after successful publication, then cancels and joins workers before releasing
journal and directory ownership. This grace does not guarantee delivery.
Cancellation before publication leaves the processing intent for a later run;
already-admitted jobs retain their existing stable IDs and uncertain outcomes.
Other platforms explicitly reject this mode.

Dispatcher tests use actual Ed25519 envelopes and SQLite to prove durable intake,
stable upstream ID on repeated intent, signature/profile/stream/version refusal
without a job, and retained input on cancellation. The CLI test publishes an
authentication failure, emits no uncorrelated stdout, then successfully reopens
the journal after command return and finds no pending work. These are boundary
controls, not native AIRC acceptance or process crash evidence.

Gateway internal packages passed race detection on Darwin, including adapter
(12.351 seconds), service (6.767 seconds), and store (35.585 seconds). The command
package passed its final race run (2.178 seconds). Command builds and affected
gateway/CLI vet/staticcheck passed, with formatting and diff checks. Actual CLI termination qualification is recorded below.

Darwin native acceptance on 2026-10-06:
`TestFileMailboxNativeAcceptanceStableReplayAndBoundedHistory` invokes the actual
current gateway executable separately for every document. It atomically publishes
synced request files and consumes each correlated response. One signed and one
encrypted Unicode send reach accepted receipts with one backend send attempt. An
independent native AIRC observer verifies exact author, target and complete body.
Repeating each signed intent through its respective profile in a new command
retains the accepted message ID and attempt count; native CHECK contains exactly
two matching records. This proves clean command restart replay within validity,
not hard-kill or storage rollback recovery.

One-record history pages match every native CHECK record, use explicit nonempty
cursors, retain no gap, and end in an empty caught-up page. Each page is obtained
through a separate authenticated file/command invocation. The signed/encrypted
control passed under race detection (14.61 seconds; package 16.287 seconds), with
integration vet/staticcheck, formatting and diff checks. Fixture baseline:
`96f16c3bbb8a3faac2e69684f591af63ceb58450`; archive SHA-256:
`4f25ee66941bd79c29f936b8deabc120dc0451e5d1b747afee4bbdf593b32829`.
Gateway executable SHA-256:
`5e0d33324da0a08412a69c13076d930e8a3f71a60e0db7721edcabd14d40e210`.

Actual CLI refusal and size controls on Darwin, 2026-10-06:
`TestFileMailboxCLIAuthenticationFreshnessAndSizeRefusal` publishes complete
request documents and runs the current executable. Signature-token rejection, expired
and future validity, the wrong audience, unauthorized targets, and streaming
requests receive correlated refusals. Read-only journal queries find no job for
each refused intent. A 16,385-byte otherwise valid document is rejected before
claim with no result or journal job; the producer removes that refused input.
Native CHECK remains empty. An encrypted document padded with JSON whitespace
to exactly 16,384 bytes then receives durable admission and an accepted receipt
with one send attempt. A freshly joined independent observer verifies the exact
author, room, and Unicode body; native CHECK contains that single matching record.

The shared fixture builds the current gateway before opening observer clients;
the refusal control rejoins its observer after the deliberately quiet refusal
batch. This avoids consuming the existing client's idle read deadline during
builds or command waits without extending production deadlines. The two mailbox
controls passed together under race detection (package 26.132 seconds). The final
exact-limit encrypted recovery control subsequently passed under race detection
(13.37 seconds; package 15.015 seconds), followed by integration vet/staticcheck,
formatting and diff checks. Gateway executable SHA-256:
`3f9a8f885c92f7ef1cebb3bb7f2b4c04031a1f76e2c576a762f87c5a90920f02`.

Review replaced the initial malformed signature token with a structurally valid
compact JWS: the fixture preserves the header and payload, decodes its 64-byte
Ed25519 signature, flips one signature byte, and re-encodes it. The actual CLI
returns authentication failure and the read-only journal query still finds no
job. This exercises signature verification rather than envelope syntax alone.
The corrected control passed under race detection (13.39 seconds; package
15.056 seconds), followed by integration vet/staticcheck and diff checks. Gateway
executable SHA-256:
`1f5cc9f403f58cfd8b7e29f6626fd252569079d9186e14b7d642fdb835a7b72a`.

Pending-delivery process termination on Darwin, 2026-10-06:
`TestFileMailboxPendingDeliveryKillAndSignalRecovery` runs the actual CLI with an
owned private Unix relay that accepts backend connections while withholding
all replies. The command publishes a complete durable pending intake result, and
the relay confirms that a backend connection is held. The test signals the live
child with SIGKILL, SIGINT or SIGTERM. SIGKILL is verified from the child's actual
wait status; the two normal signals exit successfully. Every child joins within
three seconds after the signal, without waiting out the ordinary delivery grace.

After each termination, a new journal owner reads the still-pending durable job
and its nonempty stable upstream ID. The test consumes the published response
and switches the same relay endpoint to forward later connections to the pinned
native AIRC fixture. A new CLI carrying the same intent through encrypted input
retains durable intake, and a subsequent receipt reaches accepted state. The
journal retains the original upstream ID. An independent observer verifies exact
author, target and Unicode body, and native CHECK contains exactly one matching
record. The relay bounds each direction, owns its connections, and joins its
readers on close; no live service or installed daemon is involved.

These controls interrupt after intake-result publication while backend replies
are held. They prove pending journal replay across an actual killed process and
joined signal shutdown. They do not prove hard-kill timing within file rename or
response publication, power-loss durability, rollback recovery, or an accepted
backend result with a lost acknowledgement. The termination control passed under
race detection (13.28 seconds; package 14.930 seconds), followed by integration
vet/staticcheck and diff checks. Gateway executable SHA-256:
`aabf8dad64db13c6b2bab126aa5dc1c9fe67df19ca6c270395cb16f1287879fd`.

All three actual CLI mailbox controls passed together under race detection
(package 34.849 seconds), including both envelope profiles, complete bounded
cursor reads, cryptographic/freshness/authorization refusals, exact body limits,
and the three pending-delivery termination cases. Integration vet/staticcheck,
formatting and diff checks passed. Linux runtime was still unfinished at this
Darwin checkpoint; separate qualification is recorded below.


## Linux message-file native acceptance

The real `mailbox-once` CLI and message-file adapter were separately qualified
on Ubuntu 24.04 ARM64, Docker 29.7.2 and Linux `7.0.12-linuxkit`. The native
run reused the checksum-pinned daemon and digest-verified cross-compiled
executables from the
[Linux FIFO fixture](fifo.md#linux-native-delivery-bounded-reads-and-recovery),
with production source `159afc40c4e7607fe871443002cd39596f3fb41e` and integration
Go/module source digest
`2f3e17077b032078fb140be5ab8678bdac236910bdada0e6da80cf13c0244d01`.
Artifact digests were rechecked before execution. Each run used no network,
a read-only root/artifact mount and an automatically removed anonymous
local-filesystem volume for journal and mailbox state.

`-test.run '^TestFileMailbox' -test.v -test.count=1 -test.timeout=180s`
completed with terminal exit 0 in the actual Linux integration executable:

- Signed/encrypted sends, independently observed exact ID/author/room/whole
  Unicode and newline body, one-attempt accepted replay across one-shot CLI
  restarts, exactly two native records, and complete limit-one cursor pages
  through an empty caught-up boundary (0.65s).
- Pending durable intake through SIGKILL/SIGINT/SIGTERM, verified hard-kill
  status or successful graceful shutdown, bounded joins, fresh journal owner,
  retained upstream identity and encrypted replay producing exactly one
  independently observed native record (2.62s).
- Corrupted signature, expired/future validity, wrong audience, forbidden target,
  streaming-request and oversized-document refusal; native history remained
  empty, then an encrypted exact-limit document reached native acceptance
  with its whole body (0.37s).

The actual Linux adapter executable separately passed seven `^TestFileMailbox`
controls with terminal exit 0 on the same local volume. They verified exclusive
ownership, private/root-mode and symlink boundaries, permission changes,
unsafe/empty/oversized input refusal, exact input/result limits, claim
close/reopen, retained cancellation intent, unchanged existing results and
interrupted temporary publication recovery. Its SHA256 was
`2973c2c8ca376f52e676962ac357e8c7c8a41b64583a5bdc514c67292d1d3c56`.

Raw evidence is in `/tmp/airc-linux-native-159afc4/file-mailbox-native.log`
and `/tmp/airc-linux-native-159afc4/file-mailbox-adapter.log`; native artifact
provenance is `/tmp/airc-linux-native-159afc4/provenance.json`. This qualifies
the Linux message-file mode on this fixture without Linux race instrumentation.
It does not qualify watched directories, append logs, remote transfer services,
lost acknowledgements or power-loss durability. Final full acceptance remains
in the roadmap.
