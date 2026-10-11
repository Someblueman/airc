# WebTransport datagram acceptance evidence

> Dated evidence. Runs recorded here predate the
> [identity removal](removed-identity.md): their principals, signed or
> encrypted requests and return routes no longer exist.

`TestWebTransportDatagramRoundTripFragmented` sends encrypted authenticated
fragments in reverse order with a duplicate fragment. It consumes and disregards
the admission response, then repeats the exact envelope under a new correlation
handle and fresh transport packet ID. Receipt lookup through datagrams must
reach native acceptance with one send attempt. An independent observer checks
the exact native ID, author, target and complete body exceeding 3500 bytes.

One-record history and saved-cursor resume also use datagrams. Correlated result
sequence and operation payload must yield the exact observed record, complete
status, a nonempty cursor and no gap, followed by an empty complete page.
The existing missing-fragment test requires no response and no delivery job;
watch tests retain ordered result sequence and lease boundaries. This demonstrates
explicit retry after discarded admission, not reliable delivery of datagrams.

The datagram group passed twice under race detection (exit 0, 13.593 seconds):

```sh
go test -race ./integration/gateway -run '^TestWebTransportDatagram' -count=2 -timeout=180s
```

Outputs are retained in
`/tmp/airc-roadmap-baseline/webtransport-datagram-qualification-race-fixed.out`.
The initial failed run reused transport packet IDs across independent calls;
fresh packet IDs corrected the test while retaining durable intent, with output
preserved in `webtransport-datagram-qualification-race.out`.
Affected vet, formatting and diff checks passed. This qualifies local Darwin
Go-client datagrams. Trusted browser certificate evidence and fresh final
repository acceptance, including the incomplete full race gate, remain open.
