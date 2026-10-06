# HTTP/3 acceptance evidence

`TestHTTP3ReplayAndWholeNativeHistorySearchContext` uses the standalone gateway
with an actual HTTP/3 client and private native daemon. Every HTTP call requires
HTTP/3, TLS 1.3 and h3 ALPN. Durable send and reply acceptance are compared with
an independent observer's IDs, author and target. Original stable-ID submissions
preserve acceptance and one send attempt; conflicting intent is rejected.

A separate native CHECK establishes exactly two complete metadata records.
Existing history, search, context and watch snapshots compare those records.
Added one-record history pages compare every metadata field with native CHECK,
require correct more/complete statuses, nonempty cursors and no gaps. Two final
cursor resumes must return empty complete pages. The root body includes Unicode
and a newline; reply linkage remains part of the complete record comparison.

Replay/read qualification and existing signed/encrypted profiles, no-job denial,
authentication/origin/query and wrong-TLS-authority tests passed twice under race
detection (terminal exit 0, 18.200 seconds):

```sh
go test -race ./integration/gateway -run '^(TestHTTP3ReplayAndWholeNativeHistorySearchContext|TestHTTP3SignedAndEncryptedProfiles|TestHTTP3DeniedAndUnknownRequestsCreateNoJobs|TestHTTP3RejectsAuthOriginAndQueryBoundaries|TestHTTP3RejectsWrongTLSAuthority)$' -count=2 -timeout=180s
```

Affected vet, formatting and diff checks passed. Outputs are retained in
`/tmp/airc-roadmap-baseline/http3-qualification-race.out` and
`http3-qualification-vet.out`. This qualifies the local Darwin Go client over
loopback QUIC. Remote-network and browser evidence remain separate. Fresh final
repository acceptance, including the incomplete full race gate, remains open.
