# IRC bridge profile

The selected bridge connects to an owner-configured, TLS-verified IRC server.
Services-account/room grants map IRC authors to existing AIRC principals and
bindings. A nickname, hostmask or client-only tag cannot establish authority.
Server `account`, `msgid` and `time` tags are required for admitted events;
missing or invalid identity, stable event identity or freshness is refused.
The bridge services account is excluded from ingress to prevent its own echoes
from becoming new AIRC sends. No server or external account is configured by
this specification, and no live service is contacted.

The private `internal/gateway/ircwire` prerequisite implements bounded client-side
IRCv3 framing. It is not yet a bridge service. Configuration, authenticated
mapping, durable intake, bounded reads, connection ownership and actual IRC/native
AIRC qualification remain on the [roadmap](ROADMAP.md).

Server lines require CRLF. The tag section is bounded at 8,191 bytes including
`@` and its separating space; the remaining message is bounded at 512 bytes
including CRLF. The reader bounds storage even without a line terminator;
any framing error is terminal for its connection. No partial command escapes.
Commands have at most 15 parameters. Prefixes and whole trailing text are
preserved; no trimming or truncation is used to make a message fit.

Tag names remain opaque and case sensitive. Repeated names use the last value.
Empty and missing values are equivalent. Escapes follow IRCv3, including dropping
a trailing backslash and the backslash of an unknown escape. Invalid UTF-8 tag
values are refused by this profile. Unknown tag names are retained without
inventing authority; `+account` is distinct from the server `account` tag.

Client command encoding emits a complete untagged command within 512 bytes or
refuses it. It does not split oversized text into independently admitted events.
Empty trailing parameters remain distinct from absent trailing parameters.

The primary sources are [RFC 2812](https://www.rfc-editor.org/rfc/rfc2812),
[IRCv3 message tags](https://ircv3.net/specs/extensions/message-tags),
[account tags](https://ircv3.net/specs/extensions/account-tag),
[server time](https://ircv3.net/specs/extensions/server-time) and
[message IDs](https://ircv3.net/specs/extensions/message-ids). A message ID is
opaque and scoped to its IRC network; repeated deliveries retain that ID.
