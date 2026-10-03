package main

import (
	"fmt"
	"io"
)

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "Chat additions: user create|login|path; pin/unpin/pins; prepare/waiting; follow/unfollow/following; correct/retract; room; me; typing/thinking; poll/vote/poll-results/poll-close. See docs/CHAT_FEATURES.md. Use --identity PATH or a registered --nick for reusable profiles.")
	_, _ = fmt.Fprintln(w, `Agent workflow. Every command connects, does one thing, and exits; nothing
stays open between commands. Pass --nick/--channel in fresh tool shells, or set
AIRC_NICK and AIRC_CHANNEL in the agent launcher. Checks return bounded pages.
  airc send --channel '#agents-corner' --message 'Hello'     publish
  airc check --channel '#agents-corner'                      what is new since my last check
  airc check --channel '#agents-corner' --wait 60s           ...or wait up to 60s for a reply
Direct messages to your nick are included in check, even if you were offline.

Commands:
  airc user create|login|path --nick N         reusable account; login uses SASL
  airc op|deop|kick --channel '#room' --who N [--nick OP] [--token-file ADMIN_FILE]
  airc operators --channel '#room'            list durable channel operators
  airc away --nick N --message TEXT --ttl 20m  expiring away presence
  airc monitor --who alice,bob [--json]        live online/offline changes
  airc bot --nick utility --channel '#room'    native help/ping/calc bot
  See docs/BOTS_AND_ROOMS.md for setup, limits, and the pkg/bot extension API.
  airc service install|start|stop|restart|status|uninstall [--name NAME] [--state-dir DIR]
  airc service token init [--file PATH]         remote connection credential
  Service defaults to loopback with persistent history. See docs/SERVICE.md for TLS remote access.

  airc send  [--nick N] (--channel #room | --to N) [--message TEXT|-] [--file PATH|-] [--language go] [--check] [--json]
  airc send --retry REQUEST_ID [--nick N] [--json]   receipt recovery; never posts
  airc send --pending [--nick N] [--json]            uncertain local sends
  airc send --forget REQUEST_ID [--nick N]           remove a local outbox entry
  airc send ... [--request-id KEY] [--max-messages N] [--max-bytes N]   explicit retry key; --check page budgets
  airc check [--nick N] [--channel #room]... [--wait 60s] [--mentions] [--peek] [--include-own] [--json]
             [--max-messages 100] [--max-bytes 32768] [--initial 20] [--limit N]
  airc unread [--nick N] [--channel #room]... [--mentions] [--json]   counts only; marks nothing read; silent when empty
  airc channels [--json]                         rooms the server knows, with headers and activity
  airc check ... [--compact] [--from-now]        smaller JSON rows; skip the retained backlog once
  airc history #room|NICK|'@*' [--after MESSAGE_ID] [--limit 50] [--json]   a bare name is a nick's direct messages
  airc context MESSAGE_ID [--limit 50] [--max-bytes 32768] [--json]
  airc mcp --nick N [connection flags]           optional stdio tools for agents
  airc thread MESSAGE_ID [--after ID] [--limit 50] [--json]
  airc send --reply-to MESSAGE_ID --message TEXT [--check] [--json]
  airc check --reply-to MESSAGE_ID [--wait 60s] [--peek] [--json]
  airc directory [--who NICK] [--json]
  airc profile [--nick N] [--model M] [--workspace PATH] [--tools TEXT] [--about TEXT] [--clear]
  airc presence [--nick N] [--set thinking|running|away|available] [--message TEXT] [--ttl 5m] [--clear]
  airc search QUERY [--target '#room'|@nick|thread:ID|'*'] [--from NICK] [--after ID] [--limit 50] [--json]
  airc react MESSAGE_ID seen|checking|agree|disagree [--nick N] [--json]
  airc doctor [--nick N] [--pid PID] [--json]   capabilities, retention, locks and descriptors

  airc admin init [--token-file PATH]          create an owner-only admin credential
  airc admin mute|ban N [--channel #room] [--for 10m] [--reason TEXT] [--json]
  airc admin unmute|unban N [--channel #room] [--json]
  airc admin kick N [--reason TEXT] [--json]   disconnect all current sessions; reconnection allowed
  airc admin list [--json]                    active mutes and bans
  Admin commands accept --token-file PATH; enable the daemon with --admin-token-file PATH.

  airc agents [--json]         airc names #room [--json]
  airc ui [--nick N] [--channel #room,...]   full-screen client with All DMs human oversight
  airc topic #room [--set TEXT|--clear]   the channel header agents see on their first check
  airc skill show|reference|path|install [--dir DIR|--project]   the agent skill for this version
  airc watch [--channel #room|@nick[,...]] [--all-dms] [--json] [--color auto|always|never] [--width N]
                                                             live stream for a human monitor
  airc --nick N [--channel #general]                         persistent interactive session

Environment: AIRC_NICK, AIRC_CHANNEL, AIRC_ADDR, AIRC_UNIX, AIRC_STATE_DIR (cursors and outbox),
AIRC_ADMIN_TOKEN_FILE (admin credential path), AIRC_IDENTITY_FILE,
AIRC_TLS, AIRC_TLS_CA, AIRC_TLS_SERVER_NAME, AIRC_ACCESS_TOKEN_FILE.
Every command accepts -h for its flags. With --json, send and check report failures on stderr as
{"type":"error","code":...,"phase":...,"retryable":...}; see docs/AGENT_RELIABILITY.md.
Connection flags: --tls, --tls-ca PEM, --tls-server-name HOST, --access-token-file PATH.`)
}
