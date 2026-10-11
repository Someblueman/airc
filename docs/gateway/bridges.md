# Bridges and provider ingress

These carriers accept messages whose sender a provider has already
verified (an IRC server, an XMPP room, a pinned syslog peer, an SNMP
agent) and post them as an AIRC author derived from that identity. Messages are plain text, not contract
requests; each post's request ID comes from the provider's stable event
identity, so redelivery posts once.

## Bridge authors

The IRC and XMPP bridges share one author rule. The identifier the
provider verified is the IRC services account from the server's `account`
tag or the real bare JID a non-anonymous MUC reports; never a nickname or
display name.

- **Mapped account.** An `accounts` entry (`external`, `nick`,
  `account_token_file`) maps one identifier, compared without case, to a
  registered AIRC account; its token is read at startup.
- **Guest.** Anyone else posts as the guest `guest_prefix` plus the
  identifier (defaults `irc-`, `xmpp-`). XMPP users of the bridge account's
  own domain use only their localpart. Names are
  lowercased; bytes outside letters, digits, `-` and `_` become `-`, and a
  name that changed this way or exceeds 30 bytes is shortened and given `|`
  plus ten hex digits of a SHA-256 of the whole identifier, so different
  identifiers never collide. `"guests": false` accepts mapped accounts only.
- **No borrowed names.** Configuration refuses guest prefixes where one
  starts another and mapped nicknames that start with a guest prefix, so a
  remote user cannot become a mapped account or another bridge's guest.

Each author session uses the source `bridge:<id>:<nick>`, so aircd's
per-address limits apply to each external user. A plain message is a `send`
to its route's fixed target.

### Reads

A message starting with `AIRC-READ/1`, optionally followed by a JSON object
of at most 2048 bytes, is a read instead of a post:

```text
AIRC-READ/1 {"operation":"history.read","limit":10,"correlation_id":"h1"}
```

Fields: `operation` (`history.read` by default, `search` or
`context.read`), `cursor`, `limit` (default 20), `query`, `from`,
`parent_id`, `thread_mode`, `correlation_id` and `target` (which must equal
the route's). The read runs as the sender's author and is answered with
`{"version","protocol","request_id","correlation_id","author","room","target","result"|"error"}`;
malformed reads get `invalid_request`, unrouted operations
`unsupported_operation`. The request is visible to the room like any
message, and nothing is encrypted.

## IRC bridge

Top-level `irc_bridges` (up to 32) connect over verified TLS, log in with
SASL PLAIN as a services account and join fixed channels. The server must
grant `sasl`, `account-tag`, `message-tags` and `server-time`; messages need
the server's `account`, `msgid` and `time` tags, and ones without an account
are ignored.

```json
"irc_bridges": [{"enabled": true, "id": "libera", "address": "203.0.113.5:6697",
  "tls_server_name": "irc.example.net", "nickname": "airc-bridge", "account": "airc-bridge",
  "password_file": "/etc/airc/irc-password", "freshness_seconds": 120,
  "accounts": [{"external": "carol", "nick": "carol", "account_token_file": "/etc/airc/carol.token"}],
  "routes": [{"room": "#external", "target": {"kind": "room", "name": "#bridged"},
              "operations": ["send", "history.read"]}]}]
```

Routes (1–128) take operations from `send`, `context.read`,
`history.read` and `search`; channel names are `#` plus ASCII letters,
digits, `_` or `-`. `freshness_seconds` (1–300) bounds how old a message
may be. Read results go only to the requester as NOTICE lines
`AIRC-RESULT/1 <first 16 hex of request_id> <index>/<count> <part>` (parts
of at most 360 bytes); results over 16 KiB answer `capacity_exceeded`. An
idle connection sends PING after 60 seconds and closes 30 seconds later
without a reply.

## XMPP bridge

Top-level `xmpp_bridges` (up to 32) connect with direct TLS (server name
equal to the account's domain), authenticate with SASL PLAIN (`account` is
the bare JID, plus `resource` and `password_file`) and join non-anonymous
MUC rooms. Routes map a room's bare JID to a target with the same operation
set and `freshness_seconds` as IRC. The author comes from the occupant's
real JID; post provenance uses the room's stanza ID (XEP-0359), and delayed
history is ignored. A read result is the whole body of a private chat
message to the requesting occupant (up to 32 KiB).

## TLS syslog

`protocol: "syslog_tls"` (`operations: ["send"]`) accepts RFC 5425
octet-counted RFC 5424 records (≤ 32768 bytes) over TLS 1.3 from clients
whose certificate is pinned:

```json
"syslog": {"routes": [{"certificate_sha256": "<64 hex of the client certificate DER>",
  "bound_author": {"nick": "relay", "account_token_file": "/abs/relay.token"},
  "target": {"kind": "room", "name": "#ops"}}]}
```

Each record's MSG (UTF-8, ≤ 4096 bytes after removing a BOM, unchanged by
normalization) posts as the grant's bound author. Headers and structured
data never choose the sender or target. The request ID is a SHA-256 of the
realm, target and exact record bytes, so a resent record posts once while
its job is retained. There is no syslog acknowledgement.

## Signed syslog

`protocol: "syslog_signed"` (`operations: ["send"]`) adds RFC 5848
signature blocks on top of a pinned TLS relay (`syslog.routes` admit the
connection). `signed_syslog.routes` grant each origin (`hostname`,
`certificate_der`, a base64 DSA signing certificate) a `bound_author` and
`target`. Only profile 0121 (SHA-256 with DSA, 2048- or 3072-bit) is
accepted. Records post only when a signature block covers them with
matching hashes and they are fresh within `retry.lifetime_seconds` (60 s
skew); the journal keeps a per-origin watermark so old or repeated records
cannot post again.

## SNMPv3 traps

`protocol: "snmp_v3"` (UDP, `operations: ["send"]`) accepts SNMPv3
authNoPriv USM traps (SNMPv2-Trap-PDU, ≤ 8 KiB) with `hmac_sha256_192` or
`hmac_sha1_96` authentication. Each `snmp.routes` grant binds `engine_id`,
`user`, `authentication`, `authentication_key_file` (localized key in hex),
`context_engine_id`, `context_name`, a `bound_author`, a `target` and 1–128
`notifications`. A notification names its trap `oid` and `name` and up to 16
`fields` (`oid`, `name`, `type` of `text`, `hex`, `integer`, `unsigned`,
`oid` or `ip`, `required`). The message is rendered as `SNMP <name>` followed
by one `<field>: <value>` line per field, at most 4096 bytes. USM
timeliness and a durable replay record refuse old or repeated traps.
Privacy, informs and community strings are not supported.
