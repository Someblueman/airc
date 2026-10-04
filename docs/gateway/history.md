# Bounded room-history backend primitive

The shared backend now implements one room-history snapshot through a public SDK
connection. This is an internal primitive, not an enabled HTTP operation or a
complete history/watch adapter. External authenticated opaque cursor encoding
and dispatcher/transport routing remain required.

`Store.AuthorizeHistory` validates normalized `history.read` intent, exact room
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
Watch, search, directory, byte-budget negotiation, opaque cursor provisioning,
and richer transport operations also remain pending.

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
