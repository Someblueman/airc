# Scratch server and client example

From the repository root on Darwin or Linux, with Go installed:

```sh
demo_bin=$(mktemp -d /tmp/airc-demo-bin.XXXXXX)
go build -o "$demo_bin/aircd" ./cmd/aircd
go build -o "$demo_bin/airc-gateway" ./cmd/airc-gateway
go run ./examples/gateway/scratch --native "$demo_bin/aircd" --gateway "$demo_bin/airc-gateway"
rm "$demo_bin/aircd" "$demo_bin/airc-gateway"
rmdir "$demo_bin"
```

The example requires explicit server executable paths. It creates an owner-only
temporary directory under `/tmp`, provisions a fresh native account and gateway
credential, and starts both servers with private state. The native server uses
its own Unix socket and fsync history; the gateway uses a fresh durable journal
and an ephemeral loopback HTTP listener. It changes no installed service or
default daemon state. The HTTP bearer exchange is confined to this local demo.

An independent native observer joins `#scratch`. The client posts a whole body,
waits for native acceptance through scoped receipt lookup, and checks the
observer's exact author, target, body and message ID. It reads a one-message
history page with `limit=1`, then resumes the returned opaque cursor and requires
an empty caught-up page. Transport admission alone cannot satisfy the example.

Successful output is one JSON object, with a fresh message ID:

```json
{"author":"scratch-author","body":"scratch whole body\nhello 🙂","first_page_messages":1,"message_id":"<native message ID>","resumed_page_messages":0,"target":"#scratch"}
```

The program interrupts and joins the gateway and native server and removes its
temporary state before successful exit. An error returns nonzero. The optional
binary cleanup commands above remove only the two explicitly built executables.

The freshly built current servers completed this example on Darwin arm64. Its
real command integration test additionally runs `go run` with race instrumentation
matching the parent test, a checksum-verified pinned native server, and the
current gateway. This is local scratch evidence; Linux execution and individual
carrier qualification remain separate requirements.

```sh
go test -race ./integration/gateway -run '^TestScratchServerClientSendAndReadExample$' -count=1 -timeout=180s
```

The final command test exited 0 in 7.180 seconds; affected vet, formatting and
diff checks passed. Captured results are
`/tmp/airc-roadmap-baseline/scratch-example-final-race.out`,
`scratch-example-final-current.out`, `scratch-example-final-current.stderr`
and `scratch-example-final-vet.out`. The current-server `go run` command also
exited 0. Fresh final repository acceptance remains outstanding, including the
unresolved cumulative timeout in the previous full race run.
