package backend

import (
	"context"
	"encoding/json"
	"time"
	"unicode/utf8"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

// Waiting reads active reply-coming signals for one retained room message.
// The context proof and signal snapshot share one exclusive native session.
func (b *Backend) Waiting(ctx context.Context, grant *store.ReadGrant) (core.WaitingResult, error) {
	a, err := grant.Consume(time.Now())
	if err != nil {
		return core.WaitingResult{}, err
	}
	if a.Request.Operation != core.WaitingRead || a.Request.Target == nil || a.Request.Target.Kind != "room" || !validHistoryID(a.Request.ParentID) {
		return core.WaitingResult{}, store.ErrInvalid
	}
	var result core.WaitingResult
	err = b.withRead(ctx, a, false, func(overall context.Context, client *irc.Client) error {
		features := client.Features()
		if features["CHAT"] != "1" || features["CONTEXT"] != "1" || features["CONTEXT_BYTES"] != "1" {
			return ErrReadUnsupported
		}
		request := core.Request{Operation: core.ContextRead, Target: a.Request.Target, ParentID: a.Request.ParentID}
		proof, err := boundedContextRequest(overall, client, a.Request.ParentID, 1, core.MaxContextBytes)
		if err != nil {
			return err
		}
		if _, err := validateContextProof(request, proof); err != nil {
			return err
		}
		signals, err := boundedWaitingRequest(overall, client, a.Request.Target.Name, a.Request.ParentID, core.MaxWaitingBytes)
		if err != nil {
			return err
		}
		candidate := core.WaitingResult{Target: *a.Request.Target, ParentID: a.Request.ParentID, Signals: signals}
		encoded, err := json.Marshal(candidate)
		if err != nil || len(encoded) > core.MaxWaitingBytes {
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
		return core.WaitingResult{}, err
	}
	return result, nil
}

func boundedWaitingRequest(ctx context.Context, client *irc.Client, target, parentID string, maxBytes int) ([]irc.ChatEntry, error) {
	if err := client.Chat(irc.ChatRequest{Action: "waiting", Target: target, ID: parentID}); err != nil {
		return nil, ErrReadUnavailable
	}
	deadline, hasDeadline := ctx.Deadline()
	signals := make([]irc.ChatEntry, 0, core.MaxWaitingSignals)
	seen := make(map[string]bool)
	wireBytes := 0
	for {
		select {
		case event, ok := <-client.Events():
			if !ok {
				return nil, ErrReadUnavailable
			}
			switch value := event.(type) {
			case *irc.ChatEvent:
				if value.Action != "prepare" || value.Target != target || value.ID != parentID || !validWaitingEntry(value.ChatEntry, target, parentID) {
					return nil, ErrReadUnavailable
				}
				key := value.AccountID
				if key == "" {
					actor := core.Target{Kind: "direct", Name: value.From}
					if actor.Normalize() != nil {
						return nil, ErrReadUnavailable
					}
					key = "guest:" + actor.Name
				} else {
					key = "account:" + key
				}
				if seen[key] {
					return nil, ErrReadUnavailable
				}
				if len(signals) >= core.MaxWaitingSignals {
					return nil, store.ErrCapacity
				}
				line, err := json.Marshal(value.ChatEntry)
				if err != nil {
					return nil, ErrReadUnavailable
				}
				wire := len(protocol.Format("server", "777", []string{client.Nick()}, string(line)))
				if wire > maxBytes || wireBytes > maxBytes-wire {
					return nil, store.ErrCapacity
				}
				wireBytes += wire
				seen[key] = true
				signals = append(signals, value.ChatEntry)
			case *irc.EndOfChatEvent:
				if ctx.Err() != nil || hasDeadline && !time.Now().Before(deadline) {
					return nil, ErrReadUnavailable
				}
				end := len(protocol.Format("server", "778", []string{client.Nick()}, "End of chat response"))
				if end > maxBytes || wireBytes > maxBytes-end {
					return nil, store.ErrCapacity
				}
				return signals, nil
			case *irc.RawEvent:
				if value.Command == "777" || value.Command == "778" || value.Command != "422" && len(value.Command) == 3 && value.Command[0] >= '4' && value.Command[0] <= '5' {
					return nil, ErrReadUnavailable
				}
			default:
				continue
			}
		case <-ctx.Done():
			return nil, ErrReadUnavailable
		}
	}
}

func validWaitingEntry(entry irc.ChatEntry, target, parentID string) bool {
	if entry.Action != "prepare" || entry.Target != target || entry.ID != parentID || !validNick(entry.From) || entry.AccountID != "" && !validHistoryID(entry.AccountID) || !utf8.ValidString(entry.Text) || !protocol.BriefText(entry.Text, 240) || entry.ExpiresAt.IsZero() || entry.Message != nil || entry.Profile != nil || entry.Context != nil || entry.SlowSeconds != 0 || entry.HistoryLimit != 0 || len(entry.Options) != 0 || len(entry.Votes) != 0 || entry.Closed {
		return false
	}
	return true
}
