# Active-read shutdown and listener rebinding evidence

> Dated evidence. Runs recorded here predate the
> [identity removal](removed-identity.md): their principals, signed or
> encrypted requests and return routes no longer exist.

`TestGatewayServeCLIShutdownJoinsActiveReadLease` runs an actual HTTPS gateway
process against a private native daemon through an owned withholding proxy.
It exercises history, search, watch and directory separately. After the snapshot
command has reached the proxy, SIGINT must join the gateway, upstream lease and
HTTP caller within their existing bounds. The withheld response cannot become
successful, and the proxy must have observed exactly one snapshot command.

Each case now restarts the gateway against the same journal and credentials,
setting the listener to the exact address used by the stopped process. The new
process must report that same ready address. An authenticated HTTP/2 capabilities
request must succeed and still advertise the tested read operation. This verifies
real gateway listener rebinding after an active read, rather than relying on a
different ephemeral address or only an OS bind probe. The existing process helper
owns and joins the restarted gateway during cleanup.

The four cases passed twice under the race detector:

```sh
go test -race ./integration/gateway -run '^TestGatewayServeCLIShutdownJoinsActiveReadLease$' -count=2 -timeout=180s
```

The terminal result was 0 in 22.927 seconds. Affected vet, formatting and diff
checks passed. Output is retained in
`/tmp/airc-roadmap-baseline/active-read-rebind-race-fixed.out` and
`active-read-rebind-vet.out`. The initial build failed because the test used
`core.Capabilities`; the response type was corrected to `dispatch.Capabilities`.
That failure is preserved in `active-read-rebind-race.out`. Production code,
deadlines and durability are unchanged. Individual carrier qualification and
fresh final repository acceptance remain separate; full race acceptance is
still unsatisfied by the earlier cumulative suite timeout.
