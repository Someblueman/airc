package backend

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/store"
	"github.com/Someblueman/airc/pkg/irc"
)

const MaxDirectoryBytes = 64 << 10

type DirectorySnapshot struct {
	Version int             `json:"version"`
	Agents  []irc.AgentCard `json:"agents"`
}

// Directory returns the complete public card snapshot or no data. An explicit
// directory permission is global discovery, not a message-target permission.
func (b *Backend) Directory(ctx context.Context, grant *store.ReadGrant) (DirectorySnapshot, error) {
	a, err := grant.Consume(time.Now())
	if err != nil {
		return DirectorySnapshot{}, err
	}
	if a.Request.Operation != core.Directory {
		return DirectorySnapshot{}, store.ErrInvalid
	}
	var snapshot DirectorySnapshot
	err = b.withRead(ctx, a, false, func(ctx context.Context, client *irc.Client) error {
		if err := client.Directory(a.Request.Query); err != nil {
			return ErrReadUnavailable
		}
		agents := []irc.AgentCard{}
		seen := map[string]bool{}
		bytes := 0
		for {
			select {
			case event, ok := <-client.Events():
				if !ok {
					return ErrReadUnavailable
				}
				switch e := event.(type) {
				case *irc.DirectoryEvent:
					card := e.AgentCard
					target := core.Target{Kind: "direct", Name: card.Nick}
					if target.Normalize() != nil || seen[target.Name] || !validCard(card) || a.Request.Query != "" && target.Name != a.Request.Query {
						return ErrReadUnavailable
					}
					encoded, err := json.Marshal(card)
					if err != nil {
						return ErrReadUnavailable
					}
					bytes += len(encoded) + 1
					if bytes > MaxDirectoryBytes || len(agents) >= a.Request.Limit {
						return store.ErrCapacity
					}
					seen[target.Name] = true
					agents = append(agents, card)
				case *irc.EndOfDirectoryEvent:
					if ctx.Err() != nil || !time.Now().Before(a.Deadline) || a.Request.Query != "" && len(agents) != 1 {
						return ErrReadUnavailable
					}
					result := DirectorySnapshot{Version: 1, Agents: agents}
					encoded, err := json.Marshal(result)
					if err != nil || len(encoded)+1 > MaxDirectoryBytes {
						return store.ErrCapacity
					}
					snapshot = result
					return nil
				case *irc.RawEvent:
					// A malformed 773 falls back to RawEvent in the public SDK.
					if e.Command == "773" || rejectedRead(e) {
						return ErrReadUnavailable
					}
				}
			case <-ctx.Done():
				return ErrReadUnavailable
			}
		}
	})
	if err != nil {
		return DirectorySnapshot{}, err
	}
	return snapshot, nil
}

func validCard(card irc.AgentCard) bool {
	if card.AccountID != "" && !validHistoryID(card.AccountID) {
		return false
	}
	switch card.State {
	case "unknown", "available", "thinking", "running", "away":
	default:
		return false
	}
	for _, field := range []string{card.Model, card.Workspace, card.Tools, card.About} {
		if !cardText(field, 400) {
			return false
		}
	}
	return cardText(card.Note, 240)
}

func cardText(text string, limit int) bool {
	return len(text) <= limit && utf8.ValidString(text) && strings.IndexFunc(text, unicode.IsControl) < 0
}
