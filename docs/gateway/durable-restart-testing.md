# Durable intake and stable-ID restart evidence

The shared gateway's restart boundary is covered by complementary real-service,
native-wire and journal subprocess tests. Each fixture owns its state, credentials,
accounts and processes; the native fixtures do not use the live service.

`TestGatewayServeCLIResumesJournalAfterOfflineRestart` hides only its private
native socket, submits through actual HTTPS/HTTP2, and requires durable admission
and retry-wait without acceptance uncertainty. It stops and restarts the gateway
process against the same journal, verifies the pending job and frozen expiry,
restores the socket, and observes acceptance without caller resubmission. The
independent native observer checks exact author, target, body and message ID.

`TestWireReceiptBecomesDurableAcceptance` sends the journal's stable upstream ID
through the public native SDK. After losing the acceptance response before saving,
it reopens the journal and recovers the retained receipt through `RETRY`, without
another send. After saving and reopening again, repeated admission with the same
logical ID replays the same receipt, and a new send reservation is denied.
`TestDeliveryRecoversLostAcceptanceAfterReopen` additionally exercises automatic
worker recovery with exactly one send attempt and one lookup attempt.

`TestConcurrentCoalescingAndRestart` verifies one job from 32 simultaneous equal
requests, conflict rejection, stable upstream identity and frozen defaults after
reopen. `TestProcessDeathPreservesCommittedIntake` exits a subprocess without
journal cleanup and verifies committed intake survives while an uncommitted
deletion does not. `TestAcceptanceProcessDeath` verifies saved acceptance survives
abrupt exit and cannot resend; a failed save instead retains uncertainty and the
consumed send budget. Acceptance and failed-save recovery are distinct outcomes;
these checks do not promise exactly-once delivery after loss of durable evidence.

All these unchanged tests passed in fresh full normal acceptance at executable/
test source `9b217be`, Go 1.27.1 on Darwin arm64:

```sh
go test ./... -count=1 -timeout=900s -parallel=3 -p=2
```

The terminal result was 0; integration passed in 533.051 seconds, store in
5.724 seconds, and total wall time was 536.67 seconds. Output and timing are
retained at `/tmp/airc-roadmap-baseline/acceptance-9b217be-normal.out` and
`acceptance-9b217be-normal.stderr`. This establishes the shared restart boundary;
individual carrier restart qualification and full race/final acceptance remain
separate roadmap requirements.
