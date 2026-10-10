# Darwin Unix-stream acceptance evidence

At this checkpoint, `TestDarwinUnixStreamAuthenticatedSendAndBoundedResume`
was guarded for Darwin runtime. It verifies
a real Unix socket with mode 0600 and its containing directory with mode 0700.
The standalone fixture also rejects duplicate startup on the active socket path
without replacing it and requires socket removal after joined shutdown.

The test runs the shared framed acceptance witness through the Unix connection:
two encrypted authenticated durable sends must reach native acceptance. An
independent observer checks exact IDs, author, target and complete multiline
Unicode bodies. Replay preserves each accepted ID with one send attempt.
One-record history pages must preserve those records, correct more/complete
status, nonempty cursors and no gaps; final resume returns an empty complete page.

The new test, shared Unix operation conformance and framing/encrypted-profile
boundary checks passed twice under race detection (exit 0, 31.166 seconds):

```sh
go test -race ./integration/gateway -run '^(TestDarwinUnixStreamAuthenticatedSendAndBoundedResume|TestUnixStreamSharedOperations|TestUnixStream.*Bound.*)$' -count=2 -timeout=180s
```

Affected vet, formatting and diff checks passed. Outputs are retained in
`/tmp/airc-roadmap-baseline/darwin-unix-stream-race.out` and
`darwin-unix-stream-vet.out`. This qualifies the Darwin runtime path and mode
checks. Linux execution and privilege-gated foreign-UID evidence remain open;
fresh final repository acceptance, including the incomplete full race gate,
remains outstanding.


The current equivalent is `TestUnixStreamAuthenticatedSendAndBoundedResume`,
now built for both supported native platforms. All `^TestUnixStream` controls
passed with default source-built fixtures under Darwin race detection in
21.549s after that rename and the verified framed-example fixture option.
[Linux acceptance](linux-unix-stream-testing.md) is recorded separately.
