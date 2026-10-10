# Framed TLS acceptance evidence

`TestFramedTLSAuthenticatedSendAndBoundedResume` runs separately with signed and
encrypted request profiles. Each owns a standalone gateway and private native
daemon. The fixture rejects untrusted certificates and wrong server names before
connecting with its private CA. The test requires TLS 1.3, a verified certificate
chain and the framed ALPN. An invalid authentication envelope must fail with a
correlated authentication error and no success result; valid requests must then
work on that same connection.

Both profiles execute the shared IPv4/IPv6 acceptance witness: two durable sends
reach native acceptance and an independent observer checks their IDs, author,
target and complete multiline Unicode bodies. Replaying the original envelopes
must preserve acceptance with one send attempt. One-record history pages must
match those observations with correct more/complete statuses and nonempty
cursors without gaps; final resume must return an empty caught-up page.

TLS and both plaintext IP paths passed twice under race detection (terminal
exit 0, 19.563 seconds):

```sh
go test -race ./integration/gateway -run '^TestFramed(IPv[46]|TLS)AuthenticatedSendAndBoundedResume$' -count=2 -timeout=180s
```

Affected vet, formatting and diff checks passed. Outputs are retained in
`/tmp/airc-roadmap-baseline/framed-tls-race.out` and `framed-tls-vet.out`.
This is local Darwin qualification with private certificates and native Go
clients. It does not establish remote deployment or browser trust. Fresh final
repository acceptance, including the previously incomplete full race gate,
remains outstanding.
