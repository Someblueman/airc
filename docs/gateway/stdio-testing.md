# Framed stdio acceptance evidence

> Dated evidence. Runs recorded here predate the
> [identity removal](removed-identity.md): their principals, signed or
> encrypted requests and return routes no longer exist.

`TestStdioPipeAuthenticatedSendAndBoundedResume` starts the standalone gateway
with owned anonymous stdin/stdout pipes, a private native daemon and an already
joined independent observer. Two encrypted authenticated sends must receive
durable admission and native acceptance. The observer checks exact IDs, author,
target and complete multiline Unicode bodies. Replaying each original envelope
must preserve its accepted ID and one send attempt.

One-record history pages must preserve those observations, correct more/complete
status, nonempty cursors and no gaps. Final saved-cursor resume must return an
empty complete page. Closing the owned stdin pipe must join the process cleanly.
Existing signed-profile rejection, encrypted authentication, EOF and partial
frame tests retain the pipe failure boundaries.

The acceptance and boundary tests passed twice under race detection on final
source (terminal exit 0, 21.728 seconds):

```sh
go test -race ./integration/gateway -run '^(TestStdioPipeAuthenticatedSendAndBoundedResume|TestStdioPipeExecutesSignedEncryptedAndEOFBoundaries|TestStdioPipeRejectsPartialFramesWithoutAdmission)$' -count=2 -timeout=180s
```

Affected vet, formatting and diff checks passed. Outputs are retained in
`/tmp/airc-roadmap-baseline/stdio-qualification-race-final.out` and
`stdio-qualification-vet.out`. The initial focused passing run is in
`stdio-qualification-race.out`. This qualifies local Darwin owned-pipe behavior.
Fresh final repository acceptance, including the incomplete full race gate,
remains outstanding.
