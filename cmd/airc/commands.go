package main

import (
	"context"
	"io"
	"os"
	"os/signal"
	"syscall"
)

// runner runs one command. For a session command the CLI passes a nil session
// and a bounded process context, and MCP passes its call context and the
// connection it leased, so both go through the same parsers and operations. A
// command that is not a session command manages its own context and connection
// and ignores both.
type runner func(ctx context.Context, session *agentConnection, name string, args []string, stdin io.Reader, stdout, stderr io.Writer) error

// command is one entry of commandTable: dispatch, airc -h, the hints for a
// command typed as a flag, the MCP adapter's eligibility check and the skill
// test all read it, so adding or renaming a command is one entry.
type command struct {
	name string
	run  runner
	// help is the command's lines in airc -h, verbatim, with two leading
	// spaces and no trailing newline; "" for a command the preamble covers or
	// one that shares the line of the entry before it (deop and kick on op's,
	// names on agents').
	help string
	// notes are hand-written lines printed after help: pointers to docs, and
	// forms of other commands that read best beside this one.
	notes string
	// group orders airc -h: commands print in table order with a blank line
	// where the group changes.
	group helpGroup
	// session marks a command that runs over a connection someone else may
	// own: the CLI gives it a bounded process context and the MCP adapter
	// may lease it a connection.
	session bool
	// mcp marks commands the MCP agent tool may run; they are session commands.
	mcp bool
	// signalsOnly runs the command under a context that only signals cancel,
	// with no request deadline.
	signalsOnly bool
}

// helpGroup is a section of the Commands list in airc -h.
type helpGroup string

const (
	groupSetup helpGroup = "setup" // accounts, rooms, presence, bots and the service
	groupAgent helpGroup = "agent" // the one-shot commands an agent uses
	groupChat  helpGroup = "chat"  // chat additions, listed in the preamble only
	groupAdmin helpGroup = "admin"
	groupHuman helpGroup = "human" // listings, the full-screen client and the interactive session
)

// plain adapts a session runner that needs neither the command name nor stdin.
func plain(run func(context.Context, *agentConnection, []string, io.Writer, io.Writer) error) runner {
	return func(ctx context.Context, session *agentConnection, _ string, args []string, _ io.Reader, stdout, stderr io.Writer) error {
		return run(ctx, session, args, stdout, stderr)
	}
}

// named adapts a session runner that serves several commands and so takes the name.
func named(run func(context.Context, *agentConnection, string, []string, io.Writer, io.Writer) error) runner {
	return func(ctx context.Context, session *agentConnection, name string, args []string, _ io.Reader, stdout, stderr io.Writer) error {
		return run(ctx, session, name, args, stdout, stderr)
	}
}

// namedReading adapts a session runner that serves several commands and reads stdin.
func namedReading(run func(context.Context, *agentConnection, string, []string, io.Reader, io.Writer, io.Writer) error) runner {
	return func(ctx context.Context, session *agentConnection, name string, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
		return run(ctx, session, name, args, stdin, stdout, stderr)
	}
}

// reading adapts a session runner that reads stdin.
func reading(run func(context.Context, *agentConnection, []string, io.Reader, io.Writer, io.Writer) error) runner {
	return func(ctx context.Context, session *agentConnection, _ string, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
		return run(ctx, session, args, stdin, stdout, stderr)
	}
}

// local adapts a command that manages its own context and connection.
func local(run func([]string, io.Writer, io.Writer) error) runner {
	return func(_ context.Context, _ *agentConnection, _ string, args []string, _ io.Reader, stdout, stderr io.Writer) error {
		return run(args, stdout, stderr)
	}
}

// localNamed adapts a local command that serves several names.
func localNamed(run func(string, []string, io.Writer, io.Writer) error) runner {
	return func(_ context.Context, _ *agentConnection, name string, args []string, _ io.Reader, stdout, stderr io.Writer) error {
		return run(name, args, stdout, stderr)
	}
}

// localReading adapts a local command that reads stdin.
func localReading(run func([]string, io.Reader, io.Writer, io.Writer) error) runner {
	return func(_ context.Context, _ *agentConnection, _ string, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
		return run(args, stdin, stdout, stderr)
	}
}

// commandTable is the one table of commands, in the order airc -h lists them.
var commandTable = []command{
	{name: "user", run: local(runUser), group: groupSetup,
		help: `  airc user create|login|path --nick N [--human|--agent]   reusable account; login uses SASL; --human marks a person`},
	{name: "op", run: localNamed(runRoomControl), group: groupSetup,
		help: `  airc op|deop|kick --channel '#room' --who N [--nick OP] [--token-file ADMIN_FILE]`},
	{name: "deop", run: localNamed(runRoomControl), group: groupSetup},
	{name: "kick", run: localNamed(runRoomControl), group: groupSetup},
	{name: "operators", run: localNamed(runRoomControl), group: groupSetup,
		help: `  airc operators --channel '#room'            list durable channel operators`},
	// away is presence --set away.
	{name: "away", session: true, group: groupSetup, run: func(ctx context.Context, session *agentConnection, _ string, args []string, _ io.Reader, stdout, stderr io.Writer) error {
		return runDirectoryCommandSession(ctx, session, "presence", append([]string{"--set", "away"}, args...), stdout, stderr)
	},
		help: `  airc away --nick N --message TEXT --ttl 20m  expiring away presence`},
	{name: "monitor", run: local(runMonitor), group: groupSetup,
		help: `  airc monitor --who alice,bob [--json]        live online/offline changes`},
	{name: "bot", run: local(runBot), group: groupSetup,
		help:  `  airc bot --nick utility --channel '#room'    native help/ping/calc bot`,
		notes: `  See docs/BOTS_AND_ROOMS.md for setup, limits, and the pkg/bot extension API.`},
	{name: "service", run: local(runService), group: groupSetup,
		help: `  airc service install|start|stop|restart|status|uninstall [--name NAME] [--state-dir DIR]
  airc service token init [--file PATH]         remote connection credential`,
		notes: `  Service defaults to loopback with persistent history. See docs/SERVICE.md for TLS remote access.`},

	{name: "send", run: reading(runSendSession), session: true, mcp: true, group: groupAgent,
		help: `  airc send  [--nick N] (--channel #room | --to N) [--message TEXT|-] [--file PATH|-] [--paste PATH|-] [--language go] [--check] [--json]
  airc send --retry REQUEST_ID [--nick N] [--json]   receipt recovery; never posts
  airc send --pending [--nick N] [--json]            uncertain local sends
  airc send --forget REQUEST_ID [--nick N]           remove a local outbox entry
  airc send ... [--request-id KEY] [--run ID] [--max-messages N] [--max-bytes N]   explicit retry key; run label (env AIRC_RUN); --check page budgets`},
	{name: "check", run: plain(runCheckSession), session: true, mcp: true, signalsOnly: true, group: groupAgent,
		help: `  airc check [--nick N] [--channel #room]... [--wait 60s] [--mentions] [--peek] [--include-own] [--json]
             [--max-messages 100] [--max-bytes 32768] [--initial 20] [--limit N]`},
	{name: "ask", run: reading(runAskSession), session: true, mcp: true, signalsOnly: true, group: groupAgent,
		help: `  airc ask [--nick N] (--channel #room | --to N) --message TEXT|- [--wait 60s] [--run ID] [--json]   post a question and wait for its first reply`},
	{name: "unread", run: plain(runUnreadSession), session: true, mcp: true, group: groupAgent,
		help: `  airc unread [--nick N] [--channel #room]... [--mentions] [--json]   counts only; marks nothing read; silent when empty`},
	{name: "digest", run: plain(runDigestSession), session: true, mcp: true, group: groupAgent,
		help: `  airc digest [--nick N] [--channel #room]... [--mark-read] [--json]   who said how much where, what waits on you, your uncertain sends; no bodies`},
	{name: "channels", run: plain(runChannelsSession), session: true, mcp: true, group: groupAgent,
		help:  `  airc channels [--json]                         rooms the server knows, with headers and activity`,
		notes: `  airc check ... [--compact] [--from-now]        smaller JSON rows; skip the retained backlog once`},
	{name: "history", run: plain(runHistorySession), session: true, mcp: true, group: groupAgent,
		help: `  airc history #room|NICK|'@*' [--after MESSAGE_ID] [--limit 50] [--run ID] [--json]   a bare name is a nick's direct messages`},
	{name: "context", run: plain(runContextSession), session: true, mcp: true, group: groupAgent,
		help: `  airc context MESSAGE_ID [--limit 50] [--max-bytes 32768] [--json]`},
	{name: "mcp", run: localReading(runMCP), group: groupAgent,
		help: `  airc mcp --nick N [connection flags]           optional stdio tools for agents`},
	{name: "thread", run: plain(runThreadSession), session: true, mcp: true, group: groupAgent,
		help: `  airc thread MESSAGE_ID [--after ID] [--limit 50] [--json]`,
		notes: `  airc send --reply-to MESSAGE_ID --message TEXT [--check] [--json]
  airc check --reply-to MESSAGE_ID [--wait 60s] [--peek] [--json]`},
	{name: "directory", run: named(runDirectoryCommandSession), session: true, mcp: true, group: groupAgent,
		help: `  airc directory [--who NICK] [--json]`},
	{name: "profile", run: named(runDirectoryCommandSession), session: true, mcp: true, group: groupAgent,
		help: `  airc profile [--nick N] [--human|--agent] [--model M] [--workspace PATH] [--tools TEXT] [--about TEXT] [--clear]`},
	{name: "presence", run: named(runDirectoryCommandSession), session: true, mcp: true, group: groupAgent,
		help: `  airc presence [--nick N] [--set thinking|running|away|available] [--message TEXT] [--ttl 5m] [--clear]`},
	{name: "search", run: plain(runSearchSession), session: true, mcp: true, group: groupAgent,
		help: `  airc search 'WORDS "phrase" -not' [--target '#room'|@nick|thread:ID|'*'] [--from NICK] [--run ID] [--since 2h] [--until T] [--newest] [--exact] [--after ID] [--limit 50] [--json]`},
	{name: "react", run: reading(runReactSession), session: true, mcp: true, group: groupAgent,
		help: `  airc react MESSAGE_ID seen|checking|agree|disagree [--nick N] [--json]`},
	{name: "paste", run: reading(runPasteSession), session: true, mcp: true, group: groupAgent,
		help: `  airc paste get PASTE_ID [--out FILE] [--json]   a shared paste's text; info PASTE_ID its name, size and author
  airc paste list [--nick N] [--limit N] [--json]   your own pastes; put FILE|- [--name N] uploads one without posting`,
		notes: `  airc send ... --paste PATH|- [--language L] [--message CAPTION]   share a diff, a log or test output larger than a message`},
	{name: "status", run: reading(runStatusSession), session: true, mcp: true, group: groupAgent,
		help: `  airc status --nick N --channel #room --task TEXT --state working|blocked|done [--link URL] [--message NOTE] [--json]   post where you are
  airc status --channel #room [--json]           the room's latest status per nickname, newest first`},
	{name: "resolve", run: reading(runResolveSession), session: true, mcp: true, group: groupAgent,
		help: `  airc resolve MESSAGE_ID --nick N [--message NOTE] [--json]   close a question settled elsewhere; author, addressee or admin`},
	{name: "handoff", run: reading(runHandoffSession), session: true, mcp: true, group: groupAgent,
		help: `  airc handoff (--channel #room [--to N] | --to N) --message TEXT|- [--json]   hand work over; in a room, first to accept takes it
  airc handoff accept|decline|done|withdraw ID [--message NOTE] [--json]   take it, give it back, finish it, or call it off
  airc handoff blocked ID --message WHY [--json]      say the handoff you hold is stuck; accept again when it moves
  airc handoff done ID --link URL [--json]            finish it and record where the result is`},
	{name: "handoffs", run: named(runListingSession), session: true, mcp: true, group: groupAgent,
		help: `  airc handoffs [--channel #room | --id ID] [--limit N] [--json]   open handoffs for you, held by you or offered by you`},
	{name: "decide", run: reading(runDecideSession), session: true, mcp: true, group: groupAgent,
		help: `  airc decide (--channel #room --from N | --to N) --message TEXT|- [--option A --option B]... [--json]   ask N, usually a human, to decide
  airc decide answer|withdraw ID [--choice N] [--message NOTE] [--json]   answer a decision asked of you, or call yours off`},
	{name: "decisions", run: named(runListingSession), session: true, mcp: true, group: groupAgent,
		help:  `  airc decisions [--channel #room | --id ID] [--limit N] [--json]   open decisions for you to make or that you asked`,
		notes: `  airc check --decision ID [--wait 120s] [--json]   wait for a decision to be answered or withdrawn`},
	{name: "doctor", run: local(runDoctor), group: groupAgent,
		help: `  airc doctor [--nick N] [--pid PID] [--json]   capabilities, retention, locks and descriptors`},

	// Chat additions: the preamble of airc -h lists them.
	{name: "follow", run: named(runFollowSession), session: true, mcp: true, group: groupChat},
	{name: "unfollow", run: named(runFollowSession), session: true, mcp: true, group: groupChat},
	{name: "following", run: named(runFollowSession), session: true, group: groupChat},
	{name: "correct", run: namedReading(runChatCommandSession), session: true, mcp: true, group: groupChat},
	{name: "retract", run: namedReading(runChatCommandSession), session: true, mcp: true, group: groupChat},
	{name: "prepare", run: namedReading(runChatCommandSession), session: true, mcp: true, group: groupChat},
	{name: "waiting", run: namedReading(runChatCommandSession), session: true, mcp: true, group: groupChat},
	{name: "pin", run: namedReading(runChatCommandSession), session: true, mcp: true, group: groupChat},
	{name: "unpin", run: namedReading(runChatCommandSession), session: true, mcp: true, group: groupChat},
	{name: "pins", run: namedReading(runChatCommandSession), session: true, mcp: true, group: groupChat},
	{name: "room", run: namedReading(runChatCommandSession), session: true, mcp: true, group: groupChat},
	{name: "me", run: namedReading(runChatCommandSession), session: true, group: groupChat},
	{name: "typing", run: namedReading(runChatCommandSession), session: true, group: groupChat},
	{name: "thinking", run: namedReading(runChatCommandSession), session: true, group: groupChat},
	{name: "poll", run: namedReading(runChatCommandSession), session: true, mcp: true, group: groupChat},
	{name: "vote", run: namedReading(runChatCommandSession), session: true, mcp: true, group: groupChat},
	{name: "poll-results", run: namedReading(runChatCommandSession), session: true, mcp: true, group: groupChat},
	{name: "poll-close", run: namedReading(runChatCommandSession), session: true, mcp: true, group: groupChat},

	{name: "admin", run: local(runAdmin), group: groupAdmin,
		help: `  airc admin init [--token-file PATH]          create an owner-only admin credential
  airc admin mute|ban N [--channel #room] [--for 10m] [--reason TEXT] [--json]
  airc admin unmute|unban N [--channel #room] [--json]
  airc admin kick N [--reason TEXT] [--json]   disconnect all current sessions; reconnection allowed
  airc admin list [--json]                    active mutes and bans
  airc admin account-list [--json]            registered accounts (nickname and ID)
  airc admin account-delete N [--json]        remove an account so its nickname can be registered again`,
		notes: `  Admin commands accept --token-file PATH; enable the daemon with --admin-token-file PATH.`},

	{name: "agents", run: local(runAgents), group: groupHuman,
		help: `  airc agents [--json]         airc names #room [--json]`},
	{name: "names", run: local(runNames), group: groupHuman},
	{name: "ui", run: local(runUI), group: groupHuman,
		help: `  airc ui [--nick N] [--channel #room,...] [--color auto|always|never]   full-screen client with All DMs human oversight`},
	{name: "topic", run: local(runTopic), group: groupHuman,
		help: `  airc topic #room [--set TEXT|--clear]   the channel header agents see on their first check`},
	{name: "charter", run: plain(runCharterSession), session: true, group: groupHuman,
		help: `  airc charter #room [--json]             the room's charter, its longer rules, shown like the header
  airc room #room --charter FILE|-|'' [--as-operator|--token-file F]   set or clear it (operator or admin)
  airc room #room --orchestrate NICK|off [--goal TEXT] [--token-file F]   orchestrator mode: only NICK hands work out there (admin)`},
	{name: "wake", run: local(runWake), group: groupHuman,
		help: `  airc wake --nick N [--url URL [--secret-file F] [--quiet 1m] [--mentions-only] [--quiet-hours 22:00-08:00] | --clear]
                                                             signed notices while offline; also for a person's phone`},
	{name: "skill", run: local(runSkill), group: groupHuman,
		help: `  airc skill show|path|install [--dir DIR|--project]         the agent skill for this version
  airc skill reference [SECTION|--list]                       its reference, or one section by heading`},
	{name: "watch", run: local(runWatch), group: groupHuman,
		help: `  airc watch [--channel #room|@nick[,...]] [--all-dms] [--json] [--color auto|always|never] [--width N]
                                                             live stream for a human monitor
  airc watch --nick N --channel #room [--mentions] [--bell] [--notify [--quiet-hours 22:00-08:00]]
                                                             only, or with a bell or desktop notice for, what tags or DMs you`},
	{name: "interactive", run: localReading(runInteractive), group: groupHuman,
		help: `  airc --nick N [--channel #general]                         persistent interactive session`},
}

// commandsByName indexes commandTable, and commands names its entries, so a
// command typed as a flag (airc --watch) gets a hint instead of only an
// unknown-flag error. Both are filled in init because the runners reach hints,
// which read them.
var (
	commandsByName map[string]*command
	commands       []string
)

func init() {
	commandsByName = make(map[string]*command, len(commandTable))
	for i := range commandTable {
		c := &commandTable[i]
		commandsByName[c.name] = c
		commands = append(commands, c.name)
	}
}

// runCommand runs a command as a CLI entry point: a session command with its
// own bounded process context and no leased connection.
func runCommand(c *command, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	ctx := context.Background()
	if c.session {
		var cancel context.CancelFunc
		if c.signalsOnly {
			ctx, cancel = signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		} else {
			ctx, cancel = commandContext()
		}
		defer cancel()
	}
	return c.run(ctx, nil, c.name, args, stdin, stdout, stderr)
}

// runSessionCommand runs the session command called name as a CLI entry point,
// for a caller that has only the name, such as the UI's /follow.
func runSessionCommand(name string, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	return runCommand(commandsByName[name], args, stdin, stdout, stderr)
}
