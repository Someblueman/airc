package backend

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/pkg/irc"
)

const MaxHistoryBytes = 64 << 10

var (
	ErrReadUnavailable = errors.New(string(core.UpstreamUnavailable))
	ErrReadUnsupported = errors.New(string(core.UnsupportedOperation))
)

// HistoryPage preserves whole records and the upstream retention/cursor facts.
// Cursor is trusted internal data, not an external resumable permission token.
type HistoryPage struct {
	Messages []*irc.MessageMetadata `json:"messages"`
	Cursor   string                 `json:"cursor"`
	Status   string                 `json:"status"`
	Gap      bool                   `json:"gap"`
}

// Read consumes a journal-authorized read lease on the same bounded session
// pool as delivery. Read saturation preserves the lookup pool's send reserve.
// It never submits a message, writes a cursor file, retries or creates accounts.
func (b *Backend) Read(ctx context.Context, grant *store.ReadGrant) (HistoryPage, error) {
	a, err := grant.Consume(time.Now())
	if err != nil {
		return HistoryPage{}, err
	}
	entry, ok := b.bindings[a.Binding]
	if !ok || a.Realm != b.realm || b.principals[a.Principal] != a.Binding || entry.pinned != a.Backend {
		return HistoryPage{}, auth.ErrForbidden
	}
	if !b.acquire(a.Principal, store.LookupAttempt) {
		return HistoryPage{}, store.ErrCapacity
	}
	defer b.release(a.Principal, store.LookupAttempt)
	overall, cancel := context.WithDeadline(ctx, a.Deadline)
	defer cancel()
	dialCtx, stopDial := context.WithTimeout(overall, 10*time.Second)
	client, err := irc.DialContext(dialCtx, entry.client)
	stopDial()
	if err != nil {
		return HistoryPage{}, ErrReadUnavailable
	}
	closed := make(chan struct{})
	stopClose := context.AfterFunc(overall, func() { _ = client.Close(); close(closed) })
	defer func() {
		if !stopClose() {
			<-closed
		}
		_ = client.Close()
	}()
	feature := "CHECK"
	if a.Request.Operation == core.Search {
		feature = "SEARCH"
	}
	if !client.Ephemeral() || client.Features()[feature] != "1" {
		return HistoryPage{}, ErrReadUnsupported
	}
	r := a.Request
	if r.Operation == core.Search {
		return searchPage(overall, client, r, a.Deadline)
	}
	entries, err := boundedHistoryCheck(overall, client, r)
	if errors.Is(err, store.ErrCapacity) {
		return HistoryPage{}, store.ErrCapacity
	}
	if err != nil || overall.Err() != nil || !time.Now().Before(a.Deadline) {
		return HistoryPage{}, ErrReadUnavailable
	}
	return historyPage(entries, r)
}

// The public RequestCheck collector bounds count but not total bytes. This
// exclusive SDK event owner applies both bounds before retaining another entry.
func boundedHistoryCheck(ctx context.Context, client *irc.Client, r core.Request) ([]irc.CheckEntry, error) {
	request := irc.CheckRequest{Targets: []irc.CheckTarget{{Target: r.Target.Name, After: r.Cursor, Limit: r.Limit}}, IncludeOwn: true, MaxMessages: r.Limit}
	data, err := json.Marshal(request)
	if err != nil {
		return nil, ErrReadUnavailable
	}
	if err := client.Raw("CHECK :" + string(data)); err != nil {
		return nil, ErrReadUnavailable
	}
	var entries []irc.CheckEntry
	bytes := 0
	for {
		select {
		case event, ok := <-client.Events():
			if !ok {
				return nil, ErrReadUnavailable
			}
			raw, ok := event.(*irc.RawEvent)
			if !ok {
				continue
			}
			switch raw.Command {
			case "784":
				bytes += len(raw.Trailing)
				if bytes > MaxHistoryBytes || len(entries) >= r.Limit+1 {
					return nil, store.ErrCapacity
				}
				var entry irc.CheckEntry
				if err := json.Unmarshal([]byte(raw.Trailing), &entry); err != nil {
					return nil, ErrReadUnavailable
				}
				entries = append(entries, entry)
			case "785":
				return entries, nil
			default:
				if raw.Command != "422" && len(raw.Command) == 3 && raw.Command[0] >= '4' && raw.Command[0] <= '5' {
					return nil, ErrReadUnavailable
				}
			}
		case <-ctx.Done():
			return nil, ErrReadUnavailable
		}
	}
}

func validHistoryID(id string) bool {
	if len(id) != 32 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

func historyPage(entries []irc.CheckEntry, r core.Request) (HistoryPage, error) {
	page := HistoryPage{Messages: []*irc.MessageMetadata{}}
	marked := false
	seen := map[string]bool{}
	for _, entry := range entries {
		if entry.Target != r.Target.Name || entry.Warning != "" {
			return HistoryPage{}, auth.ErrForbidden
		}
		switch entry.Kind {
		case "message":
			m := entry.Message
			if marked || m == nil || m.Target != r.Target.Name || !validHistoryID(m.ID) || seen[m.ID] || len(entry.Targets) != 1 || entry.Targets[0] != 0 || len(page.Messages) >= r.Limit || !utf8.ValidString(m.Message) || strings.ContainsRune(m.Message, 0) || len(m.Message) > 4096 || m.Timestamp.IsZero() {
				return HistoryPage{}, ErrReadUnavailable
			}
			from := core.Target{Kind: "direct", Name: m.From}
			if err := from.Normalize(); err != nil {
				return HistoryPage{}, ErrReadUnavailable
			}
			if m.ReplyTo != "" && !validHistoryID(m.ReplyTo) || m.ThreadID != "" && !validHistoryID(m.ThreadID) {
				return HistoryPage{}, ErrReadUnavailable
			}
			seen[m.ID] = true
			page.Messages = append(page.Messages, m)
		case "page":
			if marked || entry.Message != nil || entry.Status != "ok" && entry.Status != "more" || entry.Cursor != "" && !validHistoryID(entry.Cursor) || entry.Status == "more" && len(page.Messages) == 0 {
				return HistoryPage{}, ErrReadUnavailable
			}
			if len(page.Messages) > 0 && (entry.Cursor == "" || entry.Status == "more" && entry.Cursor != page.Messages[len(page.Messages)-1].ID) {
				return HistoryPage{}, ErrReadUnavailable
			}
			marked = true
			page.Cursor, page.Status, page.Gap = entry.Cursor, entry.Status, entry.Gap
		default:
			return HistoryPage{}, ErrReadUnavailable
		}
	}
	if !marked {
		return HistoryPage{}, ErrReadUnavailable
	}
	data, err := json.Marshal(page)
	if err != nil || len(data) > MaxHistoryBytes {
		return HistoryPage{}, store.ErrCapacity
	}
	return page, nil
}
