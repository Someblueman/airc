# Bounded history and scoped resume evidence

`TestGatewayServeCLIReadOpaqueCursorScopesAndRestart` exercises history, search
and watch through an actual HTTPS gateway process. Each mode requests one record
per page, checks exact native message IDs and body, follows opaque `gc1.` cursors,
and resumes the third page after stopping and restarting the gateway against the
same journal and cursor key. A caught-up resume returns no repeated message.

Its second credential is independently authenticated and authorized for the same
operations and room. Reusing the first principal's cursor with that credential
returns 400. A cursor also cannot move to another allowed room, become a raw
native message ID, or survive tampering. Malformed/ambiguous query parameters,
forbidden targets and invalid credentials are rejected independently.

`TestHistoryBackendRoomPagesGapAndRevocation` owns shared native one-record
paging, whole body/author/account/reply metadata, caught-up position, retention
gap and current revocation checks. HTTP history additionally verifies actual
HTTP/1.1 and HTTP/2 framing, sealed cursor continuation and recent snapshots;
HTTP/2 reuses a caught-up cursor and rejects a valid cursor after revocation.
The retained carrier tests are described in
[history-semantics-testing.md](history-semantics-testing.md).

`TestReadBackendByteOverflowReturnsNoPartialPageAndJoins` supplies sixteen
4096-byte native messages to history/search/watch. Exceeding the whole-page byte
bound returns capacity failure with no messages or cursor and joins the lease.
`TestReadBackendCancellationJoinsWithheldSnapshotDespiteLiveNoise` cancels each
read after its snapshot command, checks bounded return and joined lease, and
verifies no retry or delivery job was created.

These unchanged tests passed in fresh full normal acceptance at executable/test
source `9b217be`, Go 1.27.1 on Darwin arm64:
`go test ./... -count=1 -timeout=900s -parallel=3 -p=2` (terminal exit 0).
Integration passed in 533.051 seconds. Output and timing are retained in
`/tmp/airc-roadmap-baseline/acceptance-9b217be-normal.out` and
`acceptance-9b217be-normal.stderr`. This verifies the shared read boundary;
individual carrier qualification and full race/final acceptance remain separate.
