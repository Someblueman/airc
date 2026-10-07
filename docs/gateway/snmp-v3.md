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
Opaque octets remain bytes; the allowed-field mapping selects their
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
MACs, one-bit mutations at every byte position, appended data, key/raw ownership, uninitialized
verifiers, wrong protocols/key widths, authenticated malformed standard fields,
duplicates, count/value limits, engine/user/context bounds, counter overflow,
opaque octets, signed/unsigned numeric limits, OID bounds and legal long lengths.
Authenticated controls also verify reportable/reserved flag handling and refuse
all-zero/all-FF engine identities. An exact 8 KiB packet with sixteen additional
fields succeeds; the next byte fails. Final affected vet, staticcheck, formatting
and diff checks passed. A five-second, two-worker fuzz run seeded with complete
valid traps and malformed lengths passed in 5.348s after 379,866 executions.
Earlier fuzz runs also passed; no crash corpus was produced.

Sender/engine/user grants, fixed AIRC authority and allowed field mapping are
implemented below. Durable USM clock synchronization and replay admission remain
unimplemented. No listener can yet be enabled, no message is posted to AIRC, and
Darwin/Linux service/native qualification remains on the roadmap. Successful
MAC verification alone does not establish freshness, current authorization,
durable intake or native acceptance.

## Owner grant configuration

Disabled `snmp_v3` listeners accept `snmp.routes` with 1–128 grants. Each
grant binds canonical lowercase hex `engine_id`, exact `user`, explicit
`authentication` (`hmac_sha256_192` or `hmac_sha1_96`), normalized absolute
`authentication_key_file`, canonical `context_engine_id`, exact `context_name`,
existing active send-authorized `principal` and its exact `target`. Duplicate
engine/user pairs are refused. SNMP listeners are send-only UDP endpoints with
numeric addresses; TLS options and browser origins are refused.

Each grant has 1–128 `notifications`, each with a canonical numeric `oid`,
a bounded `name`, and up to 16 `fields`. A field contains canonical `oid`,
bounded `name`, explicit `type` (`text`, `hex`, `integer`, `unsigned`, `oid`,
or `ip`) and `required`. Duplicate notification OIDs, field OIDs or field names
are refused; mandatory SNMP header OIDs cannot be configured as message fields.
Labels are trimmed UTF-8 without controls and at most 64 bytes. Users and
contexts are UTF-8 without NUL, at most 32 bytes; users cannot be empty.

Strict owner configuration parsing permits at most ten nesting levels for
this schema, still within the existing 64 KiB limit. Protocol request JSON
retains its eight-level limit. Configuration validation does not read key files,
verify packets, render messages or admit journal work. Enabled listeners remain
refused until durable timeliness and service qualification are complete.

## Authenticated authority and field mapping

`auth.NewSNMPAuthenticator` independently validates grant syntax and binds each
exact engine/user pair to a private identity from its verifier snapshot, checking
active send permission and the exact target. It reads only owner-private regular
key files through the existing descriptor ownership/permission checks, refusing
symlinks and non-private parents. File contents must be canonical lowercase hex:
40 characters for SHA-1 or 64 for SHA-256, optionally followed by one newline.
Reads are bounded; errors never include key content. The verifier snapshots the
localized key, then clears transient decoded key bytes. Configuration, fields and
keys cannot subsequently alter the authenticator through caller aliases.

Verification selects only the exact engine/user grant, verifies the complete
original datagram MAC, then requires the exact context engine and name. An exact
notification OID selects its allowlist. Additional unconfigured fields, missing
required fields and mismatched types are refused. Optional missing fields are
omitted. Rendering follows configured field order, with `SNMP <name>` followed
by newline-separated `<field name>: <value>` lines. Text preserves exact UTF-8
and multiline content; hex encodes opaque octets; integer/unsigned values use
decimal, OIDs use canonical numeric text, and IP values use dotted IPv4. The
complete body must satisfy core send validation without normalization changing
any byte and must fit 4096 bytes. Oversize content is refused, never truncated.

Returned `VerifiedSNMPNotification` values expose immutable identity, target and
body accessors. Packet access returns a fresh owned parse; the zero value is
invalid. This proof establishes configured MAC/field authority only. Dispatcher
provenance, current durable authorization, USM timeliness and atomic replay/work
admission are still required before delivery.

Affected auth/config/wire race tests passed in 4.166s/2.071s/1.963s; the final
SNMP auth control passed in 1.649s after adding authenticated context-engine
mismatch coverage. Vet, staticcheck, formatting and diff checks passed. Tests
include both profiles, key-file and grant refusals, independent signed malformed
fields, all render types, exact Unicode/multiline body, optional omission,
configured order, 4096/4097-byte boundaries, detached packet/key/config ownership
and concurrent immutable verification.
