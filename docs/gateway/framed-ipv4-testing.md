# Framed TCP IPv4 acceptance evidence

`TestFramedIPv4AuthenticatedSendAndBoundedResume` runs the standalone gateway
against a private native daemon and asserts that the framed connection uses IPv4.
Two encrypted authenticated sends must receive durable admission and native
acceptance. An independent native observer checks each accepted message's ID,
author, target and complete multiline Unicode body. Replaying each original
encrypted envelope must preserve the accepted ID and one send attempt.

History reads use a one-record limit. Both pages must preserve the independently
observed records, report the correct more/complete status and return a nonempty
cursor without a gap. Resuming the final cursor must return an empty caught-up
page with a valid cursor. The fixture exposes its private native socket to the
observer; existing fixture callers otherwise retain their behavior.

The focused test passed twice under race detection (terminal exit 0, 11.229s):

```sh
go test -race ./integration/gateway -run '^TestFramedIPv4AuthenticatedSendAndBoundedResume$' -count=2 -timeout=180s
```

Existing framed CLI authentication, framing limits, TLS, IPv6 and affected
agent/channel fixture checks also passed under race detection (exit 0, 26.789s):

```sh
go test -race ./integration/gateway -run '^(TestFramedCLI|TestAgentsReadUnixAndTLSFramed$|TestChannelsReadOverSignedTLSFramed$)' -count=1 -timeout=180s
```

Affected vet, formatting and diff checks passed. Outputs are retained under
`/tmp/airc-roadmap-baseline/framed-ipv4-race.out`,
`framed-ipv4-regression-race.out` and `framed-ipv4-vet.out`.
This qualifies the local Darwin IPv4 path. IPv6 and TLS have separate roadmap
acceptance items. Fresh final repository acceptance remains outstanding, including
the full race gate that previously hit the cumulative suite timeout.
