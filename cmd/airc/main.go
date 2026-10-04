package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

type options struct {
	session                               *agentConnection
	nick, addr, unix                      string
	json                                  bool
	identityFile                          string
	tls                                   bool
	tlsCA, tlsServerName, accessTokenFile string
}

func main() {
	err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	if err == nil || errors.Is(err, flag.ErrHelp) {
		return // on ErrHelp the flag package already printed the command's usage
	}
	writeFailure(os.Stderr, err)
	os.Exit(1)
}

// writeFailure prints err unless the command already reported it: as a JSON
// error object, or as the flag package's error line and usage, after which only
// the hints are new.
func writeFailure(w io.Writer, err error) {
	if parse, ok := errors.AsType[*flagParseError](err); ok {
		for _, hint := range hints(parse.err) {
			fmt.Fprintln(w, hint)
		}
		return
	}
	if result, ok := errors.AsType[*commandFailure](err); ok && result.reported {
		return
	}
	fmt.Fprintln(w, explain(err))
}

// flagParseError marks a parse failure the flag package has already printed.
type flagParseError struct{ err error }

func (e *flagParseError) Error() string { return e.err.Error() }
func (e *flagParseError) Unwrap() error { return e.err }

// run dispatches one command. With --json, every subcommand reports failure as
// the same error object on stderr that send and check use.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	machine := wantsJSON(args)
	diagnostics := &flagErrorWriter{w: stderr, drop: machine}
	err := dispatch(args, stdin, stdout, diagnostics)
	if err != nil && diagnostics.seen && !machine && !errors.Is(err, flag.ErrHelp) {
		err = &flagParseError{err: err}
	}
	reportFailure(&err, machine, stderr)
	return err
}

// flagErrorWriter notices the flag package's error line. With drop set it also
// discards that line and the usage dump after it, which a JSON caller would
// otherwise have to skip to find the error object.
type flagErrorWriter struct {
	w    io.Writer
	drop bool
	seen bool
}

func (f *flagErrorWriter) Write(p []byte) (int, error) {
	for _, prefix := range []string{"flag provided but not defined", "flag needs an argument", "invalid value ", "invalid boolean "} {
		if strings.HasPrefix(string(p), prefix) {
			f.seen = true // parsing failed; the command returns right after
		}
	}
	if f.drop && f.seen {
		return len(p), nil
	}
	return f.w.Write(p)
}

func wantsJSON(args []string) bool {
	for _, arg := range args {
		switch arg {
		case "--":
			return false
		case "--json", "-json", "--json=true", "-json=true":
			return true
		}
	}
	return false
}

// commands names dispatch's subcommands, so a command typed as a flag
// (airc --watch) gets a hint instead of only an unknown-flag error.
var commands = []string{
	"op", "deop", "operators", "kick", "monitor", "away", "bot", "follow", "unfollow", "following",
	"pin", "unpin", "pins", "prepare", "waiting", "room", "me", "correct", "retract", "typing", "thinking",
	"poll", "vote", "poll-results", "poll-close", "service", "user", "admin", "send", "watch", "agents",
	"skill", "topic", "ui", "check", "unread", "channels", "history", "profile", "presence", "directory",
	"search", "react", "context", "mcp", "thread", "names", "doctor", "interactive",
}

func dispatch(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h" || args[0] == "-help") {
		printUsage(stdout)
		return nil
	}
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return runInteractive(args, stdin, stdout, stderr)
	}
	switch args[0] {
	case "op", "deop", "operators", "kick":
		return runRoomControl(args[0], args[1:], stdout, stderr)
	case "monitor":
		return runMonitor(args[1:], stdout, stderr)
	case "away":
		return runDirectoryCommand("presence", append([]string{"--set", "away"}, args[1:]...), stdout, stderr)
	case "bot":
		return runBot(args[1:], stdout, stderr)
	case "follow", "unfollow", "following":
		return runFollow(args[0], args[1:], stdout, stderr)
	case "pin", "unpin", "pins", "prepare", "waiting", "room", "me", "correct", "retract", "typing", "thinking", "poll", "vote", "poll-results", "poll-close":
		return runChatCommand(args[0], args[1:], stdout, stderr)
	case "service":
		return runService(args[1:], stdout, stderr)
	case "user":
		return runUser(args[1:], stdout, stderr)
	case "admin":
		return runAdmin(args[1:], stdout, stderr)
	case "send":
		return runSend(args[1:], stdin, stdout, stderr)
	case "watch":
		return runWatch(args[1:], stdout, stderr)
	case "agents":
		return runAgents(args[1:], stdout, stderr)
	case "skill":
		return runSkill(args[1:], stdout, stderr)
	case "topic":
		return runTopic(args[1:], stdout, stderr)
	case "ui":
		return runUI(args[1:], stdout, stderr)
	case "check":
		return runCheck(args[1:], stdout, stderr)
	case "unread":
		return runUnread(args[1:], stdout, stderr)
	case "channels":
		return runChannels(args[1:], stdout, stderr)
	case "history":
		return runHistory(args[1:], stdout, stderr)
	case "profile", "presence", "directory":
		return runDirectoryCommand(args[0], args[1:], stdout, stderr)
	case "search":
		return runSearch(args[1:], stdout, stderr)
	case "react":
		return runReact(args[1:], stdout, stderr)
	case "context":
		return runContext(args[1:], stdout, stderr)
	case "mcp":
		return runMCP(args[1:], stdin, stdout, stderr)
	case "thread":
		return runThread(args[1:], stdout, stderr)
	case "names":
		return runNames(args[1:], stdout, stderr)
	case "doctor":
		return runDoctor(args[1:], stdout, stderr)
	case "interactive":
		return runInteractive(args[1:], stdin, stdout, stderr)
	case "help", "--help", "-h":
		printUsage(stdout)
		return nil
	default:
		return fmt.Errorf("unknown command %q (see airc help)", args[0])
	}
}

func runAgents(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("airc agents", flag.ContinueOnError)
	fs.SetOutput(stderr)
	opt := addOptions(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := queryIdentity(opt); err != nil {
		return err
	}
	ctx, cancel := commandContext()
	defer cancel()
	client, err := dialOneShot(ctx, *opt)
	if err != nil {
		return err
	}
	defer client.Close()
	defer closeOnCancel(ctx, client)()
	if err := client.Raw("AGENTS"); err != nil {
		return err
	}
	agents := make([]irc.AgentInfo, 0)
	return awaitEvent(ctx, client, 10*time.Second, "listing agents", "timed out waiting for agent list", func(event irc.Event) (bool, error) {
		switch value := event.(type) {
		case *irc.AgentsEvent:
			agents = append(agents, value.Agent)
		case *irc.EndOfAgentsEvent:
			if opt.json {
				return true, json.NewEncoder(stdout).Encode(agents)
			}
			for _, agent := range agents {
				if _, err := fmt.Fprintf(stdout, "%s\t%s\t%s\n", agent.Nick, strings.Join(agent.Channels, ","), agent.ConnectedAt.Format(time.RFC3339)); err != nil {
					return true, err
				}
			}
			return true, nil
		}
		return false, nil
	})
}

func runHistorySession(ctx context.Context, session *agentConnection, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: airc history CHANNEL|NICK [--after MESSAGE_ID] [--limit 50] [--json]")
	}
	target := args[0]
	fs := flag.NewFlagSet("airc history", flag.ContinueOnError)
	fs.SetOutput(stderr)
	opt := addOptions(fs)
	opt.session = session
	limit := fs.Int("limit", 50, "maximum messages to return")
	after := fs.String("after", "", "exclusive message ID cursor")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("history accepts one target followed by options")
	}
	if *limit < 1 || *limit > 1000 {
		return errors.New("--limit must be between 1 and 1000")
	}
	if err := queryIdentity(opt); err != nil {
		return err
	}
	client, err := dialOneShot(ctx, *opt)
	if err != nil {
		return err
	}
	defer closeOneShot(*opt, client)
	defer closeOnCancel(ctx, client)()
	if _, _, conversation := protocol.ConversationTarget(target); conversation {
		if !client.Supports("REPLIES") {
			return errors.New("conversation history needs a daemon with REPLIES; upgrade/restart when active work is finished")
		}
		if *after == "" {
			*after = "*"
		}
	}
	if !client.Ephemeral() {
		// Older servers only serve channels, and only to members.
		if !isChannel(target) {
			return errors.New("this aircd cannot read direct-message history; restart it from a current build")
		}
		if err := client.Join(target); err != nil {
			return err
		}
	}
	if target == irc.AllDirectMessages && !client.Supports("DM_AUDIT") {
		return errors.New("all-DM history needs a daemon with DM_AUDIT; upgrade/restart when active work is finished")
	}
	page, err := fetchHistory(ctx, client, target, *after, *limit, nil)
	if err != nil {
		return err
	}
	messages, status := page.messages, page.status
	switch {
	case status == "" && *after != "":
		// Older servers ignore the cursor and return the latest messages.
		cursor := -1
		for index, message := range messages {
			if message.ID == *after {
				cursor = index
				break
			}
		}
		if cursor < 0 {
			return fmt.Errorf("history cursor %q is not in the latest %d messages; it may have expired", *after, *limit)
		}
		messages = messages[cursor+1:]
	case status == "expired":
		if session != nil {
			_ = writePageStatus(stdout, page)
		}
		return fmt.Errorf("history cursor %q is no longer retained; read the latest messages without --after and continue from the newest ID", *after)
	}
	encoder := json.NewEncoder(stdout)
	for _, message := range messages {
		if opt.json {
			err = encoder.Encode(message)
		} else {
			_, err = fmt.Fprintf(stdout, "%s %s %s: %s\n", message.Timestamp.Format(time.RFC3339), message.Target, historyLabel(target, message), indentContinuation(chatBody(message.ChatMetadata, message.ID, message.From, message.Message)))
		}
		if err != nil {
			return err
		}
	}
	if status == "more" && len(messages) > 0 {
		fmt.Fprintf(stderr, "airc: more messages are available; continue with --after %s\n", messages[len(messages)-1].ID)
	}
	if session != nil {
		return writePageStatus(stdout, page)
	}
	return nil
}

func runNames(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return errors.New("usage: airc names #channel [--nick NAME] [--json]")
	}
	channel := channelName(args[0])
	fs := flag.NewFlagSet("airc names", flag.ContinueOnError)
	fs.SetOutput(stderr)
	opt := addOptions(fs)
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: airc names #channel [--nick NAME] [--json]")
	}
	if err := queryIdentity(opt); err != nil {
		return err
	}
	ctx, cancel := commandContext()
	defer cancel()
	client, err := dialOneShot(ctx, *opt)
	if err != nil {
		return err
	}
	defer client.Close()
	defer closeOnCancel(ctx, client)()
	if err := client.Names(channel); err != nil {
		return err
	}

	nicks := make([]string, 0)
	return awaitEvent(ctx, client, 10*time.Second, "listing channel members", "timed out waiting for channel members", func(event irc.Event) (bool, error) {
		response, ok := event.(*irc.RawEvent)
		if !ok {
			return false, nil
		}
		switch response.Command {
		case "353":
			if len(response.Params) >= 3 && response.Params[2] == channel {
				nicks = append(nicks, strings.Fields(response.Trailing)...)
			}
		case "366":
			if len(response.Params) < 2 || response.Params[1] != channel {
				return false, nil
			}
			if opt.json {
				return true, json.NewEncoder(stdout).Encode(nicks)
			}
			if len(nicks) == 0 {
				_, err := fmt.Fprintf(stdout, "No members in %s\n", channel)
				return true, err
			}
			_, err := fmt.Fprintf(stdout, "Members of %s: %s\n", channel, strings.Join(nicks, " "))
			return true, err
		case "403", "407", "421", "461":
			return true, fmt.Errorf("cannot list members of %s: %s", channel, response.Trailing)
		}
		return false, nil
	})
}

func addOptions(fs *flag.FlagSet) *options {
	opt := &options{}
	fs.StringVar(&opt.nick, "nick", "", "agent nickname (env AIRC_NICK for send, check and interactive)")
	fs.StringVar(&opt.addr, "addr", envOr("AIRC_ADDR", "127.0.0.1:6667"), "TCP server address (env AIRC_ADDR)")
	fs.StringVar(&opt.unix, "unix", os.Getenv("AIRC_UNIX"), "Unix socket path (env AIRC_UNIX)")
	fs.BoolVar(&opt.json, "json", false, "emit machine-readable JSON")
	fs.StringVar(&opt.identityFile, "identity", os.Getenv("AIRC_IDENTITY_FILE"), "registered user identity file; defaults to this server/nickname's saved identity")
	addTransportOptions(fs, opt)
	return opt
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func defaultQueryNick() string {
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err == nil {
		return "observer-" + hex.EncodeToString(suffix[:])
	}
	return fmt.Sprintf("observer-%d", time.Now().UnixNano())
}

func clientConfig(opt options) irc.Config {
	network, address := "tcp", opt.addr
	if opt.unix != "" {
		network, address = "unix", opt.unix
	}
	return irc.Config{Nick: opt.nick, Addr: address, Network: network}
}

// indentContinuation indents the second and later lines of a multi-line message
// so each message stays visually distinct in line-oriented output.
func indentContinuation(message string) string {
	return strings.ReplaceAll(message, "\n", "\n  ")
}

func sameTarget(a, b string) bool {
	if isChannel(a) {
		return a == b
	}
	return strings.EqualFold(a, b)
}
