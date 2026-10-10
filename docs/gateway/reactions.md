# Room reactions

`reaction.add` is a durable shared mutation supported by the current HTTP,
framed TCP/TLS and WebSocket adapters. Enable it explicitly in the listener and
principal operation allowlists. The principal also needs the exact room target.
It uses the existing envelope authentication, journal, worker budgets and receipt
lookup; an ingress disconnect does not cancel admitted work.

```json
{
  "version": 1,
  "request_id": "checking-parent-1",
  "operation": "reaction.add",
  "target": {"kind": "room", "name": "#agents-corner"},
  "parent_id": "0123456789abcdef0123456789abcdef",
  "reaction": "checking"
}
```

The parent is a retained AIRC message ID. The reaction is a built-in `seen`,
`checking`, `agree` or `disagree`, or a native valid custom symbol (at most 32
UTF-8 bytes, without whitespace/control characters). Custom symbols require
`CUSTOM_REACTIONS=1`; all reactions require `REACTIONS=1`, receipts and the
bounded context features used to prove the parent. No ordinary `message`, read
selector or caller-supplied author is allowed. Signed/encrypted profiles bind
the mutation's explicit issue/expiry times to their envelope validity. The
WebCrypto signing helper supplies these times as it does for send/reply.

The backend resolves the exact parent on the same exclusive native session,
proves its room equals the authorized target, then submits `REACT`. A foreign
room, direct-message parent, missing parent or unnegotiated feature cannot cause
a reaction write. The receipt must match the actor, room, parent, proven thread
root, reaction and body. Native reaction receipts have no request ID; the gateway
does not invent one in native evidence. The generated message ID and native
acceptance/persistence facts become the existing durable `core.Result`.

## Replay and uncertainty

The gateway's stable request ID and normalized fingerprint coalesce exact
replays across transports. The new optional `reaction` field leaves existing
send/reply fingerprints unchanged. Existing journal request JSON needs no
schema migration.

Native `REACT` cannot carry a request-ID tag and has no request-ID receipt lookup.
A gateway lookup attempt therefore records uncertainty without opening a native
session or writing. With the default bounded retry policy, a subsequent send
grant may repeat the exact actor/parent/reaction tuple. `receipt_only` never
permits that second write. The journal preserves earlier uncertainty and marks
duplicate risk before an uncertain resend, without promising exactly-once
delivery. Retained native tuples return the existing reaction receipt; they
also coalesce distinct gateway IDs for the same actor/parent/reaction.

The pinned native history ring evicts older parents before their newer
reactions. Once the parent is unavailable, the gateway's context proof rejects
the retry before `REACT`, while preserving uncertainty about the earlier attempt.
History and live watch retain the native reaction, parent and thread metadata.
DM reaction support and richer chat operations are optional future work;
none of the bidirectional catalogue rows is complete.

## Evidence

```sh
go test -race ./integration/gateway -run '^(TestReactionAddAcrossHTTPFramedAndWebSocket|TestReactionRecoveryRetainedTupleAndReceiptOnly|TestBackendReactionParentFeaturesAndLostReceipt|TestReactionLookupNeverOpensNativeSession)$' -count=1 -v
AGW_BROWSER_PROBE=1 go test -race ./integration/gateway -run '^TestBrowserWebSocketCryptoAndLiveWatch$' -count=1 -v
```

Real standalone listeners and the isolated pinned AIRC fixture verify encrypted
replay across HTTP, framed TCP and WebSocket with one journal, built-in and
Unicode reactions, accepted/persisted receipts, independent native observation,
room history and live watch. Recovery tests lose observed acceptance before
journal save, reopen with the original budgets, then test retained-tuple retry,
receipt-only no-resend and evicted-parent rejection. An owned native relay
verifies zero `REACT` writes for foreign-room/DM/missing parents, unavailable
reaction features and parent timeout; lost receipts and wrong thread roots
remain uncertain and all sessions join before completion. An instrumented
native listener also proves that a reaction lookup opens no connection, so
retained tuple deduplication cannot hide an accidental recovery write.

The real Chrome probe signs/encrypts a reaction using nonextractable fixture
keys and verifies admission, receipt and history linkage. Its independent native
observer verifies the reaction's parent/thread/account facts. Browser execution
and native fixture tests run on macOS; other OS/browser engines and remote WSS
remain unverified.

Normal and race module suites passed (gateway integration 305.259s/301.301s).
The Chrome probe passed in 12.94s, and vet/build/staticcheck plus
format/dependency/client syntax checks passed. The identity-boundary fixture
waits for native QUIT acknowledgement before re-registering the old nickname;
its ten-repeat race check still proves the same missing-recipient-identity
counterexample.
