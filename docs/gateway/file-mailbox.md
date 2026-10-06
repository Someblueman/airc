# Local message-file mailbox

The owner-private filesystem boundary and authenticated one-request CLI are
implemented for Darwin and Linux. Darwin native AIRC acceptance and bounded
cursor reads are verified. Actual CLI hard-kill and signal-shutdown
controls remain on the roadmap; Linux runtime qualification is not yet recorded.

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
existing results, temporary-write recovery, and permission changes. Process crash qualification remains separate work; no Linux runtime test has
been recorded yet.

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
gateway/CLI vet/staticcheck passed, with formatting and diff checks. Actual CLI crash and signal qualification remain outstanding.

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
