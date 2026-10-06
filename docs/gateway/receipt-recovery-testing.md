# Correlated native receipt recovery evidence

`TestBackendSendAndRetainedLookup` runs the actual backend against a private
native daemon. Its independent joined observer checks the accepted message's
author, target and exact body against the admitted request. The backend cannot
reuse the consumed grant. After losing the backend result before journal saving,
the test reopens the journal and uses a distinct lookup grant and native lease
to recover the same message ID. Saving that receipt yields acceptance with one
send attempt and one lookup attempt; another send reservation is denied.

`TestWireReceiptBecomesDurableAcceptance` separately exercises the public SDK's
stable-ID send and `RETRY` wire path, saves the recovered receipt, and verifies
repeated admission after another reopen returns the same acceptance. It also
checks native reply parent/root linkage with an independent observer.
`TestDeliveryRecoversLostAcceptanceAfterReopen` verifies the worker performs
that recovery automatically without resetting the send budget.

`TestReceiptCorrelationAndFencing` rejects a mismatched request ID, target,
author or body, absent/negative acceptance, unexpected reply linkage, caller
mutation of the consumed target, and a stale attempt marker. These journal
checks complement the actual native receipt observations; a transport
acknowledgement alone never establishes acceptance.

These unchanged checks passed in fresh full normal acceptance at executable/
test source `9b217be`, Go 1.27.1 on Darwin arm64, using
`go test ./... -count=1 -timeout=900s -parallel=3 -p=2` (terminal exit 0).
Output and timing are retained in
`/tmp/airc-roadmap-baseline/acceptance-9b217be-normal.out` and
`acceptance-9b217be-normal.stderr`. This verifies the shared receipt boundary;
carrier qualification, full race acceptance and final acceptance remain separate.
