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
IRCv3 framing. It is not yet a bridge service. Server-session authentication, durable intake, bounded reads, connection
ownership and actual IRC/native
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

Provisioning prerequisites are defined by `config.IRCBridge`, `config.IRCRoute`
and `Config.ValidateIRCBridge`. They are deliberately not registered in
`Config` JSON or service startup yet. A bridge requires a stable owner ID,
numeric non-wildcard dial endpoint with nonzero port, verified TLS server name,
optional absolute CA path, explicit nickname and services account, and an
absolute private SASL password path. Runtime ownership must verify the password
file and TLS connection; validation does not read credentials or authenticate.

Freshness is explicitly 1–300 seconds, with 1–128 fixed account/room grants.
Each grant resolves to an active existing principal authorized for the exact
native target and all declared operations (send, context, history or search).
History/search require the existing cursor key. No signing key is required for
this server-authenticated profile; the trusted server session must supply the
authentication proof before dispatch. Current durable authority still applies.

Services-account names are literal ASCII letters, digits, dot, underscore or
hyphen, at most 64 bytes. Ingress comparisons are exact; case aliases confer no
authority. Grants whose account and room differ only by ASCII case collide and
are refused. Channels use a fixed `#` plus ASCII letters, digits, underscore or
hyphen subset, at most 64 bytes; this subset folds identically under ASCII and
RFC1459 mappings. The bridge's own account cannot be granted, including case
aliases. Nicknames use the bounded standard ASCII subset at most 30 bytes.

The unused private registration prerequisite sends a blind `CAP REQ` for
`sasl account-tag message-tags server-time`, followed by NICK and USER.
It requires one complete acknowledgement of the exact requested set before
starting SASL PLAIN. A single empty challenge triggers complete base64
credentials in 400-byte chunks; an exact final 400-byte chunk is followed by
`AUTHENTICATE +`. Constructor inputs are bounded; it owns a password copy and
clears it after emission or close. Returned command buffers belong to the caller,
which must clear credential-bearing buffers after writing them.

Registration requires both account confirmation (`900`) naming the configured
bridge account and SASL success (`903`), in either order, before CAP END.
Only a subsequent matching welcome (`001`) establishes protocol readiness.
Missing capabilities, SASL failure/logout, registration errors, early user
events, wrong account/nickname, or more than 128 registration messages refuse
the session. Removal of a required capability closes readiness. PING is answered
with a whole bounded PONG; it does not reset any future owner deadline.

This state machine performs no I/O, TLS validation, deadline management or
AIRC authentication. Readiness alone is not an identity proof. The later owner
must feed it commands read directly from its verified server connection and
join that connection's lifecycle before exposing opaque event proofs.
The registration rules follow [capability negotiation](https://ircv3.net/specs/extensions/capability-negotiation)
and [SASL](https://ircv3.net/specs/extensions/sasl-3.1).
