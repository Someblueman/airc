# Pinned AIRC fixture contract

Gateway development uses the existing module `github.com/Someblueman/airc` in
`/Users/sws/Code/airc-gateway`, branch `codex/connection-gateway`. The initial
gateway tree was clean at planning commit `66f8f2b`. The primary checkout has
unrelated dirty work and is outside the implementation boundary.

The immutable fixture baseline is
`96f16c3bbb8a3faac2e69684f591af63ceb58450`. Its full `git archive --format=tar`
export has SHA-256
`4f25ee66941bd79c29f936b8deabc120dc0451e5d1b747afee4bbdf593b32829`.
The existing module requires Go 1.27.0; the verified host runs Go 1.27.1 on
darwin/arm64. No module or dependency change is needed for this fixture.

Run from the gateway worktree:

```sh
go test -count=1 -v ./integration/gateway
go test -race -count=1 ./integration/gateway
go vet ./integration/gateway
```

The test verifies the export checksum and builds `cmd/aircd` from that export
once per test process. Only the immutable executable is shared; `-trimpath`
prevents temporary source paths from multiplying native build-cache entries.
Every fixture starts its own child with a short Unix socket path in an owner-only temporary
directory, provisions a disposable account, and connects an independent room
observer. All child runtime environment paths and persisted files belong to
the fixture. Cleanup closes clients, interrupts and waits for the owned PID,
checks listener closure, and removes only its temporary directory. Forced
termination is a test failure. No installed binary or default TCP listener is
used. Source notices remain in the unmodified export.
The test process removes the shared export/executable after all fixture cleanup
has finished. A real two-daemon control verifies that the first fixture's account
cannot log in to the second and its retained message is absent there.

Verified public SDK contracts:

- `DialContext` supports explicit Unix addresses, fixture account creation,
  SASL account login, and ephemeral registration. The fixture account token
  must be 64 hexadecimal characters. Gateway sessions must disable account
  creation and reconnection and explicitly close their clients.
- `Features()` negotiates `RECEIPTS=1`, `IDEMPOTENCY=1`, `SAFE_RETRY=1`, and
  `REPLIES=1` on this retained-history fixture. `Ephemeral()` confirms the
  negotiated mode. Feature presence alone is insufficient.
- `SendWithID` supports multiline normalized UTF-8 content. A correlated
  `SendReceiptEvent.RequestID` confirms acceptance. Its receipt separately
  reports accepted and persisted, and the independent observer verifies
  author, target, content, and message ID.
- `RetryRequest` returns the retained receipt without creating a post;
  repeating `SendWithID` with the retained ID returns the original message ID.
- `ReplyWithID` returns an accepted/persisted receipt with the parent and root
  thread IDs; the independent observer sees the same author, room, reply body,
  and linkage.

These checks establish the fixture and this narrow SDK boundary. They do not
prove the gateway dispatcher, durable journal, fault recovery, history/watch
authorization, any ingress adapter, or native hardware support. Those remain
separate acceptance requirements in the implementation plan.
