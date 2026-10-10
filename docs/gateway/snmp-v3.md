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

## Wire prerequisite evidence

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

Sender/engine/user grants, fixed AIRC authority, allowed field mapping and
durable USM clock/replay admission are implemented below. The enabled listener
passes full Darwin acceptance and selected Darwin/Linux native controls. Successful
MAC verification alone does not establish freshness, current authorization,
durable intake or native acceptance.

## Owner grant configuration

`snmp_v3` listeners accept `snmp.routes` with 1–128 grants. Each
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
verify packets, render messages or admit journal work. Enabled listeners prepare the authenticator and bind their UDP endpoint;
service qualification is recorded separately below.

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
admission are enforced by the journal path below before delivery.

Affected auth/config/wire race tests passed in 4.166s/2.071s/1.963s; the final
SNMP auth control passed in 1.649s after adding authenticated context-engine
mismatch coverage. Vet, staticcheck, formatting and diff checks passed. Tests
include both profiles, key-file and grant refusals, independent signed malformed
fields, all render types, exact Unicode/multiline body, optional omission,
configured order, 4096/4097-byte boundaries, detached packet/key/config ownership
and concurrent immutable verification.

## Durable timeliness and replay admission

Schema 14 adds separate engine clocks, permanent engine/user ownership and replay
marks. The receiver follows RFC 3414 section 3.2 step 7b: higher boots or a time
higher than the latest received time synchronizes the engine clock, then lower
boots or time more than 150 seconds behind the local clock are refused. Clocks
are shared across users of the same engine and advance with elapsed wall time.
The stored anchor survives restart; a wall clock earlier than the last admitted
packet is refused. Exhausted boots (2147483647) are refused. A higher boot can
synchronize at time zero. The first admitted authenticated trap seeds a previously
unknown engine; this cannot independently establish its UTC recency.

Engine/user ownership permanently binds principal, account binding, exact target
and context engine/name. Credential rotation does not clear clocks or ownership.
A stable SHA-256 request ID covers a domain plus engine/user/boots/time/PDU request
ID; envelope message IDs and equivalent BER lengths cannot create different work.
A separate digest covers decoded notification OID, uptime and fields. Reusing
the tuple with changed content conflicts, including after human-readable mapping.
Retained identical jobs coalesce. A replay mark refuses recreation of a pruned
job. Marks are removed only below the irreversible latest-received time minus
150 seconds or an older boot. Elapsed idle wall time alone never removes them,
so subsequent authenticated resynchronization cannot resurrect old work.

State is bounded to 1024 engines, 4096 permanent engine/user owners and 16384
replay marks. Capacity exhaustion refuses admission without evicting authority
or valid replay marks. Successful intake commits job, replay mark, owner and clock
in one transaction. Quota, current authorization, SQL and commit failures roll
back all changes, including pruning. The dispatcher accepts only identities owned
by its verifier and configured send operations; the store also checks current
configuration and durable revocation. Existing ordinary journal request/default
and replay semantics are unchanged. Migration from earlier schemas preserves
jobs and existing signed-syslog/OSCORE state.

Affected store/auth/config/wire race packages passed in
44.639s/5.669s/4.655s/2.085s. Final focused SNMP store tests passed in 5.312s,
including concurrent restart replay, changed tuple conflicts, pruned results,
shared-engine users, exact timeliness boundaries, reboot, clock rollback,
localized-key rotation, permanent ownership, all three state capacities and
late transaction failure after replay pruning. The dispatcher fixture initially
omitted its required private state directory; that fixture was corrected, and
the complete dispatcher race package passed in 1.604s. Affected vet,
staticcheck, formatting and diff checks passed.
Service qualification and full integration acceptance are recorded below.

## Service native integration evidence

Explicit `snmp_v3` listeners now prepare the private authenticator and bind UDP.
The receive loop owns its socket, receives at most 8193 bytes to detect oversized
datagrams, and performs one bounded admission at a time. Request-slot exhaustion
drops packets before authentication; no user-space pending queue or per-source
identity is created. Invalid, unauthorized, stale, conflicting and oversized
traps are silently refused. No trap acknowledgement or delivery receipt is sent.
Network/kernel/request-capacity loss before durable intake remains possible.
Cancellation closes the socket and joins the receive loop before journal closure.
Credential references are checked as private regular files without aliasing
other state/credentials; startup additionally verifies descriptors and key content.
Capability discovery checks references but does not consume keys or start runtime.

Selected Darwin SNMP native race controls passed in 18.114s: independent ASN.1
UDP senders and the installed Net-SNMP client delivered exact author/target/body
to a real native observer; tests covered deliberate relay loss, replay from a
new source and after restart, no transport replies, timeliness/boot and field
refusals, 4096/4097-byte body boundaries and joined shutdown. Net-SNMP sends its
actual exact datagram through a relay; its packet is not manufactured by a
gateway encoder. Adapter controls also prove a valid packet dropped for a full
request slot cannot seed the engine clock, and cancellation releases the socket.

Full Darwin normal/race/build/format/vet/staticcheck/diff acceptance completed
with terminal exit 0 against frozen Go/module SHA-256
`c7d0cd8e60d26b660b3cc9bb40a3ca3a32ddad490f3c35b34285f1e8d7f7f327`.
Normal acceptance passed in 660.7417961250176s; race acceptance passed in
1553.3970378339873s. Each recorded 3,285 test passes, 53 opt-in/platform skips
and no failures; 31 packages passed and ten had no tests. The native integration
package passed in 647.608s normally and 1308.438s with race instrumentation,
including all fourteen SNMP native pass events. Build, formatting (empty output),
vet, staticcheck and diff checks exited 0. The source digest matched before
every phase, after termination and before committing. Exact checkpoint metadata
and commands are in [testing.md](testing.md#snmp-integration-checkpoint).

Linux ARM64 native checks passed in 6.568s with 13 pass events, one skip and no
failures. The unavailable external Net-SNMP executable was explicitly skipped;
independent UDP/native controls passed. Linux used the existing offline native
fixture and image without race instrumentation. No physical network or general
SNMP management claim is made.
