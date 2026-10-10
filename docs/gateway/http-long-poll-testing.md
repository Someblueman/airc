# HTTP long-poll acceptance evidence

`TestHTTPLongPollQuietDeadlineAndCancellationJoin` exercises HTTP/1.1 and HTTP/2
against a private native daemon through an owned proxy. Each request must reach
native subscription and snapshot before cancellation or quiet-result checks.
Continuous unrelated native noise cannot extend the two-second idle timer:
quiet polls must return an empty complete page without invented cursor or gap,
using the selected HTTP protocol, between 1.5 and 4 seconds after request start.
The lower bound rejects immediate-return behavior. Client cancellation must
return an error. Every case requires the native lease to join after return;
cleanup also cancels and joins the HTTP caller.

The existing `TestHTTPWatchNativeWakeOpaqueScopeAndRevocation` verifies live
native wakeup, exact independent observer metadata, opaque cursor scope, denial
and revocation over both HTTP versions. `TestHTTPWatchQuietPollAndRetainedResume`
retains quiet result, bounded retained pages, cursor resume and retention-gap
acceptance.

All three tests passed twice under race detection (exit 0, 20.850 seconds):

```sh
go test -race ./integration/gateway -run '^(TestHTTPLongPollQuietDeadlineAndCancellationJoin|TestHTTPWatchNativeWakeOpaqueScopeAndRevocation|TestHTTPWatchQuietPollAndRetainedResume)$' -count=2 -timeout=180s
```

After adding the lower timing bound, the changed test passed twice again
(exit 0, 11.772 seconds):

```sh
go test -race ./integration/gateway -run '^TestHTTPLongPollQuietDeadlineAndCancellationJoin$' -count=2 -timeout=180s
```

Affected vet, formatting and diff checks passed. Outputs are retained under
`/tmp/airc-roadmap-baseline/http-long-poll-race.out`,
`http-long-poll-boundary-race.out` and `http-long-poll-vet.out`.
This qualifies local Darwin HTTP long polling. Fresh final repository acceptance,
including the previously incomplete full race gate, remains outstanding.
