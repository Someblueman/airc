# Signed syslog ingestion

## Author

Each `signed_syslog.routes` origin grant names a required `bound_author`
(`nick`, optional `account_token_file`) instead of a gateway principal. A
verified signature block posts its records as that author. The pinned TLS
relay grant in `syslog.routes` still requires a `bound_author` by schema, but
it only admits the connection; signed records never post as the relay. Replay state records the author scope that first used an
origin; a later grant of that origin to another author conflicts. See [route
authors](route-authors.md). Sections below that mention a principal or binding
record earlier checkpoints; the verification and freshness rules are
unchanged.

## Wire and cryptographic prerequisite

The internal helper implements bounded signature and certificate-block fields
from [RFC 5848](https://www.rfc-editor.org/rfc/rfc5848.html), profile `0121`
(SHA-256 with OpenPGP DSA signature integers). Other versions, including SHA-1,
are refused explicitly; this is a selected profile, not general RFC interoperability.
It accepts 2,048-bit DSA parameters with 224/256-bit subgroup orders and
3,072-bit parameters with 256-bit orders. It refuses 1,024-bit keys. Public-key
construction bounds sizes, checks primes, divisibility and subgroup membership,
and snapshots parameters. Production code only verifies signatures; the narrow
staticcheck legacy-algorithm exception documents RFC 5848's DSA requirement.

Parsing starts from owned RFC 5424 bytes, requires ordered single parameters,
canonical decimal counters and base64, 1–99 hashes, and one signed block per
record. Certificate fragment declarations are bounded to 32 KiB, with exact
fragment length and one-based index bounds. Signature extraction removes only
the original SIGN parameter and preserves every other header, structured-data
and MSG byte. OpenPGP signature integers require canonical bit lengths and
complete consumption. Verification reparses raw input, checks the signature,
and requires the entire ordered covered-record list with exact SHA-256 hashes
of complete RFC 5424 records excluding transport framing. Certificate blocks
cannot claim covered records. No partial verified list is returned on failure.

Race-instrumented wire tests passed in 3.337s; focused vet and staticcheck passed.
Tests independently encode real DSA-signed blocks and exercise changed headers,
MSG/counters, appended bytes, changed/reordered/missing covered records, caller
key mutation, invalid and uninitialized keys, malformed integer encodings,
owned bytes, duplicate/malformed blocks and fragment bounds. Existing framing
and record controls run in the same package.

## Origin authority and bounded verification buffers

A `syslog_signed` listener requires existing pinned TLS transport
routes plus separate `signed_syslog.routes` (1–128). Each origin grant contains
canonical base64 public certificate DER (at most 8 KiB), a literal ASCII FQDN/IP
hostname, an active existing send principal and its exact allowed target.
Case-insensitive duplicate hostnames, wildcard/invalid names, malformed or
unsupported certificates and expanded authority are refused. The signer uses
an end-entity DSA certificate with digital-signature usage when constrained;
unknown critical extensions and CA certificates are refused. Its exact DER is
provisioned, independently of the transport certificate. No CA-path fallback
or unsigned header authority exists. Constructors snapshot certificates, key
parameters and private verifier-owned identities; block verification checks
certificate validity at the supplied current time. A fully assembled C-type
payload must contain the exact provisioned certificate and a valid timestamp.
That timestamp requires all carrying certificate blocks to have been verified;
wall-clock freshness remains the next durable admission prerequisite.

One connection owns a verification buffer with SG0 and one signer
hostname/app/procid/nonzero reboot tuple. Pending ordinary records are owned
copies, capped at 99 records and 1 MiB. Certificate payloads are capped at 32 KiB
and 128 verified fragments; out-of-order fragments and identical overlaps work,
while contradictory overlaps fail. Every fragment is independently signed before
assembly. The completed certificate payload precedes covered records in this
selected profile. A signature block releases only the complete ordered verified
batch; it never posts to AIRC. Each incomplete payload or pending-record batch
has an absolute three-second deadline which incoming records do not renew.
Any refusal poisons the owner and clears retained record/payload references.
The service owner enforces this deadline on socket reads as well.

Final affected race checks passed: configuration 4.252s, authentication 6.568s,
wire 7.540s. A subsequent authentication race run passed in 7.295s after adding
exact 1 MiB/next-record and 128/129-fragment controls. Affected vet, staticcheck,
formatting and diff checks passed. Controls use independently encoded real DSA
certificates/signatures and cover grant parsing/refusals, exact origin mapping,
immutable snapshots, unknown origins, certificate expiry, false payloads,
fragment assembly/conflicts, record ownership, verified-only release, resource
bounds, absolute deadline nonrenewal and terminal refusal cleanup.

## Durable freshness and replay admission

Journal schema version 13 adds `signed_syslog_state`; migration preserves existing
jobs, authorization, OSCORE state and queue ownership. Replay state is keyed by
SHA-256 of the provisioned signing public-key SPKI and the case-insensitive origin
hostname, bound permanently to principal, binding and exact target. Certificate
renewal with the same key retains that replay identity. Rows are never pruned or
reset by startup/network traffic; a 1,024-origin ceiling fails closed. A new key
constitutes separately provisioned cryptographic authority.

Authenticated batches carry a private integrity proof of their contents and
certificate interval. Admission refuses changed/forged batches and rechecks
certificate validity. The dispatcher additionally checks verifier identity
provenance and configured send capability. Both the signed block timestamp and
every covered record timestamp are required, nonnegative and fresh within the
configured retry lifetime, with the existing 60-second clock-skew bound. Records
must not precede the authenticated reboot timestamp beyond that skew; reboot
timestamps cannot be future beyond skew. Job issue/expiry bounds come from each
signed record timestamp, rather than the socket arrival time. Literal UTF-8 MSG
bodies are bounded to 4,096 bytes and must survive core normalization unchanged.

All covered jobs, their replay markers and the origin watermark commit in one
transaction after current durable authorization. Capacity or persistence failure
rolls back the entire batch. The existing single-request transaction body is
reused without changing its public interface. Request IDs use the documented
`syslog_signed/record-v1` JSON domain, signing-key digest, origin, reboot, message
number and exact raw record bytes. Existing upstream IDs remain realm/principal/
binding scoped by the journal.

The durable watermark retains reboot, authenticated session-start timestamp,
block counter, highest message number and last unsigned-block digest. Older
reboots, owner/target rebinding, changed session timestamps and conflicting reuse
of the latest block counter are refused. Higher reboot IDs cannot move the
session-start time backward. Retained overlapping/repeated records coalesce to
the same jobs and upstream IDs; old message numbers with pruned results cannot
create replacement jobs. New records require a later block counter. Message
number wrap requires a new reboot in this selected bounded profile. Gaps are not
fabricated into posts or treated as reliable delivery.

Full affected race suites passed: store 39.162s, authentication 4.468s, dispatcher
1.637s, configuration 2.493s and wire 7.522s; affected vet, staticcheck, formatting
and diff checks passed. The final certificate-renewal authentication regression
passed under race instrumentation in 8.515s with affected lint checks passing.
Controls verify atomic capacity rollback, retained overlap/reopen identity,
pruned-result refusal, old reboot refusal, expired/future block and record times,
records before reboot, future reboot, changed batches, certificate renewal,
revocation, read-only storage failure and migration from version 12 without
inventing replay state or altering existing jobs/OSCORE state. Earlier artificial
version-10/11 downgrade fixtures now remove the version-13 table as well.

## Integration boundary

The service permits configured `syslog_signed` listeners. Socket owners retain the pinned TLS transport boundary and release
covered batches only after origin verification and atomic durable admission.
Capability discovery reports the selected send-only partial profile without
starting runtime owners. Darwin and Linux native controls verify exact covered-batch delivery, replay
across reconnect/restart, refusals, bounds and joined shutdown. Full repository
acceptance passed on the source recorded below.

Review follow-up: SG=0 permits a nonzero SPRI, as RFC 5848 section 4.2.3
specifies. Signature and certificate block controls accept SPRI=110; the earlier
zero-only restriction was removed. The corrected wire race suite passed in
3.536s, with focused vet, staticcheck, formatting and diff checks passing.

## Selected listener configuration

The listener uses `protocol: "syslog_signed"`, `enabled: true`, TLS certificate
and key paths, and `operations: ["send"]`. Its `syslog.routes` grant the TLS
relay access by exact transport certificate digest; `signed_syslog.routes`
independently grant an origin signing key authority to post as a bound author
to a target. A relay certificate does not replace the origin signature.

```json
"signed_syslog": {
  "routes": [{
    "certificate_der": "CANONICAL_BASE64_ORIGIN_DSA_CERTIFICATE_DER",
    "hostname": "signed.example",
    "bound_author": {"nick": "producer", "account_token_file": "/owner/producer.token"},
    "target": {"kind": "room", "name": "#allowed-room"}
  }]
}
```

Clients send RFC 6587 octet-counted records over the pinned TLS 1.3 connection:
first independently signed certificate fragments with a complete C-type
payload, then ordinary RFC 5424 records, then an RFC 5848 signature block covering
the entire pending ordered record list. The selected profile requires a reconnect
for a new reboot ID. It does not return a syslog application acknowledgement;
AIRC acceptance is observed separately through native events and authenticated
receipts. Exact repeated blocks retain request and upstream identities. Incomplete
batches are discarded on timeout or shutdown and never reach durable admission.

## Service native integration evidence

Frozen executable/test source SHA-256 is
`1244fcc9ddd2d7c23bfc18cac6ab44180ae5d117521d20dfd6ec4bde58dd7f0c`.
The focused Darwin race suite passed in 38.706s. Linux ARM64 ran the same selected
native controls in 22.967s with exit 0, 10 pass events, no skips and no failures.
Linux used the existing offline Ubuntu image and digest-verified native fixture;
no Linux race instrumentation, installation, remote deployment or general RFC
interoperability is claimed. Linux gateway SHA-256 is
`7c3746e11e768f8b8a93558bffaf9c8b0614249e179dcbb10f80ded8fc8ccac9`,
test executable SHA-256 is
`ed82cdc8ea8421cb2931b0c51051106f4cc96e76030b8044b998dd9d6845d699`,
and native fixture SHA-256 is
`1f3aa4a566a23d4326e2a2b3ed81942b5384e17521dc1bf38572511778d7e85f`.
Provenance and the complete Linux log are under
`/tmp/signed-syslog-native-v1-a538d8f`.

The independent client encodes octet framing, certificate payloads, covered
record hashes and signatures without the production wire encoder. Native AIRC
observations verify exact Unicode/multiline bodies, the existing author and
allowed target. Authenticated receipt reads verify durable single-send results;
reconnect/restart fences and native history verify replay adds no duplicate
posts or changes to upstream IDs. Covered batches can deliver in either order
through independent workers. An initial fixture assumed delivery order and
consumed the other valid event while waiting; the corrected fixture collects
both bodies and checks their individual IDs.

Actual service refusals cover an uncovered batch timing out, missing certificate
payload, changed covered record, changed signature header, stale/future record
timestamps and the 100th pending record. Each requires terminal closure rather
than treating a read timeout as successful refusal. After joined shutdown,
refusal controls verify zero jobs, replay markers, origin-watermark rows and
native posts. Shutdown with an unverified pending record also leaves no state
or post. Earlier wire/authentication/store checks independently cover fragment,
byte, origin-grant, freshness, transactional failure and replay boundaries.
Full normal repository acceptance exited 0 in 652.873s; full race acceptance
exited 0 in 1540.999s on the same frozen source. Each recorded 3,160 passes,
53 platform/opt-in skips and no failures, with 30 passing packages and nine
without tests. Build, formatting (empty output), vet, staticcheck and diff gates
all passed. Exact commands, timings and terminal provenance are in
[testing.md](testing.md#signed-syslog-integration-checkpoint).
The acceptance runner and metadata prefix are
`/tmp/airc-gateway-signed-syslog-v1-a538d8f`.
