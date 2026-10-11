# Gateway authentication

The gateway works like an IRC server's front door, not an identity
authority. It has no accounts, bearer tokens or signing keys of its own.

## Access gate

Each listener is an access gate (see
[configuration.md](configuration.md#listeners)): which operations and
targets it admits, whether guests may use it, an optional access password
(IRC's `PASS`) and per-source and per-author limits. Zero configuration
admits guests, as an open IRC server does. Route-owned ingress (Redis,
PostgreSQL, SFTP, SCP, syslog, SNMPv3, webhooks, ChirpStack, chat bridges)
gates with its provider's own check, then posts as an author; see
[route-authors.md](route-authors.md).

## Authors

Every request names its author (see
[contract-v1.md](contract-v1.md#authors)):

- a nickname alone is an AIRC **guest**, posting as that nick;
- a nickname with an account token is that **registered** AIRC account.

AIRC itself checks the author: the gateway connects as the author through
the configured `upstream`, a guest by nickname and a registered author with
SASL and its token. A guest on a registered nickname, a nickname in use and
a wrong token fail exactly as they would for an IRC client
(`nick_registered`, `nick_in_use`, `authentication_failed`).
`account.register` creates an account with the author's own token, and
`account.identify` checks an author ahead of time. On connection carriers an
identified author becomes the connection's default author, as after IRC
`NICK` and SASL, for that connection only.

Local carriers can bind an author instead: a mailbox, queue, shared-memory,
mapped-file, offline or Bundle Protocol listener's `bound_author`, and an
OSCORE context's `oscore.author`, are the default author for requests that
name none ([local-authors.md](local-authors.md)).

Tokens are accepted on any carrier and can be observed on plaintext ones, so
use TLS where the network is untrusted.

## Tokens and the journal

Tokens never reach disk. The journal keeps only
SHA-256(`airc-gateway-account-v1` NUL folded-nick token), to match a
resubmission of the same request ID; jobs are keyed by author scope (`g:` and
`a:` plus the folded nickname). After a restart a registered author's
unfinished job waits in `credential_wait` until the same request ID returns
with a matching token. Receipts are visible only to the same author: the
nickname for a guest's job, the matching token for a registered author's.

## Abuse limits

Limits are IRC's. Each listener bounds requests per source and per author
and open connections. With the upstream's `gateway_token_file`, each author
session sends `WEBIRC` with the real client's address, so aircd applies its
own per-address limits: eight account registrations per source an hour
(`437`, reported as `capacity_exceeded`), and nine failed tokens per source
in ten minutes, after which aircd refuses that source's tokens
(`authentication_failed`) until the window passes. Carriers without
addresses share one source, `listener:<id>`.

## Credential files

Passwords, tokens, keys and certificates are referenced by path. On Unix the
reader opens an owner-only parent directory, then the final component with
`O_NOFOLLOW`; it must be a regular file owned by the current user with no
group or other permission bits. Symlinks, directories and FIFOs are refused,
reads are bounded, and errors never include file contents.

`airc-gateway credential init --file PATH` creates one new 32-byte random key
(hex, one newline, mode 0600) in an existing owner-only directory, without
overwriting anything; use it for a `cursor_key_file`. Listener passwords and
AIRC account tokens are written by their owners.

## Provider verification

Provider identities are verified where a provider defines them: GitHub
webhook HMAC, ChirpStack bearer secrets, TLS syslog certificate pins, RFC 5848
signed syslog, SNMPv3 USM, IRC SASL and account tags, XMPP MUC JIDs, Matrix
sync authentication and SSH logins. Each maps the verified sender to an
author; see [route-authors.md](route-authors.md) and the carrier documents.

The identity model that came before (principals, bindings, signed and
encrypted requests, signed results, return routes) is removed; see
[removed-identity.md](removed-identity.md).
