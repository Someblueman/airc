# Native room search

`GET /v1/search?kind=room&target=%23room&query=CAF%C3%89&limit=50`
uses the pinned server's native Unicode case-insensitive substring search.
`query` is required, bounded to 256 UTF-8 bytes and cannot contain control
characters. Optional `from` filters one valid nickname, normalized to lowercase;
omitted or `*` means any sender. Limit is 1–1000, default 50. Unknown/duplicate
parameters, invalid selectors, `start`, raw cursor IDs and malformed input fail.
Direct-message search remains pending because the upstream inbox selector does
not establish a two-party conversation boundary.

The response shares history's whole-message `messages`, opaque `cursor`, `status`
and `gap` fields. Replies preserve parent/root metadata. Public SDK search events
omit retained receipt facts: absent receipt metadata means unknown, and must not
be interpreted as accepted or persisted. Explicit receipt/status lookup remains
available separately. Search results are not a receipt probe or delivery job.

Initial search starts in retained history. Resume using the returned cursor with
the exact query and normalized sender. The listener's access policy and AIRC's
own room authorization for the author are checked again for every page.
AES-GCM cursor scope includes operation, target, query and sender, in addition
to realm and author scope. Existing history cursor associated
data remains unchanged when the optional search selectors are absent.

An evicted position returns `status: "expired"`, `gap: true`, no records and no
cursor. No fallback silently restarts search. An empty successful search has no
upstream archive watermark; an existing anchored position is preserved, while
an initial empty search returns an empty cursor. It does not invent progress
through nonmatching records. Matching records use the upstream's last matched ID
and explicit `ok`/`more` marker.

Reads use one-use journal authorization and the existing bounded session pool,
leaving a send slot available. Each HTTP read is bounded to four seconds;
registration, cancellation and shutdown close and join its exclusive SDK session.
Records/count and the complete encoded response are bounded to 64 KiB. Overflow,
malformed records/markers, timeout and disconnect return errors without a partial
page, retry or reconnect. `SEARCH=1` and ephemeral mode must be negotiated.

Isolated pinned-daemon evidence covers HTTPS HTTP/1.1 and HTTP/2, Unicode matching,
sender filtering, whole multiline bodies, reply metadata, pagination, empty pages,
query/sender/cross-operation cursor denials, eviction and durable revocation.
Owned proxy tests cover cancellation under continuous unrelated traffic and
byte overflow with joined cleanup and no read-created delivery job. Real owned
race-enabled CLI processes verify search cursor reuse across restart with the
same provisioned key and SIGINT shutdown during a withheld SEARCH snapshot.
