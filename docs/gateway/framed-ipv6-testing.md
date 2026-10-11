# Framed TCP IPv6 acceptance evidence

> Dated evidence. Runs recorded here predate the
> [identity removal](removed-identity.md): their principals, signed or
> encrypted requests and return routes no longer exist.

`TestFramedIPv6AuthenticatedSendAndBoundedResume` connects the standalone gateway
through `[::1]` and requires an IPv6 remote address with no IPv4 representation.
It uses the same acceptance witness as the separately asserted IPv4 path, now
in `integration/gateway/framed_acceptance_test.go`.

Each path owns a private native daemon and gateway. Two encrypted authenticated
sends must receive durable admission and native acceptance. An independent native
observer checks the accepted IDs, author, target and complete multiline Unicode
bodies. Replaying the original envelopes must preserve each accepted ID and one
send attempt. One-record history pages must match those observations, return the
correct more/complete statuses and nonempty cursors without gaps. Final cursor
resume must return an empty caught-up page with a valid cursor.

Both address-family tests passed twice under race detection (terminal exit 0,
14.698 seconds):

```sh
go test -race ./integration/gateway -run '^TestFramedIPv[46]AuthenticatedSendAndBoundedResume$' -count=2 -timeout=180s
```

Affected vet, formatting and diff checks passed. Outputs are retained in
`/tmp/airc-roadmap-baseline/framed-ipv6-race.out` and `framed-ipv6-vet.out`.
This qualifies local Darwin IPv6 framed TCP. TLS remains a separate acceptance
item. Fresh final repository acceptance, including the previously incomplete
full race gate, remains outstanding.
