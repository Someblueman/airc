# SNMPv3 trap ingestion

## Authenticated wire prerequisite

The internal wire decoder implements a bounded authenticated USM trap profile
from [RFC 3412](https://www.rfc-editor.org/rfc/rfc3412.html),
[RFC 3414](https://www.rfc-editor.org/rfc/rfc3414.html),
[RFC 3416](https://www.rfc-editor.org/rfc/rfc3416.html) and
[RFC 3417](https://www.rfc-editor.org/rfc/rfc3417.html). It requires version 3,
USM security model 3, authNoPriv, a plaintext scoped SNMPv2-Trap-PDU, and zero
error status/index. The reportable flag is treated as zero for an unconfirmed
trap as required by RFC 3412; other reserved bits are also ignored on receipt. Community messages, noAuthNoPriv, authPriv, informs,
queries, responses, reports and other security models are refused. This is a
selected one-way trap profile, not a general SNMP management implementation.

Authentication is selected explicitly per verifier: `hmac_sha256_192` uses a
32-byte localized key and a 24-byte MAC as defined by
[RFC 7860](https://www.rfc-editor.org/rfc/rfc7860.html); `hmac_sha1_96` uses a
20-byte localized key and the legacy 12-byte USM MAC from RFC 3414. There is no
protocol fallback or MD5 support. Constructors snapshot the key. Verification
reparses the owned complete datagram, clears only the original authentication
parameter bytes in a separate copy, computes HMAC over those exact bytes and
compares the selected-length MAC in constant time. Header, security parameters,
context, PDU and values are all covered; no reserialized representation or
partially verified packet confers authority.

Datagrams are capped at 8 KiB. Fixed protocol nesting uses bounded definite BER
lengths; legal nonminimal definite-long lengths are accepted, while indefinite
lengths, high-tag encodings, truncation and trailing data fail. Length accumulation
is bounded before integer arithmetic can overflow. Engine/context-engine IDs
are 5–32 bytes and refuse all-zero/all-FF reserved values; username is nonempty UTF-8 of at most 32 bytes and context name is
UTF-8 of at most 32 bytes. Message IDs and engine counters retain RFC integer
bounds, including signed 32-bit request IDs. Engine boots at its maximum value
still requires rejection by the later timeliness layer; parsing is not timeliness.

A trap must begin with `sysUpTime.0` (TimeTicks) and `snmpTrapOID.0` (OID), in
that order. Up to sixteen additional distinct fields are retained. Object IDs
are bounded to 128 arcs with SNMP 32-bit subidentifier limits and canonical
base-128 values. Supported values are opaque OCTET STRING (at most 512 bytes),
signed INTEGER32, OID, four-byte IpAddress, Counter32, Gauge32, TimeTicks and
Counter64. Numeric values require legal minimal signed/unsigned encodings and
exact width bounds. Null, exception and unsupported value types are refused.
Opaque octets remain bytes; a later allowed-field mapping must decide their
permitted rendering and cannot assume UTF-8. Packet storage is owned, and key
mutation by a caller cannot alter an existing verifier.

## Evidence and remaining boundary

The final affected race suite passed in 1.400s, including the installed Net-SNMP
5.6.2.1 client on a private Darwin loopback UDP socket. The client uses isolated
configuration/persistence directories and fixture-only credentials. Its actual
SHA-1 USM trap verifies exact engine/user/context, notification OID, uptime and
Unicode/multiline OCTET STRING bytes. The initial fixture incorrectly expected
`-Z` to set the authoritative trap sender's boots/time; the actual client emits
0/0. That assertion was corrected without changing production parsing or MAC
verification. This is wire interoperability evidence, not AIRC delivery evidence.

Independent fixture encoding uses the standard ASN.1 encoder and test-only USM
key localization, without the production decoder. Tests cover SHA-1 and SHA-256
MACs, every single-byte mutation, appended data, key/raw ownership, uninitialized
verifiers, wrong protocols/key widths, authenticated malformed standard fields,
duplicates, count/value limits, engine/user/context bounds, counter overflow,
opaque octets, signed/unsigned numeric limits, OID bounds and legal long lengths.
Authenticated controls also verify reportable/reserved flag handling and refuse
all-zero/all-FF engine identities. An exact 8 KiB packet with sixteen additional
fields succeeds; the next byte fails. Final affected vet, staticcheck, formatting
and diff checks passed. A five-second, two-worker fuzz run seeded with complete
valid traps and malformed lengths passed in 5.348s after 379,866 executions.
Earlier fuzz runs also passed; no crash corpus was produced.

Sender/engine/user grants, fixed AIRC authority and allowed field mapping remain
unimplemented. Durable USM clock synchronization and replay admission remain
unimplemented. No listener can yet be enabled, no message is posted to AIRC, and
Darwin/Linux service/native qualification remains on the roadmap. Successful
MAC verification alone does not establish freshness, current authorization,
durable intake or native acceptance.
