# Local message-file mailbox

The owner-private filesystem boundary is implemented on Darwin and Linux.
Authenticated dispatcher integration, the CLI lifecycle, native AIRC acceptance,
and process crash/restart controls remain on the roadmap. This is not yet a
supported gateway listener or command.

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
when processing resumes.

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
existing results, temporary-write recovery, and permission changes. Actual CLI
and process crash qualification remains separate work; no Linux runtime test has
been recorded yet.

The adapter package passed its full race suite on Darwin (9.815 seconds).
Adapter vet/staticcheck, formatting, and diff checks passed. The isolated mailbox
controls also passed after their final file organization (package 1.596 seconds).
