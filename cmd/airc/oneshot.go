package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Someblueman/airc/pkg/irc"
)

const requestTimeout = 10 * time.Second

// dialOneShot connects for a single request. On current servers the session is
// ephemeral: it claims no nickname and leaves no join/quit traffic behind. On
// older servers the request is ignored and the session behaves as before, which
// callers detect with client.Ephemeral().
func dialOneShot(opt options) (*irc.Client, error) {
	cfg := clientConfig(opt)
	cfg.Ephemeral = true
	return irc.Dial(cfg)
}

// identity resolves the nickname an agent sends as: --nick, then $AIRC_NICK.
func identity(opt *options) error {
	if opt.nick == "" {
		opt.nick = os.Getenv("AIRC_NICK")
	}
	if opt.nick == "" {
		return errors.New("a nickname is required: pass --nick or set AIRC_NICK")
	}
	return nil
}

// serverError turns a server error numeric into an error. It returns nil for
// events that are not errors the caller should stop on.
func serverError(event irc.Event) error {
	raw, ok := event.(*irc.RawEvent)
	if !ok {
		return nil
	}
	switch raw.Command {
	case "401":
		return errors.New("no such nick: the recipient is not connected, and this server cannot queue direct messages for offline agents (that needs a current aircd started with --history)")
	case "403":
		return errors.New("no such channel (channel names start with # or & and contain no spaces)")
	case "404":
		return errors.New("cannot send to that channel; join it first")
	case "405", "407", "412", "417", "437", "461", "484":
		return fmt.Errorf("server rejected the request: %s", raw.Trailing)
	}
	return nil
}

// channelName lets a channel be given without its # prefix, which shells
// treat as the start of a comment when unquoted. Targets that already carry a
// prefix, including @nick, are returned unchanged.
func channelName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" || strings.ContainsAny(name[:1], "#&@") {
		return name
	}
	return "#" + name
}

// explain adds a hint to the errors people most often cause with shell quoting.
func explain(err error) string {
	message := err.Error()
	if strings.Contains(message, "flag needs an argument: -channel") {
		message += "\nhint: a bare # starts a comment in many shells, so '#room' must be quoted (or leave the # off: --channel room)"
	}
	return message
}

func isChannel(target string) bool {
	return strings.HasPrefix(target, "#") || strings.HasPrefix(target, "&")
}

// fetchHistory asks for messages for a channel or nickname and waits for the end
// marker. other, if set, sees every event that is not part of the reply so a
// caller that is also observing does not lose live notifications.
func fetchHistory(client *irc.Client, target, after string, limit int, other func(irc.Event)) ([]*irc.HistoryEvent, string, error) {
	if err := client.HistoryAfter(target, after, limit); err != nil {
		return nil, "", err
	}
	var messages []*irc.HistoryEvent
	timer := time.NewTimer(requestTimeout)
	defer timer.Stop()
	for {
		select {
		case event, ok := <-client.Events():
			if !ok {
				return nil, "", errors.New("server disconnected while reading history")
			}
			switch value := event.(type) {
			case *irc.HistoryEvent:
				if sameTarget(value.Target, target) {
					messages = append(messages, value)
					continue
				}
			case *irc.EndOfHistoryEvent:
				if sameTarget(value.Target, target) {
					return messages, value.Status, nil
				}
			case *irc.RawEvent:
				if value.Command == "461" || value.Command == "442" {
					return nil, "", fmt.Errorf("cannot read history of %s: %s", target, value.Trailing)
				}
			}
			if other != nil {
				other(event)
			}
		case <-timer.C:
			return nil, "", errors.New("timed out waiting for message history")
		case <-client.Done():
			return nil, "", errors.New("connection closed while reading history")
		}
	}
}
