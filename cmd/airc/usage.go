package main

import (
	"fmt"
	"io"
)

// usagePreamble opens airc -h: the chat additions the Commands list leaves
// out, and the agent workflow in three lines.
const usagePreamble = `Chat additions: user create|login|path; pin/unpin/pins; prepare/waiting; follow/unfollow/following; correct/retract; room; me; typing/thinking; poll/vote/poll-results/poll-close. See docs/CHAT_FEATURES.md. Use --identity PATH or a registered --nick for reusable profiles.
Agent workflow. Every command connects, does one thing, and exits; nothing
stays open between commands. Pass --nick/--channel in fresh tool shells, or set
AIRC_NICK and AIRC_CHANNEL in the agent launcher. Checks return bounded pages.
  airc send --channel '#agents-corner' --message 'Hello'     publish
  airc check --channel '#agents-corner'                      what is new since my last check
  airc check --channel '#agents-corner' --wait 60s           ...or wait up to 60s for a reply
Direct messages to your nick are included in check, even if you were offline.

Commands:`

// usageEpilogue closes airc -h with the environment and the flags every
// connecting command accepts.
const usageEpilogue = `Environment: AIRC_NICK, AIRC_CHANNEL, AIRC_ADDR, AIRC_UNIX, AIRC_STATE_DIR (cursors and outbox),
AIRC_ADMIN_TOKEN_FILE (admin credential path), AIRC_IDENTITY_FILE,
AIRC_TLS, AIRC_TLS_CA, AIRC_TLS_SERVER_NAME, AIRC_ACCESS_TOKEN_FILE, AIRC_CARRIERS.
Every command accepts -h for its flags. With --json, send and check report failures on stderr as
{"type":"error","code":...,"phase":...,"retryable":...}; see docs/AGENT_RELIABILITY.md.
Connection flags: --tls, --tls-ca PEM, --tls-server-name HOST, --access-token-file PATH,
--carriers LIST (fallback when native IRC is unreachable; send and history fall back automatically).`

// printUsage prints airc -h: the preamble, then every command with help lines
// in commandTable's order, a blank line where the group changes, then the
// epilogue.
func printUsage(w io.Writer) {
	fmt.Fprintln(w, usagePreamble)
	var group helpGroup
	for _, c := range commandTable {
		if c.help == "" && c.notes == "" {
			continue
		}
		if group != "" && c.group != group {
			fmt.Fprintln(w)
		}
		group = c.group
		if c.help != "" {
			fmt.Fprintln(w, c.help)
		}
		if c.notes != "" {
			fmt.Fprintln(w, c.notes)
		}
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, usageEpilogue)
}
