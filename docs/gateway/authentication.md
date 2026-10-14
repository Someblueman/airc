# Authors and access

The gateway works like an IRC server's front door, not an identity
authority. It has no accounts, bearer tokens or signing keys of its own:
each request names its author, and AIRC decides who that author is and what
it may do. The model that came before (principals, bindings, signed and
encrypted requests, return routes) is gone; see
[removed-identity.md](removed-identity.md).

## Authors

A request's `author` ([contract](contract-v1.md#authors-and-identity)) is:

- a nickname alone: an AIRC **guest** posting as that nick;
- a nickname with an account token: that **registered** AIRC account.

The gateway opens an AIRC session as the author through the configured
`upstream`: a guest connects by nickname, a registered author logs in with
SASL and its token. A guest on a registered nickname, a nickname in use and
a wrong token fail exactly as they would for an IRC client
(`nick_registered`, `nick_in_use`, `authentication_failed`).
`account.register` creates an account with the author's own token, and
`account.identify` checks an author ahead of time; on a connection carrier
the identified author becomes the connection's default, as after IRC `NICK`
and SASL, until the connection closes.

As everywhere on AIRC, a guest nickname proves nothing to other users; only
a registered account does. Tokens are accepted on every carrier and are
readable on plaintext ones, so use TLS where the network is untrusted.

## Listener access

Each listener is an access gate ([configuration](configuration.md#listeners)):
which operations and targets it admits, whether guests may use it, an
optional access password (IRC's `PASS`), and per-source and per-author
rates and connection caps. An empty policy admits guests, as an open IRC
server does. Local carriers add the boundary they already have: owner-only
sockets, FIFOs, queues and directories, Unix peer credentials, the parent
process for stdio and socket pairs, the serial line, the kernel-audited
caller UID for Mach IPC, and the operator who runs an import.

## Bound authors

Producers that cannot hold a token at rest, or whose provider already
verified the sender, post as a configured author instead:

```json
"bound_author": {"nick": "alerts", "account_token_file": "/owner/alerts.token"}
```

With `account_token_file` (an AIRC account token, as `airc user create`
writes one) the bound author is that registered account; without it, a
guest under `nick`. The token is read once at startup, kept in memory, and a
bad file fails startup.

| Where | Rule |
|---|---|
| Mailboxes, queues, shared memory, mapped files, offline bundles, BPv7 (`bound_author` on the listener) | A request that names no author runs as the bound author; one that names an author runs as that author. `import-bundle` and `import-bpv7` also take `--author-nick NICK [--author-token-file PATH]` for one run. |
| OSCORE (`oscore.author`) | The default author for requests that name none. |
| Redis, PostgreSQL NOTIFY, SFTP and SCP routes (`bound_author`, `guests`) | A request naming a registered author always runs as it; one naming a guest only when `guests` allows; otherwise the bound author; with neither, `invalid_request`. `guests` defaults to true without a bound author and false with one. |
| TLS syslog, signed syslog, SNMPv3 grants (required `bound_author`) | The verified sender posts as its grant's bound author; these messages are text, not requests. |
| IRC, XMPP bridges (`accounts`, `guest_prefix`, `guests`) | See [bridge authors](bridges.md#bridge-authors). |

Whoever the route's own gate admits (anyone who can PUBLISH, XADD, NOTIFY,
write the mailbox or hold the SSH key) can post as its bound author: restrict
that gate as you would the account's token.

## Provider-verified senders

Route-owned ingress checks its sender with the provider's own mechanism and
has no listener password or guest policy: pinned TLS syslog client
certificates, RFC 5848 origin signatures, SNMPv3 USM, SSH logins for SFTP
and SCP, and for bridges the IRC `account` tag or the real JID a
non-anonymous MUC reports. Provider replay tables record
the author scope that first used an event or origin; a later grant to a
different author conflicts.

## Tokens and the journal

Tokens never reach disk. A job keeps only SHA-256(`airc-gateway-account-v1`
NUL folded-nick token), to match a resubmission of the same request ID, and
jobs are keyed by author scope (`g:` or `a:` plus the folded nickname). After
a restart a registered author's unfinished job waits in `credential_wait`
until the same request ID returns with a matching token. Receipts are
visible only to the same author: by nickname for a guest's job, with the
matching token for a registered author's.

## Abuse limits

The limits are IRC's. Each listener bounds requests per source (default 120
a minute) and per author (60), and open connections (256, 16 per source
address, loopback exempt); route-owned ingress has the same request rates.
With the upstream's `gateway_token_file`, each author session sends
`WEBIRC` with the real client's source, so aircd applies its own
per-address limits: eight account registrations per source an hour (`437`,
reported as `capacity_exceeded`), and nine failed tokens per source in ten
minutes, after which aircd refuses that source's tokens until the window
passes (a correct token refused this way is retried later rather than
failed). Sources are the client IP address without its port,
`listener:<id>:pid:<pid>` for each client process on a Unix stream socket,
`listener:<id>` for other carriers without addresses, `route:<id>` for
route-owned ingress and `bridge:<id>:<nick>` for each bridged user.

## Credential files

On Unix the reader opens an owner-only parent directory, then the final
component with `O_NOFOLLOW`; the file must be a regular file owned by the
current user with no group or other permission bits. Symlinks, directories
and FIFOs are refused, reads are bounded, and errors never include file
contents. Listener passwords and AIRC account tokens are written by their
owners; `airc-gateway credential init --file PATH` creates a random 32-byte
key such as a `cursor_key_file`.
