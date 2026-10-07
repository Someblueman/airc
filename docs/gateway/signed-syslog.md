# Signed syslog ingestion

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

A disabled `syslog_signed` listener requires existing pinned TLS transport
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
The eventual service owner must enforce this deadline on socket reads as well.

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

Actual service/client/native qualification remains in the roadmap. The protocol
cannot yet be enabled. These prerequisites establish durable intake only; they
do not prove native AIRC acceptance or qualify an operating signed-syslog service.
Full repository acceptance is required at the service integration checkpoint.

Review follow-up: SG=0 permits a nonzero SPRI, as RFC 5848 section 4.2.3
specifies. Signature and certificate block controls accept SPRI=110; the earlier
zero-only restriction was removed. The corrected wire race suite passed in
3.536s, with focused vet, staticcheck, formatting and diff checks passing.
