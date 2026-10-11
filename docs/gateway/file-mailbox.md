# Local message-file mailbox

Plain author requests: `request.json` may hold a plain-profile frame naming its
author; with `bound_author` a frame without an author runs as that configured
nickname, whose token stays in an owner-only file. The private directory is the
access gate. See [local authors](local-authors.md).

The one-request CLI is implemented and qualified on Darwin and Linux,
including native AIRC acceptance, bounded cursor reads, stable restart replay,
pending-delivery SIGKILL recovery and joined SIGINT/SIGTERM shutdown.

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
request and survives interruption. Claim is filesystem possession, not journal
admission or AIRC acceptance. The request is admitted under its stable request
ID, and a retained processing file is replayed through the journal when
processing resumes, so a retry never creates a second job.

Result publication requires a retained processing input and 1 through 65,536
bytes. It writes and syncs private `response.tmp`, exclusively links the complete
file as `response.json`, removes the temporary link and syncs the directory, then
removes the processing input and syncs the directory again. An existing response
is never replaced. A failed write leaves the processing input available for
replay. A leftover private temporary file can be discarded and rewritten; it is
never treated as a completed result. A crash after response publication but
before input removal can leave both files: consume the complete response, then
resume the retained request through stable journal replay. An unread result
blocks new claims, bounding the consumer's result footprint.

The caller must distinguish a published durable intake result from a final AIRC
accepted receipt. The filesystem boundary deliberately treats content as opaque;
it does not itself parse a request or guarantee delivery.
Directory ownership and mode are rechecked before claim and publication. Private
local files protect clear result contents; no remote shared-directory or transfer
service trust is implied.

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

The address must name an existing directory. TLS, origins, watches and other
enabled listeners are refused. The mailbox directory and its fixed input and
result paths must not alias state or credential references. History reads need
the cursor key. `bound_author` is optional.

Write one version-1 [framed request document](framed-stream-v1.md), without a
length prefix, to the request slot: `profile` absent or `plain` and a
`request` naming its author (or none, with `bound_author`). Stream requests
are refused.

```sh
airc-gateway mailbox-once --file /absolute/path/gateway.json
```

The command processes one claimed document and exits; it does not watch for
new files. Ordinary `serve` rejects this mode before opening the journal.
`response.json` holds one correlated bounded frame, including refusals, so
callers must inspect its `error` and result state. An admitted request may
still be pending: query `receipt.get` with the same author in another
invocation to establish AIRC acceptance. `history.read` returns a bounded page
and cursor in `result.page`; each next page is a new request. Consume the
result before submitting the next request.

Handlers keep the ordinary four-second request deadline. The command owns the
journal and delivery runner, allows up to five seconds for pending journal work
after publication, then cancels and joins workers before releasing the journal
and directory. This grace does not guarantee delivery. Cancellation before
publication leaves the processing input for a later run. Other platforms
reject this mode.

## Evidence

```sh
go test -race -count=1 ./internal/gateway/adapter -run FileMailbox
go test -race -count=1 ./integration/gateway -run 'FileMailbox|LocalAuthorsOverFileMailbox'
```

Adapter tests cover exclusive ownership, exact input and result limits,
interruption through close and reopen, refusal of unsafe inputs without
consuming them, unchanged existing results, temporary-write recovery,
permission changes, durable intake, stable replay and refusals that create no
job. Integration tests run the real executable once per document against a
pinned AIRC fixture: native acceptance checked by an independent observer,
restart replay keeping one message, bounded history pages, size and mode
refusals with no job, SIGKILL after admission recovered by a later run, and
joined shutdown.
