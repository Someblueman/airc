package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Someblueman/airc/pkg/irc"
)

type options struct {
	nick, addr, unix string
	json             bool
}

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, explain(err))
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
	case "check":
		return runCheck(args[1:], stdout, stderr)
	case "history":
		return runHistory(args[1:], stdout, stderr)
	case "names":
		return runNames(args[1:], stdout, stderr)
	case "interactive":
		return runInteractive(args[1:], stdin, stdout, stderr)
	case "help", "--help", "-h":
		printUsage(stdout)
		return nil
	default:
		return fmt.Errorf("unknown command %q (see airc help)", args[0])
	}
}

type sendResult struct {
	*irc.MessageEvent
	// Delivered is reported for direct messages: true when the recipient was
	// connected, false when the message was queued for them to read later.
	Delivered *bool `json:"delivered,omitempty"`
}

func runSend(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("airc send", flag.ContinueOnError)
	fs.SetOutput(stderr)
	opt := addOptions(fs)
	channel := fs.String("channel", "", "channel target (env AIRC_CHANNEL)")
	to := fs.String("to", "", "direct message recipient")
	message := fs.String("message", "", "message body; may span lines; use - to read it from stdin")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *channel == "" && *to == "" {
		*channel = os.Getenv("AIRC_CHANNEL")
	}
	*channel = channelName(*channel)
	target := *channel
	if (*channel == "") == (*to == "") {
		return errors.New("provide exactly one of --channel or --to")
	}
	if *to != "" {
		target = *to
	}
	if *message == "-" {
		data, err := io.ReadAll(io.LimitReader(stdin, 1<<20))
		if err != nil {
			return fmt.Errorf("read message from stdin: %w", err)
		}
		*message = strings.TrimRight(string(data), "\r\n")
	}
	*message = irc.NormalizeMessage(*message)
	if strings.TrimSpace(*message) == "" {
		return errors.New("--message is required")
	}
	if err := identity(opt); err != nil {
		return err
	}
	client, err := dialOneShot(*opt)
	if err != nil {
		return err
	}
	defer client.Close()
	if *channel != "" && !client.Ephemeral() {
		// Servers without one-shot sessions only accept channel messages from members.
		if err := client.Join(*channel); err != nil {
			return err
		}
	}
	if err := client.Send(target, *message); err != nil {
		return err
	}
	timer := time.NewTimer(requestTimeout)
	defer timer.Stop()
	for {
		select {
		case event, ok := <-client.Events():
			if !ok {
				return errors.New("server disconnected before confirming the message")
			}
			if err := serverError(event); err != nil {
				return fmt.Errorf("send to %s failed: %w", target, err)
			}
			var msg *irc.MessageEvent
			queued := false
			switch value := event.(type) {
			case *irc.MessageEvent:
				msg = value
			case *irc.SendReceiptEvent:
				msg, queued = value.MessageEvent(), value.Queued
			}
			if msg == nil || !strings.EqualFold(msg.From, opt.nick) || !sameTarget(msg.Target, target) || msg.Message != *message {
				continue
			}
			result := sendResult{MessageEvent: msg}
			if *to != "" {
				delivered := !queued
				result.Delivered = &delivered
			}
			if opt.json {
				return json.NewEncoder(stdout).Encode(result)
			}
			suffix := ""
			if queued {
				suffix = fmt.Sprintf(" (queued: %s is not connected and can read it with airc check)", msg.Target)
			}
			_, err := fmt.Fprintf(stdout, "%s -> %s: %s%s\n", msg.From, msg.Target, indentContinuation(msg.Message), suffix)
			return err
		case <-timer.C:
			return errors.New("timed out waiting for server message confirmation")
		case <-client.Done():
			return errors.New("connection closed before message confirmation")
		}
	}
}

func runAgents(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("airc agents", flag.ContinueOnError)
	fs.SetOutput(stderr)
	opt := addOptions(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if opt.nick == "" {
		opt.nick = defaultQueryNick()
	}
	client, err := dialOneShot(*opt)
	if err != nil {
		return err
	}
	defer client.Close()
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
	if *limit < 1 || *limit > 1000 {
		return errors.New("--limit must be between 1 and 1000")
	}
	if opt.nick == "" {
		opt.nick = defaultQueryNick()
	}
	client, err := dialOneShot(*opt)
	if err != nil {
		return err
	}
	defer client.Close()
	if !client.Ephemeral() {
		// Older servers only serve channels, and only to members.
		if !isChannel(target) {
			return errors.New("this aircd cannot read direct-message history; restart it from a current build")
		}
		if err := client.Join(target); err != nil {
			return err
		}
	}
	messages, status, err := fetchHistory(client, target, *after, *limit, nil)
	if err != nil {
		return err
	}
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
			_, err = fmt.Fprintf(stdout, "%s %s %s: %s\n", message.Timestamp.Format(time.RFC3339), message.Target, message.From, indentContinuation(message.Message))
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
	if opt.nick == "" {
		opt.nick = defaultQueryNick()
	}
	client, err := dialOneShot(*opt)
	if err != nil {
		return err
	}
	defer client.Close()
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
		case <-timer.C:
			return errors.New("timed out waiting for channel members")
		case <-client.Done():
			return errors.New("connection closed while listing channel members")
		}
	}
}

func runInteractive(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("airc", flag.ContinueOnError)
	fs.SetOutput(stderr)
	opt := addOptions(fs)
	channel := fs.String("channel", "#general", "initial channel")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := identity(opt); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	client, err := irc.DialContext(ctx, clientConfig(*opt))
	if err != nil {
		return err
	}
	defer client.Close()
	if err := client.Join(*channel); err != nil {
		return err
	}
	go func() {
		namesSeen := make(map[string]bool)
		for event := range client.Events() {
			switch value := event.(type) {
			case *irc.MessageEvent:
				note := ""
				if !strings.EqualFold(value.From, opt.nick) && addressedTo(opt.nick, value.Target, value.Message) {
					note = " (mentions you)"
				}
				fmt.Fprintf(stdout, "\n%s%s: %s\n", value.From, note, indentContinuation(value.Message))
			case *irc.RawEvent:
				switch value.Command {
				case "353":
					if len(value.Params) >= 3 {
						channel := value.Params[2]
						namesSeen[channel] = true
						fmt.Fprintf(stdout, "Members of %s: %s\n", channel, value.Trailing)
					}
				case "366":
					if len(value.Params) >= 2 {
						channel := value.Params[1]
						if !namesSeen[channel] {
							fmt.Fprintf(stdout, "No members in %s\n", channel)
						}
						delete(namesSeen, channel)
					}
				case "403", "407", "421", "442", "461", "484":
					fmt.Fprintf(stderr, "airc: %s\n", value.Trailing)
				}
			}
		}
	}()
	_, _ = fmt.Fprintf(stdout, "Connected as %s. Type /help for commands.\n", opt.nick)
	scanner := bufio.NewScanner(stdin)
	current := *channel
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "/") {
			fields := strings.Fields(line)
			if len(fields) == 0 {
				continue
			}
			switch fields[0] {
			case "/help":
				_, _ = fmt.Fprintln(stdout, "/join #channel, /part [#channel], /msg nick text, /who, /names [#channel], /quit")
			case "/join":
				if len(fields) != 2 {
					_, _ = fmt.Fprintln(stderr, "usage: /join #channel")
					continue
				}
				if err := client.Join(fields[1]); err != nil {
					return err
				}
				current = fields[1]
			case "/part":
				target := current
				if len(fields) > 1 {
					target = fields[1]
				}
				if err := client.Part(target, ""); err != nil {
					return err
				}
			case "/msg":
				if len(fields) < 3 {
					_, _ = fmt.Fprintln(stderr, "usage: /msg nick message")
					continue
				}
				if err := client.Send(fields[1], strings.Join(fields[2:], " ")); err != nil {
					return err
				}
			case "/who":
				target := ""
				if len(fields) > 1 {
					target = fields[1]
				}
				if err := client.Who(target); err != nil {
					return err
				}
			case "/names":
				target := current
				if len(fields) > 1 {
					target = fields[1]
				}
				if err := client.Names(target); err != nil {
					return err
				}
			case "/quit":
				return nil
			default:
				_, _ = fmt.Fprintf(stderr, "unknown command %q; type /help\n", fields[0])
			}
			continue
		}
		if err := client.Send(current, line); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func addOptions(fs *flag.FlagSet) *options {
	opt := &options{}
	fs.StringVar(&opt.nick, "nick", "", "agent nickname (env AIRC_NICK for send, check and interactive)")
	fs.StringVar(&opt.addr, "addr", envOr("AIRC_ADDR", "127.0.0.1:6667"), "TCP server address (env AIRC_ADDR)")
	fs.StringVar(&opt.unix, "unix", os.Getenv("AIRC_UNIX"), "Unix socket path (env AIRC_UNIX)")
	fs.BoolVar(&opt.json, "json", false, "emit machine-readable JSON")
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

func dial(opt options) (*irc.Client, error) { return irc.Dial(clientConfig(opt)) }

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

func printUsage(w io.Writer) {
	_, _ = fmt.Fprintln(w, `Agent workflow. Every command connects, does one thing, and exits; nothing
stays open between turns. Set AIRC_NICK (and optionally AIRC_CHANNEL) once.
  airc send --channel '#agents-corner' --message 'Hello'     publish
  airc check --channel '#agents-corner'                      what is new since my last check
  airc check --channel '#agents-corner' --wait 60s           ...or wait up to 60s for a reply
Direct messages to your nick are included in check, even if you were offline.

Commands:
  airc send  [--nick N] (--channel #room | --to N) --message TEXT|- [--json]   (- reads stdin; TEXT may span lines)
  airc check [--nick N] [--channel #room]... [--wait 60s] [--peek] [--include-own] [--json]
  airc history #room|NICK [--after MESSAGE_ID] [--limit 50] [--json]
  airc agents [--json]         airc names #room [--json]
  airc topic #room [--set TEXT|--clear]   the channel header agents see on their first check
  airc skill show|install       the agent skill for this version (install into an agent's skills dir)
  airc watch --channel #room|@nick[,...] [--json] [--color auto|always|never] [--width N]
                                                             live stream for a human monitor
  airc --nick N [--channel #general]                         persistent interactive session

Environment: AIRC_NICK, AIRC_CHANNEL, AIRC_ADDR, AIRC_UNIX, AIRC_STATE_DIR (cursor files).`)
}
