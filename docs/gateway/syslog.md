# TLS syslog ingestion

## Current integration state

Service activation and native qualification passed the unchanged-source full
acceptance gate. This send-only TLS profile is qualified on Darwin and Linux
ARM64 within the evidence limits below. Signed covered-record freshness remains
separate work. The prerequisite checkpoints describe their original scope.

## Wire prerequisite

The internal `syslogwire` helper implements a bounded version-1 record profile
from [RFC 5424](https://www.rfc-editor.org/rfc/rfc5424.html), carried using
[RFC 5425 octet counting](https://www.rfc-editor.org/rfc/rfc5425.html).
At the wire checkpoint it was not connected to a listener or service; its
controls alone supplied no certificate, dispatch or native qualification.

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

The certificate-grant checkpoint introduced a disabled `syslog_tls` listener with `syslog.routes`: 1–128 entries
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
passed. That checkpoint did not enable a listener or claim service/native
qualification.

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
listener rebinding and a nonrenewable partial-record deadline. That checkpoint did not enable the service or supply actual Darwin/Linux native
qualification.

The final complete affected race suites passed: adapter 90.913s, dispatch 2.031s,
authentication 3.032s and syslog wire 1.773s. Affected vet and staticcheck,
formatting and diff checks passed. Full repository acceptance remains required at
the service/native integration checkpoint.


## Service/native integration evidence

The service now recognizes opt-in `syslog_tls`, configures the pinned TLS profile,
and hands the underlying listener directly to the syslog owner so socket
capacity is acquired once. Capability discovery reports configured send-only
operations without starting the runtime or exposing peer pins/credential paths.

Independent native clients emit octet-counted RFC 5424 records without importing
the production wire package. Tests compare exact Unicode/multiline body, fixed
target and AIRC author against an independent native observer, despite a forged
record HOSTNAME. Identical replay over another connection and after journal
reopen/service restart retains message IDs and one send attempt, with no duplicate
native posts. A distinct subsequent event fences preceding one-way input.

Durable completion is observed separately through the existing authenticated
HTTPS receipt API, using a dedicated IPv6 loopback control listener. This is not
an acknowledgement on syslog. The native message stream remains the independent
delivery evidence. Controls also cover missing/unknown peer certificates, wrong
server-name verification, TLS 1.2, malformed/truncated records, excessive declared
length, unsupported binary MSG and excessive body size, with no journal job or
native post. A one-socket service control proves shared admission includes the
TLS connection, joins shutdown and rebinds the listener.

The final selected Darwin race suite passed in 21.022s: 12 test pass events,
no skips or failures. The actual gateway binary SHA-256 was
`62caba826f64a6f4f5122d0c4ea38d9a24158c515800b6b27322737fb915b23a`.
The selected Linux ARM64 native suite passed in 3.4245815409813076s: the same
12 pass events, no skips or failures, no Linux race claim. It used Go host
cross-compilation, digest checks, Ubuntu image
`33ceb71981b602c1a7443a53469e4dba065f7503eab3078a2d7a57a2ab987517`,
a network-disabled/read-only container and disk-backed temporary volume, with no
installation or downloads. Gateway/test SHA-256 values respectively were
`bacedaf0a791ac1a71b8404275f38842b5cac372ec7adb43a6cb0e22b981a743` and
`3933969cc8e07b86f1c08a3465f35712e836ec7634eaa589c4ce334c22af6b48`.
The native fixture SHA-256 was
`1f3aa4a566a23d4326e2a2b3ed81942b5384e17521dc1bf38572511778d7e85f`,
from baseline `96f16c3bbb8a3faac2e69684f591af63ceb58450` and archive
`4f25ee66941bd79c29f936b8deabc120dc0451e5d1b747afee4bbdf593b32829`.
Linux provenance and log are under `/tmp/syslog-native-v1-44cbe61`.

Early fixture runs used an external SQLite acceptance poll and failed with
`SQLITE_PROTOCOL (15)` or observed dispatching state. The first poll also tried
to scan nullable receipt facts into integers. No production journal fix or
causal conclusion about the SQLite failure is claimed. The final fixture uses
the already-established receipt-control API instead of an external live database
reader; journal reopen verifies retained results after joined shutdown. Fixture
resource-reserve and distinct numeric-loopback endpoint errors were corrected
before final qualification. None of those failed runs is acceptance evidence.

Full acceptance passed against frozen sorted tracked Go/module path/content
SHA-256 `557f0a09ca391cc3aec1b8e22ab4d70587853ad6fef35cac02919a950177c172`,
with evidence prefix `/tmp/airc-gateway-syslog-v1-44cbe61`. Source verification
matched before every phase and after terminal completion. Exact commands and
results are recorded in [testing.md](testing.md#tls-syslog-integration-checkpoint).
Normal acceptance passed in 643.334742749983s and race acceptance in
1496.1346880419878s; each recorded 3,110 test passes, 53 opt-in/platform skips,
no failures, 30 passing packages and eight packages without tests. Native
integration passed in 629.684s normally and 1270.044s under race instrumentation.
Build, formatting, vet, staticcheck and diff checks all passed.
