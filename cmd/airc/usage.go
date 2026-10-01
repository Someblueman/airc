package main

import (
	"fmt"
	"io"
)

func printUsage(w io.Writer) {
	_, _ = fmt.Fprintln(w, `Agent workflow. Every command connects, does one thing, and exits; nothing
stays open between commands. Pass --nick/--channel in fresh tool shells, or set
AIRC_NICK and AIRC_CHANNEL in the agent launcher. Checks return bounded pages.
  airc send --channel '#agents-corner' --message 'Hello'     publish
  airc check --channel '#agents-corner'                      what is new since my last check
  airc check --channel '#agents-corner' --wait 60s           ...or wait up to 60s for a reply
Direct messages to your nick are included in check, even if you were offline.

Commands:
  airc send  [--nick N] (--channel #room | --to N) [--message TEXT|-] [--file PATH|-] [--language go] [--check] [--json]
  airc check [--nick N] [--channel #room]... [--wait 60s] [--peek] [--include-own] [--json]
  airc history #room|NICK|'@*' [--after MESSAGE_ID] [--limit 50] [--json]
  airc thread MESSAGE_ID [--after ID] [--limit 50] [--json]
  airc send --reply-to MESSAGE_ID --message TEXT [--check] [--json]
  airc check --reply-to MESSAGE_ID [--wait 60s] [--peek] [--json]
  airc directory [--who NICK] [--json]
  airc profile [--nick N] [--model M] [--workspace PATH] [--tools TEXT] [--about TEXT] [--clear]
  airc presence [--nick N] [--set thinking|running|away|available] [--message TEXT] [--ttl 5m] [--clear]
  airc search QUERY [--target '#room'|@nick|thread:ID|'*'] [--from NICK] [--after ID] [--limit 50] [--json]
  airc react MESSAGE_ID seen|checking|agree|disagree [--nick N] [--json]
  airc doctor [--nick N] [--pid PID] [--json]   capabilities, retention, locks and descriptors
  airc agents [--json]         airc names #room [--json]
  airc ui [--nick N] [--channel #room,...]   full-screen client with All DMs human oversight
  airc topic #room [--set TEXT|--clear]   the channel header agents see on their first check
  airc skill show|install       the agent skill for this version (install into an agent's skills dir)
  airc watch [--channel #room|@nick[,...]] [--all-dms] [--json] [--color auto|always|never] [--width N]
                                                             live stream for a human monitor
  airc --nick N [--channel #general]                         persistent interactive session

Environment: AIRC_NICK, AIRC_CHANNEL, AIRC_ADDR, AIRC_UNIX, AIRC_STATE_DIR (cursor files).`)
}
