package backend

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

// Poll reads durable native poll state. Its returned target proves ownership
// independently of the history ring, which may no longer contain the question.
func (b *Backend) Poll(ctx context.Context, grant *store.ReadGrant) (core.PollResult, error) {
	a, err := grant.Consume(time.Now())
	if err != nil {
		return core.PollResult{}, err
	}
	if a.Request.Operation != core.PollResults || a.Request.Target == nil || a.Request.Target.Kind != "room" || !protocol.ValidMessageID(a.Request.PollID) {
		return core.PollResult{}, store.ErrInvalid
	}
	var result core.PollResult
	err = b.withRead(ctx, a, false, func(overall context.Context, client *irc.Client) error {
		if client.Features()["CHAT"] != "1" {
			return ErrReadUnsupported
		}
		candidate, err := boundedPollRequest(overall, client, a.Request.Target.Name, a.Request.PollID, core.MaxPollBytes)
		if err != nil {
			return err
		}
		encoded, err := json.Marshal(candidate)
		if err != nil || len(encoded) > core.MaxPollBytes {
			return store.ErrCapacity
		}
		deadline, _ := overall.Deadline()
		if overall.Err() != nil || !time.Now().Before(deadline) {
			return ErrReadUnavailable
		}
		result = candidate
		return nil
	})
	if err != nil {
		return core.PollResult{}, err
	}
	return result, nil
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
				if seen || !validPollEntry(value.ChatEntry, target, id) {
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
			case *irc.RawEvent:
				if value.Command == "777" || value.Command == "778" || value.Command != "422" && len(value.Command) == 3 && value.Command[0] >= '4' && value.Command[0] <= '5' {
					return core.PollResult{}, ErrReadUnavailable
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
