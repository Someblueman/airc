// Package bot runs explicitly addressed, live-only AIRC command bots.
package bot

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Someblueman/airc/pkg/irc"
)

// Command handlers run serially. They must honor ctx and return bounded text;
// the runner creates no handler goroutines or unbounded work queues.
type Command struct {
	Name   string
	Help   string
	Handle func(context.Context, *irc.MessageEvent, string) (string, error)
}

type Config struct {
	Client   irc.Config
	Channels []string
	Commands []Command
	// Interval is a global command rate limit; defaults to one command per second.
	Interval time.Duration
}

// Run reconnects using the same identity and joined channels. It never requests
// history: offline commands are not executed later. Cancellation closes the client.
func Run(ctx context.Context, cfg Config) error {
	if cfg.Client.IdentityToken == "" || cfg.Client.CreateAccount || cfg.Client.Ephemeral {
		return errors.New("bots require an existing persistent account; create it with airc user create")
	}
	if len(cfg.Channels) > 64 {
		return errors.New("at most 64 bot channels")
	}
	commands := map[string]Command{}
	for _, cmd := range cfg.Commands {
		if cmd.Name == "" || cmd.Name == "help" || cmd.Name != strings.ToLower(cmd.Name) || strings.ContainsAny(cmd.Name, " \t\r\n") || cmd.Handle == nil || len(cmd.Help) > 120 {
			return errors.New("invalid bot command")
		}
		if _, exists := commands[cmd.Name]; exists {
			return errors.New("duplicate bot command")
		}
		commands[cmd.Name] = cmd
	}
	if len(commands) > 16 {
		return errors.New("at most 16 bot commands")
	}
	help := []string{"help: list commands"}
	names := make([]string, 0, len(commands))
	for name := range commands {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		help = append(help, name+": "+commands[name].Help)
	}
	if cfg.Interval <= 0 {
		cfg.Interval = time.Second
	}
	cfg.Client.Reconnect = true
	c, err := irc.DialContext(ctx, cfg.Client)
	if err != nil {
		return err
	}
	defer c.Close()
	if !c.Supports("BOT_REPLIES") || c.Features()["HISTORY"] == "0" {
		return errors.New("bot requires BOT_REPLIES and retained history on the daemon")
	}
	for _, channel := range cfg.Channels {
		if err := c.Join(channel); err != nil {
			return err
		}
	}
	// A fixed ring prevents duplicate delivery from causing repeated commands.
	seen := map[string]bool{}
	ring := make([]string, 1024)
	next := 0
	var ready time.Time
	for {
		select {
		case <-ctx.Done():
			return nil
		case event, ok := <-c.Events():
			if !ok {
				return errors.New("bot connection closed")
			}
			if e, ok := event.(*irc.RawEvent); ok && len(e.Command) == 3 && e.Command[0] == '4' && e.Command != "422" {
				return fmt.Errorf("bot request rejected: %s", e.Trailing)
			}
			message, ok := event.(*irc.MessageEvent)
			if !ok {
				continue
			}
			body, addressed := commandText(c.Nick(), message)
			if !addressed || message.ID == "" || seen[message.ID] {
				continue
			}
			delete(seen, ring[next])
			ring[next] = message.ID
			seen[message.ID] = true
			next = (next + 1) % len(ring)
			now := time.Now()
			if now.Before(ready) {
				continue
			}
			ready = now.Add(cfg.Interval)
			name, args, _ := strings.Cut(body, " ")
			result := "Unknown command. Send help for the command list."
			if name == "help" || name == "" {
				result = strings.Join(help, "\n")
			} else if command, exists := commands[strings.ToLower(name)]; exists {
				callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
				result, err = command.Handle(callCtx, message, strings.TrimSpace(args))
				cancel()
				if err != nil {
					result = "Command failed: " + err.Error()
				}
			}
			if result == "" {
				continue
			}
			if len(result) > 4096 {
				result = "Command response exceeded 4096 bytes."
			}
			if err := c.BotReply(message.ID, result); err != nil {
				return err
			}
		}
	}
}

func commandText(nick string, m *irc.MessageEvent) (string, bool) {
	if m.Type != "message" || m.Kind != "" || m.Reaction != "" || strings.EqualFold(m.From, nick) {
		return "", false
	}
	text := strings.TrimSpace(m.Message)
	if strings.EqualFold(m.Target, nick) {
		return text, true
	}
	for _, prefix := range []string{nick + ":", "@" + nick} {
		if len(text) < len(prefix) || !strings.EqualFold(text[:len(prefix)], prefix) {
			continue
		}
		rest := text[len(prefix):]
		if rest == "" || strings.HasPrefix(rest, " ") || strings.HasPrefix(rest, "\t") {
			return strings.TrimSpace(rest), true
		}
	}
	return "", false
}
