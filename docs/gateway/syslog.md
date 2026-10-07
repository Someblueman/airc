# TLS syslog ingestion

## Wire prerequisite

The internal `syslogwire` helper implements a bounded version-1 record profile
from [RFC 5424](https://www.rfc-editor.org/rfc/rfc5424.html), carried using
[RFC 5425 octet counting](https://www.rfc-editor.org/rfc/rfc5425.html).
It is not yet connected to a listener or service. Certificate authorization,
durable dispatch, and actual native qualification remain on the roadmap.

A frame begins with a nonzero decimal byte count and one ASCII space. The record
is limited to 32,768 bytes; an excessive declaration is rejected before allocating
or reading the declared body. The reader consumes exactly that body, preserving
subsequent frames, and returns no partial record on truncation or syntax failure.
Its owner must supply absolute socket deadlines and close after any error; the
helper does not resynchronize an invalid stream or own a connection.

The parser checks priority 0–191 without leading zeros, version 1, the timestamp
or NILVALUE, printable bounded header fields, and complete structured-data
syntax. Timestamp fractions are limited to six digits; invalid dates, leap
seconds, lowercase separators and out-of-range offsets are rejected. Structured
data IDs are unique; repeated parameter names are allowed. Escaped delimiters and
unknown escape sequences are preserved, and parameter values must be valid
shortest UTF-8. This syntax parser does not validate registered structured-data
semantics or private enterprise registrations.

The record owns an exact copy of its input. Structured data and MSG remain byte
slices of that copy. Non-BOM MSG may contain arbitrary bytes; BOM-marked MSG must
contain valid shortest UTF-8. A later send adapter must separately enforce AIRC's
body contract. HOSTNAME, MSGID and timestamps are untrusted metadata and confer
no sender authority, freshness or stable replay identity.

The checkpoint controls exercise one-byte input fragmentation, consecutive frame
boundaries, exact raw-byte ownership, Unicode, repeated parameters, valid and
unknown escapes, binary MSG, malformed syntax, truncated prefixes/bodies,
oversized declarations/header fields, and the exact maximum record size.
`go test -race ./internal/gateway/syslogwire -count=1` passed in 1.278s;
affected vet and staticcheck, formatting and diff checks passed. These are helper
controls, not service/native evidence. Required full acceptance remains due after
executable integration changes.

## Provisioned TLS peer grants

The disabled `syslog_tls` listener now accepts `syslog.routes`: 1–128 entries
containing `certificate_sha256`, `principal`, and `target`. The fingerprint is
exactly 64 lowercase hexadecimal characters representing SHA-256 of the complete
DER end-entity certificate. Each certificate maps to one active existing
principal and one exact authorized target. Duplicate certificate grants,
unknown/revoked principals, missing send authority, extra listener operations,
cleartext listeners and origins are rejected. Credentials and AIRC bindings
remain those of the existing principal; record headers never select them.

The authenticator snapshots these grants from the verified principal snapshot.
Its server TLS configuration requires TLS 1.3 and a client certificate, disables
session tickets and ALPN, and checks the pinned leaf during the handshake. It
also checks validity dates, end-entity status, digital-signature key usage,
client-authentication extended usage where constrained, and unhandled critical
extensions. Authentication accepts an actual completed TLS connection and returns
the immutable principal identity plus a copy of the provisioned target. TLS
proves possession of the leaf's private key; copying certificate bytes is not
sufficient.

This is a selected end-entity pin policy permitted by RFC 5425, using SHA-256.
It provides no CA/name fallback, legacy SHA-1 fingerprint format, or TLS 1.2
profile, and does not claim general RFC 5425 interoperability. Certificate
rotation requires a new provisioned snapshot. Durable current authorization and
revocation remain the dispatch/store responsibility. Transport sender identity
is distinct from origin authentication; signed syslog remains a separate item.

Real TLS controls verify the exact principal, binding and target, route snapshot
ownership, and refusal of unknown/missing certificates, mismatched private keys,
expired leaves, server-only leaves and TLS 1.2. Construction/configuration controls
cover duplicate/invalid grants, missing authority and revoked senders.
The final complete affected race suites passed: configuration 1.301s and
authentication 2.117s. Affected vet and staticcheck, formatting and diff checks
passed. No listener is enabled and no service/native qualification is claimed.

## Owned intake and replay profile

The internal listener owner now takes shared socket capacity before TLS
handshaking, authenticates the pinned peer under one three-second deadline, and
claims a bounded per-principal connection slot. Idle input has a 30-second
boundary; after the first byte, the complete octet-counted frame has one absolute
three-second deadline. Fragments cannot renew it. Shared request admission is
nonblocking, and journal dispatch has a three-second child context shortened by
parent cancellation. Refusals close the connection without a success response.
Cancellation or listener failure closes all owned connections and joins their
callbacks and goroutines before releasing principal/socket ownership.

This send-only profile posts MSG text under the certificate's fixed principal
and target. It removes a leading UTF-8 BOM, requires valid UTF-8, at most 4,096
body bytes and the existing AIRC send contract. It rejects bodies that would
require newline normalization, so accepted text reaches intake without silent
alteration. Empty, binary, NUL-containing and oversized MSG cannot post. Record
headers and structured data are retained for record identity but are not posted
or trusted to choose a sender or destination.

Record identity is content-addressed. The request ID is lowercase SHA-256 of the
Go JSON encoding of `{Protocol, Realm, Target, Record}`, in that field order,
with protocol `syslog_tls/content-v1`, the provisioned realm/target, and the exact
record bytes encoded by the JSON byte-slice rule (base64). The existing journal
scopes this ID to principal/binding. Identical records sent again, including over
another connection or after journal reopen, reuse the retained job/upstream ID.
Distinct intended events must have distinct record bytes, for example a new
originator timestamp or event identifier; identical intentional repetitions are
coalesced while their job is retained. Changing the record, target or realm
changes the ID. This is a local content-addressed import policy, not a standard
syslog message identifier or proof of freshness. A record replayed after journal
retention may be admitted again; there is no unbounded replay tombstone.

Transport-authenticated dispatch accepts only send requests and identities from
that dispatcher's own verifier snapshot. The journal still checks durable current
authorization and revocation. Once journal intake commits, workers own retries
and uncertain AIRC outcomes. Before intake, disconnects, capacity refusal or
storage failure can lose a record. Syslog has no application acknowledgement:
a successful TLS write or orderly transport closure does not prove journal
intake, worker delivery or AIRC acceptance. The gateway does not invent an ACK.
An independent native observer is required for delivery qualification.

Controls prove exact durable body/target, retained identity across journal reopen,
capacity ownership, foreign-verifier and canceled-intent refusal, real TLS
principal capacity, incomplete-handshake global capacity, joined cancellation,
listener rebinding and a nonrenewable partial-record deadline. The service does
not yet enable this owner; actual Darwin/Linux native qualification remains due.

The final complete affected race suites passed: adapter 90.913s, dispatch 2.031s,
authentication 3.032s and syslog wire 1.773s. Affected vet and staticcheck,
formatting and diff checks passed. Full repository acceptance remains required at
the service/native integration checkpoint.
