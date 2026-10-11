# Session capacity and reserved send evidence

> Dated evidence. Runs recorded here predate the
> [identity removal](removed-identity.md): their principals, signed or
> encrypted requests and return routes no longer exist.

`TestGRPCMultiplexedWatchQuotaAndReservedUnaryCapacity` uses an actual gateway
process, generated TLS gRPC client, private native daemon and independent joined
observer. Its limits are three global/per-principal sessions, two global/
per-principal watches and one worker. Two active watches occupy the whole allowed
read pool; another watch receives capacity failure. Discovery remains available.

Before canceling either watch, the test submits a durable message and waits for
native acceptance. The independent observer checks exact author, target, body
and correlated message ID; the accepted result requires one send attempt.
Native STATUS still reports both held observation leases at acceptance. This
rules out demonstrating a send only after the reads have ended.

After cancellation, the test waits for the observation count to fall to baseline,
then verifies the journal contains exactly the accepted send. It also checks
zero jobs before submission, preserving the no-job claim for reads and capacity
denial. The previous cleanup wait accepted counts at or above baseline and did
not establish lease release; it now uses the existing at-most wait helper.

The fixture accepts explicit limits through a private constructor. All other
gRPC callers retain the same default limits and isolated resources. Production
configuration, capacity accounting and deadlines are unchanged.

Shared backend unit checks independently cover global/per-principal caps,
lookup/watch send reservation and release, including a sole send slot.
`TestFramedGlobalCapacityAndJoinedCancellation` verifies correlated request
rejection without consuming or releasing another request's slot. These unchanged
checks passed in full normal acceptance at `9b217be`; the strengthened real
gRPC fixture adds the missing native acceptance evidence under held reads.

The strengthened test passed three times under the race detector:

```sh
go test -race ./integration/gateway -run '^TestGRPCMultiplexedWatchQuotaAndReservedUnaryCapacity$' -count=3 -timeout=180s
```

It exited 0 in 10.286 seconds. Output is retained in
`/tmp/airc-roadmap-baseline/read-reserve-race.out`. Affected vet, formatting and
diff checks passed. All eleven real gRPC integration tests also passed under
`go test -race ./integration/gateway -run '^TestGRPC' -count=1 -timeout=180s`
in 39.254 seconds (exit 0), including authentication, malformed input, replay,
reads, revocation, quota, slow-reader deadlines and joined shutdown. Output is
`/tmp/airc-roadmap-baseline/read-reserve-grpc-race.out`. Final repository
acceptance remains separate; the earlier full race run timed out and is not a
passing gate for this source.
