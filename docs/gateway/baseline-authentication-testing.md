# Baseline authentication and isolation evidence

The real TLS HTTP fixture owns a pinned native daemon, provisioned author,
independent joined observer, gateway journal, credentials and ephemeral listener.
It never uses the live service. The baseline principal maps to `fixture-author`
and `#gateway-fixture`; a separately provisioned bearer principal has only
receipt lookup permission and a different allowed target.

`TestHTTPDurableSubmissionStatusAndReplyOverTLS` negotiates HTTP/1.1 and HTTP/2,
submits through the authenticated endpoint, waits for native acceptance, and
compares the independent observer's exact author, target and body with the
request. Scoped status returns the observed message ID. Stable-ID repetition
coalesces, changed intent conflicts, and an actual reply retains parent/root
linkage.

`TestHTTPDenialsAndScopedLookup` rejects an invalid or absent credential,
duplicate authority, an unconfigured origin and a forbidden target. The second
principal cannot retrieve the first principal's request (404). Strict decoding
rejects caller-supplied account identity. `TestHTTPPUTRejectsIdentityAndPermissionChanges`
also rejects submission by the receipt-only principal and verifies rejected
requests created no job. These checks establish the baseline authentication,
principal-result isolation and target authorization boundaries; they do not
qualify every detached profile or connection mode.

These unchanged tests ran in fresh full normal acceptance at executable/test
source `9b217be`, using Go 1.27.1 on Darwin arm64:

```sh
go test ./... -count=1 -timeout=900s -parallel=3 -p=2
```

The command exited 0; the integration package passed in 533.051 seconds and
the repository run took 536.67 seconds wall time. Captured output and timing
are `/tmp/airc-roadmap-baseline/acceptance-9b217be-normal.out` and
`acceptance-9b217be-normal.stderr`. Full race acceptance is separately pending;
this baseline evidence does not close the final acceptance requirement.
