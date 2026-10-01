package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/Someblueman/airc/pkg/irc"
)

const requestTimeout = 10 * time.Second

// dialOneShot connects for a single request. On current servers the session is
// ephemeral: it claims no nickname and leaves no join/quit traffic behind. On
// older servers the request is ignored and the session behaves as before, which
// callers detect with client.Ephemeral().
func dialOneShot(ctx context.Context, opt options) (*irc.Client, error) {
	cfg := clientConfig(opt)
	cfg.Ephemeral = true
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	client, err := irc.DialContext(ctx, cfg)
	if err != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return client, err
}

// commandContext bounds ordinary one-shot commands and cancels them on signals.
func commandContext() (context.Context, context.CancelFunc) {
	signals, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	ctx, cancel := context.WithTimeout(signals, requestTimeout)
	return ctx, func() { cancel(); stop() }
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
	case "430", "442", "421", "451", "462", "405", "407", "412", "417", "437", "461", "484", "464", "465", "474", "481", "485":
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
	if errors.Is(err, syscall.EMFILE) {
		message += "\nhint: this process exhausted its file descriptors; finish/cancel unused tool sessions and inspect the agent/launcher with airc doctor --pid PID. Configure its descriptor limit before launch; changing a child shell cannot fix the running parent."
	} else if errors.Is(err, syscall.ENFILE) {
		message += "\nhint: the system file table is full; inspect process descriptor counts before retrying."
	}
	if strings.Contains(message, "flag needs an argument: -channel") {
		message += "\nhint: a bare # starts a comment in many shells, so '#room' must be quoted (or leave the # off: --channel room)"
	}
	return message
}

// addressedTo reports whether a message needs nick's attention: it is a direct
// message to nick, or a channel message that tags or addresses it.
func addressedTo(nick, target, body string) bool {
	if !isChannel(target) {
		return strings.EqualFold(target, nick)
	}
	return slices.Contains(irc.Mentions(body), strings.ToLower(nick))
}

func isChannel(target string) bool {
	return strings.HasPrefix(target, "#") || strings.HasPrefix(target, "&")
}

type historyPage struct {
	messages []*irc.HistoryEvent
	status   string
	cursor   string
}

// fetchHistory reads one reply, preserving unrelated live events for observers.
func fetchHistory(ctx context.Context, client *irc.Client, target, after string, limit int, other func(irc.Event)) (historyPage, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return historyPage{}, err
	}
	if err := client.HistoryAfter(target, after, limit); err != nil {
		if ctx.Err() != nil {
			return historyPage{}, ctx.Err()
		}
		return historyPage{}, err
	}
	return awaitHistory(ctx, client, target, other)
}

func awaitHistory(ctx context.Context, client *irc.Client, target string, other func(irc.Event)) (historyPage, error) {
	var page historyPage
	for {
		select {
		case event, ok := <-client.Events():
			if !ok {
				if ctx.Err() != nil {
					return historyPage{}, ctx.Err()
				}
				return historyPage{}, errors.New("server disconnected while reading history")
			}
			switch value := event.(type) {
			case *irc.HistoryEvent:
				page.messages = append(page.messages, value)
				continue
			case *irc.EndOfHistoryEvent:
				if sameTarget(value.Target, target) {
					page.status, page.cursor = value.Status, value.Cursor
					return page, nil
				}
			}
			if err := serverError(event); err != nil {
				return historyPage{}, err
			}
			if other != nil {
				other(event)
			}
		case <-ctx.Done():
			return historyPage{}, ctx.Err()
		case <-client.Done():
			if ctx.Err() != nil {
				return historyPage{}, ctx.Err()
			}
			return historyPage{}, errors.New("connection closed while reading history")
		}
	}
}
