package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"github.com/Someblueman/airc/pkg/irc"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

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
	cfg, err := dialConfig(*opt)
	if err != nil {
		return err
	}
	client, err := irc.DialContext(ctx, cfg)
	if err != nil {
		return err
	}
	defer client.Close()
	stopClose := context.AfterFunc(ctx, func() { _ = client.Close() })
	defer stopClose()
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
			case *irc.KickEvent:
				fmt.Fprintf(stdout, "%s kicked %s from %s: %s\n", value.By, value.Agent, value.Channel, value.Reason)
			case *irc.MonitorEvent:
				fmt.Fprintf(stdout, "monitor: %s online=%t\n", strings.Join(value.Nicks, ","), value.Online)
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
				case "305", "306", "301", "MODE", "783", "324":
					fmt.Fprintln(stdout, strings.Join(value.Params, " "), value.Trailing)
				case "401", "441", "472", "482", "734", "403", "407", "421", "442", "461", "484":
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
			case "/away":
				if err := client.Away(strings.TrimSpace(strings.TrimPrefix(line, "/away"))); err != nil {
					return err
				}
			case "/monitor":
				if len(fields) != 2 {
					fmt.Fprintln(stderr, "usage: /monitor nick,nick")
					continue
				}
				if err := client.Monitor(strings.Split(fields[1], ",")...); err != nil {
					return err
				}
			case "/mode", "/kick":
				if err := client.Raw(strings.TrimPrefix(line, "/")); err != nil {
					return err
				}
			case "/help":
				_, _ = fmt.Fprintln(stdout, "/join #channel, /part [#channel], /msg nick text, /who, /names [#channel], /away [reason], /monitor nick,nick, /mode #channel +/-o nick, /kick #channel nick :reason, /quit")
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
