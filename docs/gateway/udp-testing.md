# UDP unicast acceptance evidence

`TestUDPLostResponseReplayReorderAndSourceReconnect` owns a real packet proxy
on the configured return route. It drops an actual response, replays identical
authenticated intent with fresh transport identifiers, reorders and duplicates
real fragments, and reconnects the source socket. Accepted durable requests must
preserve native IDs with one send attempt.

Late completion of incomplete fragments is rejected after the carrier expiry
window; a fresh retry then succeeds. This scenario also expires packet-source
state and does not isolate the assembler's expiry policy. A separate native CHECK must contain
exactly the three intended complete records with correct IDs, bodies, author and
target. Full history matches those records. Added one-record encrypted mailbox
pages compare every metadata field, require more/complete status, nonempty
cursors and no gaps. Final saved-cursor resume returns an empty complete page.

The shared AGD1 assembler policy is covered separately by
`TestWebTransportDatagramsBoundIncompleteAssembliesAndExpire` in
`internal/gateway/adapter/webtransport_datagram_test.go`.

The witness passed twice under race detection (exit 0, 23.855 seconds):

```sh
go test -race ./integration/gateway -run '^TestUDPLostResponseReplayReorderAndSourceReconnect$' -count=2 -timeout=180s
```

Affected vet, formatting and diff checks passed. Outputs are retained in
`/tmp/airc-roadmap-baseline/udp-qualification-race.out` and
`udp-qualification-vet.out`. This qualifies local Darwin UDP unicast with a
provisioned encrypted mailbox and explicit loss recovery. It does not promise
reliable datagram delivery. Multicast/broadcast and fresh final repository
acceptance remain separate, including the incomplete full race gate.
