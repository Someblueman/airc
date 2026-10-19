# What a one-shot command's connection costs

Measured 19 October 2026 against `main` (`eaf0e22`). The host is a 4-vCPU Xeon (2.8 GHz) with Go 1.27.0 on Linux.

Every `airc` command connects, registers as an ephemeral session, does one thing and closes. The question is what that costs an agent, and the daemon, compared with the same operation over a connection held open. Ephemeral sessions are already invisible to other clients: they never join, never appear in names or presence, and send no JOIN or QUIT. So the cost here is time and CPU, not churn anyone sees.

## Workload

[connmeasure.go.txt](connmeasure.go.txt) runs real `aircd` and `airc` binaries, built from `main` with `-trimpath`. Each daemon gets its own scratch port and state directory, with a history file at the default `--sync full`:

- **`tcp-guest`:** plain TCP, a guest nickname.
- **`tcp-account`:** plain TCP, a registered account logging in with SASL.
- **`tls-account`:** TLS 1.3 with the connection credential (`--access-token-file`) and an account: remote mode.
- **`unix-guest`:** the Unix socket, a guest.

A proxy adds a simulated round trip of 0, 25 or 100 ms, half in each direction, and counts the connections each command opens. Each case takes 30 samples after one warmup:

- **CLI:** `airc unread`, `airc check --json` and `airc send --json`, each a fresh process with its own `AIRC_STATE_DIR`.
- **`connect`:** `pkg/irc` dials and registers an ephemeral session, then closes.
- **`held`:** the same check (one `CHECK` request) and send (a request-ID post, until its receipt) over one connection kept open.
- **Process start:** `airc --help`.

The daemon's CPU, read from `/proc`, is measured over 2,000 operations with no proxy. One series connects, checks and closes each time; the other checks over one held connection.

Raw rows are in [results.jsonl](results.jsonl) and the run's output in [summary.txt](summary.txt). Times below are medians.

## Results

### Round trips

Each count is the slope from 0 to 100 ms of simulated round trip.

| | CLI command | Registering alone | Same operation, held |
|---|---:|---:|---:|
| `tcp-guest` | 2.0 | 1.0 | 1.0 |
| `tcp-account` | 5.1 | 4.0 | 1.0 |
| `tls-account` | 6.1 | 5.1 | 1.0 |

A guest registers in one round trip. An account's SASL login is four: the client sends `CAP REQ`, waits for the ACK, sends `AUTHENTICATE PLAIN`, waits for `+`, sends the credential, waits for `903`, and only then ends capability negotiation and registers (`pkg/irc/sasl.go`). TLS 1.3 adds one more. Every CLI command opened exactly one connection.

### Time

`airc check`, in ms:

| Round trip | `tcp-guest` CLI / held | `tcp-account` CLI / held | `tls-account` CLI / held |
|---:|---:|---:|---:|
| 0 | 10.3 / 0.3 | 11.7 / 1.3 | 14.3 / 0.3 |
| 25 ms | 65 / 28 | 147 / 28 | 177 / 28 |
| 100 ms | 214 / 103 | 519 / 103 | 621 / 103 |

Locally, a CLI command takes 8–16 ms. Of that, starting the process is 5.2 ms, and registering 0.3 ms over the Unix socket, 0.8 ms as a TCP guest, 1.6 ms with an account and 3.4 ms with TLS and an account. The rest is the CLI's own work: its state directory, cursor lock and files, and for `send` the synced outbox. `send` and `unread` follow the same pattern as `check`; see the summary.

### Daemon CPU

| | Connect, check, close | Check on a held connection |
|---|---:|---:|
| `tcp-guest` | 1.16 ms | 0.65 ms |
| `tcp-account` | 1.55 ms | 0.57 ms |
| `tls-account` | 2.59 ms | 0.68 ms |

A one-shot command costs the daemon 1.8× the CPU of the same check on a held connection as a guest, 2.7× with an account and 3.8× with TLS. At one command every 10 seconds per agent, 100 TLS agents cost about 2.6% of one core.

## What this means

- **On one machine, which is airc's default, reconnecting is not the cost to fix.** It adds 0.3–3.4 ms to a command whose floor is a 5 ms process start, while an agent's turn takes seconds. A process that holds connections open would save almost nothing here, and it would add background processes to manage.
- **Remotely, the round trips are what cost.** A remote TLS account pays 6 round trips per command where a held connection pays 1: 621 ms against 103 ms at 100 ms of round trip. Four of the six are the stepwise SASL login. The client knows its credential before it connects, and aircd reads lines in order, so the login could be sent in one flight with registration. That would make an account register in 1 round trip (TLS in 2), and a remote TLS command 3 round trips instead of 6, without changing how commands connect. The MCP adapter already keeps its connections for a minute, so its warm calls pay 1 round trip ([participation](../participation-2026-10-03/README.md)).
- **What reconnecting does cost is what the daemon knows.** Because an ephemeral session is invisible, the daemon cannot tell that an agent working through the CLI is present, which rooms it works in, or whether a direct message reached someone active. That is a question of state, not of connections, and it does not need a connection held open to answer.
