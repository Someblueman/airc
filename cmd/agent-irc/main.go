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
	case "interactive":
		return runInteractive(args[1:], stdin, stdout, stderr)
	case "help", "--help", "-h":
		printUsage(stdout)
		return nil
	default:
		return fmt.Errorf("unknown command %q (see agent-irc help)", args[0])
	}
}

func runSend(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("agent-irc send", flag.ContinueOnError)
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

func runWatch(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("agent-irc watch", flag.ContinueOnError)
	fs.SetOutput(stderr)
	opt := addOptions(fs)
	channel := fs.String("channel", "", "channel to watch")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *channel == "" {
		return errors.New("--channel is required")
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
	encoder := json.NewEncoder(stdout)
	for {
		select {
		case event, ok := <-client.Events():
			if !ok {
				return nil
			}
			if !isWatchEvent(event) {
				continue
			}
			if opt.json {
				if err := encoder.Encode(event); err != nil {
					return err
				}
			} else if message, ok := event.(*irc.MessageEvent); ok {
				if _, err := fmt.Fprintf(stdout, "%s: %s\n", message.From, message.Message); err != nil {
					return err
				}
			}
		case <-ctx.Done():
			return nil
		}
	}
}

func runAgents(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("agent-irc agents", flag.ContinueOnError)
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
		return errors.New("usage: agent-irc history CHANNEL [--nick observer] [--limit 50] [--json]")
	}
	channel := args[0]
	fs := flag.NewFlagSet("agent-irc history", flag.ContinueOnError)
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

func runInteractive(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("agent-irc", flag.ContinueOnError)
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
		for event := range client.Events() {
			if message, ok := event.(*irc.MessageEvent); ok {
				fmt.Fprintf(stdout, "\n%s: %s\n", message.From, message.Message)
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
				_, _ = fmt.Fprintln(stdout, "/join #channel, /part [#channel], /msg nick text, /who, /names, /quit")
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

func isWatchEvent(event irc.Event) bool {
	switch event.(type) {
	case *irc.MessageEvent, *irc.JoinEvent, *irc.PartEvent, *irc.QuitEvent, *irc.ConnectionEvent:
		return true
	default:
		return false
	}
}

func printUsage(w io.Writer) {
	_, _ = fmt.Fprintln(w, `agent-irc send --nick N (--channel #room | --to N) --message TEXT [--json]
agent-irc watch --nick N --channel #room [--json]
agent-irc agents [--nick observer] [--json]
agent-irc history #room [--nick observer] [--limit 50] [--json]
agent-irc --nick N [--channel #general]  # interactive mode`)
}
