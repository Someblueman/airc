package main

import (
	"bufio"
	"context"
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
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return runInteractive(args, stdin, stdout, stderr)
	}
	switch args[0] {
	case "send":
		return runSend(args[1:], stdout, stderr)
	case "watch":
		return runWatch(args[1:], stdout, stderr)
	case "agents":
		return runAgents(args[1:], stdout, stderr)
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

func runSend(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("airc send", flag.ContinueOnError)
	fs.SetOutput(stderr)
	opt := addOptions(fs)
	channel := fs.String("channel", "", "channel target")
	to := fs.String("to", "", "direct message recipient")
	message := fs.String("message", "", "message body")
	if err := fs.Parse(args); err != nil {
		return err
	}
	target := *channel
	if (*channel == "") == (*to == "") {
		return errors.New("provide exactly one of --channel or --to")
	}
	if *to != "" {
		target = *to
	}
	if *message == "" {
		return errors.New("--message is required")
	}
	client, err := dial(*opt)
	if err != nil {
		return err
	}
	defer client.Close()
	if *channel != "" {
		if err := client.Join(*channel); err != nil {
			return err
		}
	}
	if err := client.Send(target, *message); err != nil {
		return err
	}
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event, ok := <-client.Events():
			if !ok {
				return errors.New("server disconnected before confirming the message")
			}
			var msg *irc.MessageEvent
			switch value := event.(type) {
			case *irc.MessageEvent:
				msg = value
			case *irc.SendReceiptEvent:
				msg = value.MessageEvent()
			}
			if msg == nil || !strings.EqualFold(msg.From, opt.nick) || !sameTarget(msg.Target, target) || msg.Message != *message {
				continue
			}
			if opt.json {
				return json.NewEncoder(stdout).Encode(msg)
			}
			_, err := fmt.Fprintf(stdout, "%s -> %s: %s\n", msg.From, msg.Target, msg.Message)
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
		opt.nick = "agent-observer"
	}
	client, err := dial(*opt)
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
		return errors.New("usage: airc history CHANNEL [--nick observer] [--limit 50] [--json]")
	}
	channel := args[0]
	fs := flag.NewFlagSet("airc history", flag.ContinueOnError)
	fs.SetOutput(stderr)
	opt := addOptions(fs)
	limit := fs.Int("limit", 50, "maximum messages to return")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *limit < 1 || *limit > 1000 {
		return errors.New("--limit must be between 1 and 1000")
	}
	if opt.nick == "" {
		opt.nick = "agent-observer"
	}
	client, err := dial(*opt)
	if err != nil {
		return err
	}
	defer client.Close()
	if err := client.Join(channel); err != nil {
		return err
	}
	if err := client.History(channel, *limit); err != nil {
		return err
	}
	messages := make([]*irc.HistoryEvent, 0)
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event, ok := <-client.Events():
			if !ok {
				return errors.New("server disconnected while reading history")
			}
			switch value := event.(type) {
			case *irc.HistoryEvent:
				if value.Target == channel {
					messages = append(messages, value)
				}
			case *irc.EndOfHistoryEvent:
				if value.Target != channel {
					continue
				}
				if opt.json {
					for _, message := range messages {
						if err := json.NewEncoder(stdout).Encode(message); err != nil {
							return err
						}
					}
				} else {
					for _, message := range messages {
						if _, err := fmt.Fprintf(stdout, "%s %s %s: %s\n", message.Timestamp.Format(time.RFC3339), message.Target, message.From, message.Message); err != nil {
							return err
						}
					}
				}
				return nil
			}
		case <-timer.C:
			return errors.New("timed out waiting for message history")
		case <-client.Done():
			return errors.New("connection closed while reading history")
		}
	}
}

func runNames(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return errors.New("usage: airc names #channel [--nick observer] [--json]")
	}
	channel := args[0]
	fs := flag.NewFlagSet("airc names", flag.ContinueOnError)
	fs.SetOutput(stderr)
	opt := addOptions(fs)
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: airc names #channel [--nick observer] [--json]")
	}
	if opt.nick == "" {
		opt.nick = "agent-observer"
	}
	client, err := dial(*opt)
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
	if opt.nick == "" {
		return errors.New("--nick is required for interactive mode")
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
				fmt.Fprintf(stdout, "\n%s: %s\n", value.From, value.Message)
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
	fs.StringVar(&opt.nick, "nick", "", "agent nickname")
	fs.StringVar(&opt.addr, "addr", "127.0.0.1:6667", "TCP server address")
	fs.StringVar(&opt.unix, "unix", "", "Unix socket path")
	fs.BoolVar(&opt.json, "json", false, "emit machine-readable JSON")
	return opt
}

func dial(opt options) (*irc.Client, error) { return irc.Dial(clientConfig(opt)) }

func clientConfig(opt options) irc.Config {
	network, address := "tcp", opt.addr
	if opt.unix != "" {
		network, address = "unix", opt.unix
	}
	return irc.Config{Nick: opt.nick, Addr: address, Network: network}
}

func sameTarget(a, b string) bool {
	if strings.HasPrefix(a, "#") || strings.HasPrefix(a, "&") {
		return a == b
	}
	return strings.EqualFold(a, b)
}

func printUsage(w io.Writer) {
	_, _ = fmt.Fprintln(w, `airc send --nick N (--channel #room | --to N) --message TEXT [--json]
airc watch --nick N --channel #room [--json]
airc agents [--nick observer] [--json]
airc names #room [--nick observer] [--json]
airc history #room [--nick observer] [--limit 50] [--json]
airc --nick N [--channel #general]  # interactive mode`)
}
