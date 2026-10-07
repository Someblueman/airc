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

## Integration boundary

This helper does not authorize a signer, assemble certificate payloads, enforce
freshness/reboot/block/message replay state, buffer pending records, or post to
AIRC. Those independently verifiable steps remain in the roadmap. TLS peer
identity alone does not authenticate the signed origin. No service/native or
full signed-syslog qualification is claimed by this prerequisite.
