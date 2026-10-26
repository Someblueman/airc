package backend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/store"
	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

var errPollScope = errors.New("poll response outside requested scope")

type pollNativeError struct{ event *irc.RawEvent }

func (e *pollNativeError) Error() string { return "native poll response failed" }
func (e *pollNativeError) Unwrap() error { return ErrReadUnavailable }

// Poll reads durable native poll state. Its returned target proves ownership
// independently of the history ring, which may no longer contain the question.
func (b *Backend) Poll(ctx context.Context, grant *store.ReadGrant) (core.PollResult, error) {
	valid := func(r core.Request) bool { return roomRead(core.PollResults)(r) && protocol.ValidMessageID(r.PollID) }
	return readSnapshot(ctx, b, grant, valid, core.MaxPollBytes, func(ctx context.Context, client *irc.Client, r core.Request) (core.PollResult, error) {
		if client.Features()["CHAT"] != "1" {
			return core.PollResult{}, ErrReadUnsupported
		}
		return boundedPollRequest(ctx, client, r.Target.Name, r.PollID, core.MaxPollBytes)
	})
}

func boundedPollRequest(ctx context.Context, client *irc.Client, target, id string, maxBytes int) (core.PollResult, error) {
	if err := client.Chat(irc.ChatRequest{Action: "results", ID: id}); err != nil {
		return core.PollResult{}, ErrReadUnavailable
	}
	deadline, hasDeadline := ctx.Deadline()
	var result core.PollResult
	seen := false
	wireBytes := 0
	for {
		select {
		case event, ok := <-client.Events():
			if !ok {
				return core.PollResult{}, ErrReadUnavailable
			}
			switch value := event.(type) {
			case *irc.ChatEvent:
				if seen {
					return core.PollResult{}, ErrReadUnavailable
				}
				if value.Target != target || value.ID != id {
					return core.PollResult{}, fmt.Errorf("%w: %w", ErrReadUnavailable, errPollScope)
				}
				if !validPollEntry(value.ChatEntry, target, id) {
					return core.PollResult{}, ErrReadUnavailable
				}
				line, err := json.Marshal(value.ChatEntry)
				if err != nil {
					return core.PollResult{}, ErrReadUnavailable
				}
				wire := len(protocol.Format("server", "777", []string{client.Nick()}, string(line)))
				if wire > maxBytes || wireBytes > maxBytes-wire {
					return core.PollResult{}, store.ErrCapacity
				}
				wireBytes += wire
				result = core.PollResult{Target: core.Target{Kind: "room", Name: target}, ID: value.ID, From: value.From, AccountID: value.AccountID, Question: value.Text, Options: value.Options, Votes: value.Votes, ExpiresAt: value.ExpiresAt, Closed: value.Closed}
				seen = true
			case *irc.EndOfChatEvent:
				if !seen || ctx.Err() != nil || hasDeadline && !time.Now().Before(deadline) {
					return core.PollResult{}, ErrReadUnavailable
				}
				end := len(protocol.Format("server", "778", []string{client.Nick()}, "End of chat response"))
				if end > maxBytes || wireBytes > maxBytes-end {
					return core.PollResult{}, store.ErrCapacity
				}
				return result, nil
			case *irc.SendReceiptEvent:
				return core.PollResult{}, ErrReadUnavailable
			case *irc.RawEvent:
				if value.Command == "777" || value.Command == "778" || value.Command == "762" {
					return core.PollResult{}, ErrReadUnavailable
				}
				if value.Command != "422" && len(value.Command) == 3 && value.Command[0] >= '4' && value.Command[0] <= '5' {
					return core.PollResult{}, &pollNativeError{event: value}
				}
			}
		case <-ctx.Done():
			return core.PollResult{}, ErrReadUnavailable
		}
	}
}

func validPollEntry(entry irc.ChatEntry, target, id string) bool {
	if entry.Action != "results" || entry.Target != target || entry.ID != id || !protocol.ValidMessageID(entry.ID) || !validNick(entry.From) || entry.AccountID != "" && !validHistoryID(entry.AccountID) || !protocol.BriefText(entry.Text, 1000) || strings.TrimSpace(entry.Text) == "" || entry.ExpiresAt.IsZero() || entry.Message != nil || entry.Profile != nil || entry.Context != nil || entry.SlowSeconds != 0 || entry.HistoryLimit != 0 || len(entry.Options) < 2 || len(entry.Options) > 8 || len(entry.Votes) != len(entry.Options) {
		return false
	}
	seen := make(map[string]bool, len(entry.Options))
	total := 0
	for i, option := range entry.Options {
		if option == "" || !protocol.BriefText(option, 80) || seen[option] || entry.Votes[i] < 0 || entry.Votes[i] > 256-total {
			return false
		}
		seen[option] = true
		total += entry.Votes[i]
	}
	return true
}
