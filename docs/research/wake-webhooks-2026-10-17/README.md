# Wake notices: aircd to a real receiver

Run 17 October 2026 on the branch that adds them, on a 4-vCPU Intel Xeon
2.10 GHz with Go 1.27.0, Linux AMD64. This is the
[plan](../stay-connected-2026-10-17/README.md)'s sixth tier, built in aircd
rather than the gateway: aircd already knows an account's inbox without
holding its token, so no credential is kept at rest to wake it. What it
does is in [WAKEUP.md](../../WAKEUP.md#webhook-notices).

## The run

[run.sh.txt](run.sh.txt) starts a scratch `aircd` with an accounts file and
`--wake-allow` naming two prefixes: a local [ntfy](https://ntfy.sh) server
(v2.11.0, built from its tag; ntfy.sh itself answered this sandbox's shared
address with its daily quota exceeded) and
[verify_receiver.py.txt](verify_receiver.py.txt), a receiver written
independently of aircd that checks each notice's HMAC signature and
timestamp. `airc user create` makes the accounts `carol` and `dave`;
`airc wake` registers them, carol on an ntfy topic with a generated secret
and a 10-second quiet period, dave on the verifying receiver with his own
secret. A guest then posts over plain IRC while neither holds a
connection. [transcript.txt](transcript.txt) is the output.

| Step | Result |
| --- | --- |
| `airc wake` for a URL off the allowlist | Refused by aircd: "Wake URL is not on this daemon's allowlist" |
| carol registers, no `--secret-file` | Registered; a 32-byte secret printed once |
| dave registers with `--secret-file` | Registered; no secret printed |
| DM to carol, mention of dave, mention of carol, a post naming nobody | ntfy's `airc-carol` topic holds two notices: one at once, the later mention after the quiet period; the post naming nobody sends none |
| The verifying receiver | One notice for dave, signature valid and fresh |
| carol runs `airc check --mentions` | The DM and the mention, read natively |

The notices carry the nick, account ID, a message count and the newest
message's ID, never a sender or text.

## What the tests add

`internal/server/wake_test.go` drives aircd's wire with an HTTP receiver
that checks every signature: a guest cannot register (`481`); an account
cannot register a URL off the allowlist (`417`); a connected account is not
woken; a DM wakes at once and what follows within the quiet period goes in
one notice counting the mention and the DM but not a post that does not
name the account or a reaction; a refused notice is reported by
`WAKE STATUS` with the messages still pending; registrations outlive a
restart and `WAKE CLEAR` removes them; and a daemon without `--wake-allow`
answers `437`. `cmd/airc/service_test.go` checks that `airc service install
--wake-allow` passes the list to aircd and refuses a prefix without a path.
