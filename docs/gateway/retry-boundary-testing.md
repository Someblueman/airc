# Shared retry and uncertainty evidence

`TestBackendWireDeadlinesAndFeatures` uses a real native daemon behind an owned
proxy. Stalled registration and unavailable features produce no possible-post
claim. Withheld acceptance still reaches an independent observer and is recorded
as possible acceptance. Every attempt returns within its bounded deadline and
joins its upstream lease; neither a transport write nor a missing receipt is
reported as native acceptance.

The journal tests exercise retry and receipt-only policies, separate send/lookup
budgets, frozen expiry, persistent delay, bounded jitter, and restart without
resetting counters. In particular, `TestCompletionPoliciesBudgetsAndExpiry`
denies sends outside budget/policy and lookups at expiry;
`TestReceiptOnlyRestartAndLookupExhaustion` preserves uncertainty and exhausts
the same lookup budget across reopen. `TestCompletionLookupFallbackAndEarlierUncertainty`
retains uncertainty and duplicate risk on a same-ID resend, and a later permanent
rejection cannot erase possible earlier acceptance. Retained acceptance resolves
that uncertainty. `TestCompletionKnownNoWriteAndPersistentDelay` distinguishes
a known pre-write failure and preserves its scheduled delay across restart.

`TestWireUnknownReceiptCompletionThenSend` checks a real canceled dial and native
unknown receipt response before retrying the same upstream ID. Its observer
checks author, target and body; saved acceptance resolves the prior failure.
`TestDeliveryRecoversLostAcceptanceAfterReopen` recovers real acceptance with
one send and one lookup. The saved-acceptance and failed-save tests described
in [durable-restart-testing.md](durable-restart-testing.md) fence another send
after known acceptance, including when observed receipt bytes still await a
successful save. `TestCompletionSaveFailureAndAcceptanceDominance` prevents a
failure completion from replacing those observed acceptance bytes.

These tests passed in fresh full normal acceptance at executable/test source
`9b217be`, with Go 1.27.1 on Darwin arm64:

```sh
go test ./... -count=1 -timeout=900s -parallel=3 -p=2
```

The command exited 0; output and terminal timing are retained in
`/tmp/airc-roadmap-baseline/acceptance-9b217be-normal.out` and
`acceptance-9b217be-normal.stderr`. This verifies the shared bounded retry and
uncertainty boundary. It does not promise exactly-once delivery when durable
acceptance evidence is lost, or complete individual carrier qualification.
Full race and final acceptance remain separate requirements.
