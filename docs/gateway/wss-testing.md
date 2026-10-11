# WSS acceptance evidence

> Dated evidence. Runs recorded here predate the
> [identity removal](removed-identity.md): their principals, signed or
> encrypted requests and return routes no longer exist.

`TestWSSCLIStableRetryAfterDisconnect` runs signed and encrypted envelopes over
a wss URL with a private CA and an explicitly allowed Origin. It shares the
WebSocket acceptance witness while retaining separately owned fixtures and
connections. Disconnecting before observing admission and replaying identical
intent must preserve durable native acceptance with one send attempt. An
independent observer checks the ID, author, target and complete multiline Unicode
body. A one-record history page and saved-cursor caught-up resume must preserve
the native record, complete status, nonempty cursor and no gap.

`TestWebSocketCLIUpgradeAndEnvelopeBoundaries` rejects untrusted certificates,
wrong server names, disallowed/null/ambiguous origins, upgrade credentials,
tampered signed/encrypted envelopes and expired authentication. Ambient cookies
cannot establish identity; an allowed origin still works after rejection.

These tests and the preserved plaintext WebSocket witness passed twice under
race detection on the final source (terminal exit 0, 15.823 seconds):

```sh
go test -race ./integration/gateway -run '^(TestWebSocketCLIStableRetryAfterDisconnect|TestWSSCLIStableRetryAfterDisconnect|TestWebSocketCLIUpgradeAndEnvelopeBoundaries)$' -count=2 -timeout=180s
```

Affected vet, formatting and diff checks passed. Outputs are retained in
`/tmp/airc-roadmap-baseline/wss-qualification-race-final.out` and
`wss-qualification-vet.out`. The earlier passing run before adding explicit
cleanup for the additional origin-authorized connection is in
`wss-qualification-race.out`. This qualifies local Darwin Go clients with private
certificate trust. Actual browser certificate trust and fresh final repository
acceptance, including the incomplete full race gate, remain separate.
