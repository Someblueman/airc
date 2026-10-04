# Bounded room-history backend primitive

The shared backend now implements one room-history snapshot through a public SDK
connection. Bearer-authenticated HTTP room history is now routed through it,
with [authenticated opaque cursors](cursors-v1.md). This remains partial history
support; safe DM mapping and complete adapter acceptance are required.
Room [conversation selectors](conversation-selectors.md) are also available through
HTTP, framed TCP/TLS and WebSocket.

`Store.AuthorizeRead` validates normalized `history.read` intent, exact room
permissions in both the immutable credential and current configuration, and
durable revocation under the store gate. It returns a nonserializable one-use
lease with a fixed fifteen-second deadline and pinned binding. It creates no job,
changes no delivery budget, and does not touch existing CLI cursor files. Closing
the store or reaching the deadline prevents consumption. A lease authorized before
revocation can complete that one snapshot until its original deadline, as with
delivery attempts; revocation prevents new leases.

The backend verifies realm/principal/binding against its immutable startup snapshot,
uses the delivery session pool's read/lookup limits and send reserve, dials once,
requires exact `CHECK=1` and ephemeral registration, and owns the event stream
exclusively. Cancellation closes and joins the SDK session before releasing its
slot. There is no retry, reconnect, account creation or send on this path.
If the global or per-principal session limit is one, reads and receipt probes
return capacity failure without dialing: the only slot is reserved for sends.
Configure both limits to at least two to allow read/probe leases alongside sends.

One fixed CHECK selector requests the authorized room, includes own messages,
and limits records to the requested 1–1000 bound. The collector also stops at
64 KiB of response entry bytes before retaining another entry. It requires one
matching page marker, valid status/cursor/association metadata, unique valid
message IDs, valid sender names, exact room matches, bounded UTF-8 bodies and
timestamps. Foreign/malformed/incomplete snapshots fail without returning any
records. Whole record metadata, upstream order, cursor, `ok`/`more` status and
the explicit retention gap survive. The encoded page has a separate 64 KiB cap;
exceeding a cap returns capacity failure rather than silently truncating a page.
An empty upstream cursor selects recent history; `*` starts retained history.
These raw upstream positions are internal and are not external permission tokens.

The pinned daemon's nickname history selector is an entire destination inbox,
not a two-party conversation selector. This primitive rejects direct targets.
A safe complete DM history mapping remains implementation work, not a physical
transport limitation or permission to omit DM history from the final gateway.
The real account-replacement probe in [dm-read-boundary.md](dm-read-boundary.md)
also verifies that nickname/available sender metadata cannot identify the
historical recipient account. Direct read targets stay rejected until that
ownership prerequisite has a concrete safe implementation.
Room watch, search and directory reads also use the shared backend. Other native
operations and the remaining connection catalogue still require implementation.

`GET /v1/history?kind=room&target=%23room&limit=50` returns retained history from
the beginning by default. `start=recent` instead selects the most recent bounded
snapshot. Omit `start` when resuming with the opaque returned `cursor`. Limit
defaults to 50 and accepts 1–1000, subject to the 64 KiB whole-page cap. Only
kind, target, limit, cursor, initial start and paired `parent_id`/`thread_mode`
conversation selectors are accepted; duplicates, unknown
parameters, malformed values or combining start with a resumed cursor fail.
The HTTP read context is four seconds, within the server's write deadline.
Results preserve messages, opaque cursor, status and gap; HTTP never returns
a raw resumption ID or silently truncates a page. Encoded cursor overhead and
the response newline also count toward the response cap. A pre-revocation lease
may finish its bounded read; later pages fail current authorization.

Native HTTPS HTTP/1.1 and HTTP/2 tests verify observed posts in returned history.
HTTP/2 additionally verifies reply/root metadata, multiline bodies, recent and
retained paging, empty caught-up pages, eviction gaps and durable revocation.
An owned CLI restart resumes a saved cursor under the same key. SIGINT during an
active withheld snapshot joins the caller and upstream lease without reconnecting.
Other principals,
targets, raw IDs and tampered/duplicate/unknown query fields are denied.

Real immutable-daemon evidence covers multiline/own messages, forward pagination,
empty caught-up pages, retention eviction/gaps, forbidden room selection and
pre/post-revocation leases. An owned relay withholds CHECK responses while emitting
continuous unrelated traffic: cancellation returns failure, joins both relay
directions, issues exactly one CHECK, and leaves no delivery job. A real oversized
snapshot returns capacity failure with no partial page and joins its session.
Unit tests cover
one-use concurrent consumption, deadline, caller mutation, credential/current
permission intersection, raw cursor validation and malformed/oversized pages.

```sh
go test -race -count=1 ./integration/gateway -run '^TestHistoryBackend'
go test -race ./internal/gateway/store ./internal/gateway/backend
```
