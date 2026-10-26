# Removed gateway identity model

The gateway no longer has its own identity system. Requests name their
author, as IRC clients name their nick: a guest nickname, or a registered
AIRC account with its token. Each listener is an access gate (operations,
targets, guests, an optional password and limits). See
[authentication.md](authentication.md) and
[configuration.md](configuration.md).

Everything that belonged to the earlier model is gone:

| Removed | Replacement |
|---|---|
| `principals` (bearer tokens, per-principal operations and targets, revocation, `anonymous_principal`) | Authors named in each request; listener `operations`, `targets`, `guests`, `password_file`; AIRC's own account and ban controls |
| `bindings` | One top-level `upstream` |
| `signing_keys` (signed requests) and `recipient_keys` (encrypted requests) | Plain contract requests over TLS, a listener password or the local access gate |
| `response_signing_key` and `return_routes` (signed or sealed results, fixed reply destinations) | Plain results on the request's own connection or source address |
| `fifo`, `append_log`, `directory_mailbox` and `principal` options (OSCORE, MQTT-SN, Mach IPC callers, Bundle Protocol; carriers since [removed](removed-carriers.md)) | The listener itself; a mailbox may set `bound_author` |
| `target` on MQTT topic routes (and the AMQP and STOMP routes since [removed](removed-carriers.md)) | The request's own target, limited by listener `targets` |
| `limits.*_per_principal` | `limits.*_per_author` |

The gateway was never deployed, so nothing is migrated automatically. A
configuration that still uses any of these fields fails to load with one
error naming the first such field and this note. Remove the fields, add an
`upstream`, and make clients name their author.

## Journal compatibility

A journal from before the current schema refuses to open; see
[configuration](configuration.md#journal).
