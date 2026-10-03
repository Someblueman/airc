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
	nick, addr, unix                      string
	json                                  bool
	identityFile                          string
	tls                                   bool
	tlsCA, tlsServerName, accessTokenFile string
}

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		var result *commandFailure
		if !errors.As(err, &result) || !result.reported {
			fmt.Fprintln(os.Stderr, explain(err))
		}
		os.Exit(1)
	}
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
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
	case "history":
		return runHistory(args[1:], stdout, stderr)
	case "profile", "presence", "directory":
		return runDirectoryCommand(args[0], args[1:], stdout, stderr)
	case "search":
		return runSearch(args[1:], stdout, stderr)
	case "react":
		return runReact(args[1:], stdout, stderr)
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
	stopClose := context.AfterFunc(ctx, func() { _ = client.Close() })
	defer stopClose()
	if err := client.Raw("AGENTS"); err != nil {
		return err
	}
	agents := make([]irc.AgentInfo, 0)
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event, ok := <-client.Events():
			if !ok {
				return errors.New("server disconnected while listing agents")
			}
			switch value := event.(type) {
			case *irc.AgentsEvent:
				agents = append(agents, value.Agent)
			case *irc.EndOfAgentsEvent:
				if opt.json {
					return json.NewEncoder(stdout).Encode(agents)
				}
				for _, agent := range agents {
					if _, err := fmt.Fprintf(stdout, "%s\t%s\t%s\n", agent.Nick, strings.Join(agent.Channels, ","), agent.ConnectedAt.Format(time.RFC3339)); err != nil {
						return err
					}
				}
				return nil
			}
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return errors.New("timed out waiting for agent list")
		case <-client.Done():
			return errors.New("connection closed while listing agents")
		}
	}
}

func runHistory(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: airc history CHANNEL|NICK [--after MESSAGE_ID] [--limit 50] [--json]")
	}
	target := args[0]
	fs := flag.NewFlagSet("airc history", flag.ContinueOnError)
	fs.SetOutput(stderr)
	opt := addOptions(fs)
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
	ctx, cancel := commandContext()
	defer cancel()
	client, err := dialOneShot(ctx, *opt)
	if err != nil {
		return err
	}
	defer client.Close()
	stopClose := context.AfterFunc(ctx, func() { _ = client.Close() })
	defer stopClose()
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
		return fmt.Errorf("history cursor %q is no longer retained; read the latest messages without --after and continue from the newest ID", *after)
	}
	encoder := json.NewEncoder(stdout)
	for _, message := range messages {
		if opt.json {
			err = encoder.Encode(message)
		} else {
			_, err = fmt.Fprintf(stdout, "%s %s %s: %s\n", message.Timestamp.Format(time.RFC3339), message.Target, historyLabel(target, message), indentContinuation(chatBody(&irc.MessageEvent{ChatMetadata: message.ChatMetadata, ID: message.ID, From: message.From, Message: message.Message})))
		}
		if err != nil {
			return err
		}
	}
	if status == "more" && len(messages) > 0 {
		fmt.Fprintf(stderr, "airc: more messages are available; continue with --after %s\n", messages[len(messages)-1].ID)
	}
	return nil
}

func runNames(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return errors.New("usage: airc names #channel [--nick NAME] [--json]")
	}
	channel := args[0]
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
	stopClose := context.AfterFunc(ctx, func() { _ = client.Close() })
	defer stopClose()
	if err := client.Names(channel); err != nil {
		return err
	}

	nicks := make([]string, 0)
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event, ok := <-client.Events():
			if !ok {
				return errors.New("server disconnected while listing channel members")
			}
			response, ok := event.(*irc.RawEvent)
			if !ok {
				continue
			}
			switch response.Command {
			case "353":
				if len(response.Params) >= 3 && response.Params[2] == channel {
					nicks = append(nicks, strings.Fields(response.Trailing)...)
				}
			case "366":
				if len(response.Params) < 2 || response.Params[1] != channel {
					continue
				}
				if opt.json {
					return json.NewEncoder(stdout).Encode(nicks)
				}
				if len(nicks) == 0 {
					_, err := fmt.Fprintf(stdout, "No members in %s\n", channel)
					return err
				}
				_, err := fmt.Fprintf(stdout, "Members of %s: %s\n", channel, strings.Join(nicks, " "))
				return err
			case "403", "407", "421", "461":
				return fmt.Errorf("cannot list members of %s: %s", channel, response.Trailing)
			}
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return errors.New("timed out waiting for channel members")
		case <-client.Done():
			return errors.New("connection closed while listing channel members")
		}
	}
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

func dial(opt options) (*irc.Client, error) {
	cfg, err := dialConfig(opt)
	if err != nil {
		return nil, err
	}
	return irc.Dial(cfg)
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
	if strings.HasPrefix(a, "#") || strings.HasPrefix(a, "&") {
		return a == b
	}
	return strings.EqualFold(a, b)
}
