package backend

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Someblueman/airc/internal/gateway/jsoninput"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

// LiveEvent is the bounded, non-message portion of an events.watch page.
// Target is always the exact room selected by the request. Live events do not
// carry a cursor or an occurrence timestamp: the native protocol supplies
// neither durable ordering nor replay for these records.
type LiveEvent struct {
	Kind   string         `json:"kind"`
	Target string         `json:"target"`
	Actor  string         `json:"actor,omitempty"`
	Member string         `json:"member,omitempty"`
	Reason string         `json:"reason,omitempty"`
	Topic  *string        `json:"topic,omitempty"`
	Mode   string         `json:"mode,omitempty"`
	Signal *irc.ChatEntry `json:"signal,omitempty"`
}

const maxLiveEventText = 400

// normalizeLiveEvent turns one SDK event into the gateway's strict live-event
// union. Native events are delivered only to an authorized room observer, so a
// QUIT has no channel on the wire and is attributed to that observer's one
// room. All other room-bearing events must name the selected room exactly.
func normalizeLiveEvent(event irc.Event, room, nick string) (*LiveEvent, error) {
	if !validRoom(room) || !validNick(nick) || event == nil {
		return nil, ErrReadUnavailable
	}
	var live *LiveEvent
	switch value := event.(type) {
	case *irc.JoinEvent:
		if value == nil || value.Type != "join" || value.Channel != room || !validNick(value.Agent) || value.Timestamp.IsZero() {
			return nil, ErrReadUnavailable
		}
		live = &LiveEvent{Kind: "join", Target: room, Member: value.Agent}
	case *irc.PartEvent:
		if value == nil || value.Type != "part" || value.Channel != room || !validNick(value.Agent) || !nativeReason(value.Reason) {
			return nil, ErrReadUnavailable
		}
		live = &LiveEvent{Kind: "part", Target: room, Member: value.Agent, Reason: value.Reason}
	case *irc.QuitEvent:
		if value == nil || value.Type != "quit" || !validNick(value.Agent) || !nativeReason(value.Reason) {
			return nil, ErrReadUnavailable
		}
		live = &LiveEvent{Kind: "quit", Target: room, Member: value.Agent, Reason: value.Reason}
	case *irc.KickEvent:
		if value == nil || value.Type != "kick" || value.Channel != room || !validNick(value.Agent) || !validNick(value.By) || !briefLiveText(value.Reason) {
			return nil, ErrReadUnavailable
		}
		live = &LiveEvent{Kind: "kick", Target: room, Actor: value.By, Member: value.Agent, Reason: value.Reason}
	case *irc.TopicEvent:
		// 331/332 query replies are not live mutations and have no setter.
		if value == nil || value.Type != "topic" || value.Channel != room || !validNick(value.SetBy) || !briefLiveText(value.Topic) {
			return nil, ErrReadUnavailable
		}
		topic := value.Topic
		live = &LiveEvent{Kind: "topic", Target: room, Actor: value.SetBy, Topic: &topic}
	case *irc.SignalEvent:
		// events.watch selects ObserveSignalsRaw. A typed signal has already
		// passed through encoding/json and therefore cannot prove duplicate,
		// unknown, case-aliased or null fields were absent on the wire.
		return nil, ErrReadUnavailable
	case *irc.RawEvent:
		return normalizeRawLiveEvent(value, room, nick)
	default:
		// Registration replies, acknowledgements, history pages and typed noise
		// are expected while a subscription is being established.
		return nil, nil
	}
	return boundLiveEvent(live)
}

func normalizeRawLiveEvent(value *irc.RawEvent, room, nick string) (*LiveEvent, error) {
	if value == nil {
		return nil, ErrReadUnavailable
	}
	if (value.Command == "MODE" || value.Command == "780") && liveRawWireBytes(value) > MaxHistoryBytes {
		return nil, store.ErrCapacity
	}
	switch value.Command {
	case "MODE":
		if value.Type != "raw" || len(value.Tags) != 0 || value.Trailing != "" || len(value.Params) != 3 || value.Params[0] != room || value.Params[1] != "+o" && value.Params[1] != "-o" || !validNick(value.Params[2]) {
			return nil, ErrReadUnavailable
		}
		actor := rawPrefixNick(value.Prefix)
		if actor == "" {
			return nil, ErrReadUnavailable
		}
		return boundLiveEvent(&LiveEvent{Kind: "mode", Target: room, Actor: actor, Member: value.Params[2], Mode: value.Params[1]})
	case "780":
		if value.Type != "raw" || value.Prefix == "" || strings.ContainsAny(value.Prefix, " \t\r\n\x00") || len(value.Tags) != 0 || len(value.Params) != 1 || value.Params[0] != nick {
			return nil, ErrReadUnavailable
		}
		entry, err := decodeLiveSignal(value.Trailing)
		if err != nil {
			return nil, ErrReadUnavailable
		}
		live := normalizeSignal(entry, room)
		if live == nil {
			return nil, ErrReadUnavailable
		}
		return boundLiveEvent(live)
	default:
		return nil, nil
	}
}

func rawPrefixNick(prefix string) string {
	if prefix == "" || strings.ContainsAny(prefix, " \t\r\n\x00") {
		return ""
	}
	name, rest, found := strings.Cut(prefix, "!")
	if !found || rest == "" || !validNick(name) {
		return ""
	}
	return name
}

type liveSignalWire struct {
	Action    *string   `json:"action"`
	Target    *string   `json:"target"`
	ID        *string   `json:"id,omitempty"`
	From      *string   `json:"from"`
	AccountID *string   `json:"account_id,omitempty"`
	Text      *string   `json:"text,omitempty"`
	ExpiresAt *string   `json:"expires_at,omitempty"`
	Options   *[]string `json:"options,omitempty"`
	Votes     *[]int    `json:"votes,omitempty"`
	Closed    *bool     `json:"closed,omitempty"`
}

func decodeLiveSignal(raw string) (irc.ChatEntry, error) {
	var wire liveSignalWire
	if err := jsoninput.Decode([]byte(raw), &wire, MaxHistoryBytes); err != nil || wire.Action == nil || wire.Target == nil || wire.From == nil {
		return irc.ChatEntry{}, errors.New("invalid live signal JSON")
	}
	var expires time.Time
	if wire.ExpiresAt != nil {
		var err error
		expires, err = time.Parse(time.RFC3339Nano, *wire.ExpiresAt)
		if err != nil || expires.IsZero() {
			return irc.ChatEntry{}, errors.New("invalid live signal expiry")
		}
	}
	if wire.ExpiresAt == nil || !validLiveSignalWireShape(wire) {
		return irc.ChatEntry{}, errors.New("invalid live signal shape")
	}
	entry := irc.ChatEntry{Action: *wire.Action, Target: *wire.Target, From: *wire.From, ExpiresAt: expires.UTC()}
	if wire.ID != nil {
		entry.ID = *wire.ID
	}
	if wire.AccountID != nil {
		entry.AccountID = *wire.AccountID
	}
	if wire.Text != nil {
		entry.Text = *wire.Text
	}
	if wire.Options != nil {
		entry.Options = append([]string(nil), (*wire.Options)...)
	}
	if wire.Votes != nil {
		entry.Votes = append([]int(nil), (*wire.Votes)...)
	}
	if wire.Closed != nil {
		entry.Closed = *wire.Closed
	}
	return entry, nil
}

func validLiveSignalWireShape(wire liveSignalWire) bool {
	if wire.Action == nil || wire.Target == nil || wire.From == nil || wire.ExpiresAt == nil {
		return false
	}
	switch *wire.Action {
	case "typing", "thinking":
		return wire.ID == nil && wire.Options == nil && wire.Votes == nil && wire.Closed == nil
	case "prepare":
		return wire.ID != nil && wire.Options == nil && wire.Votes == nil && wire.Closed == nil
	case "cancel":
		return wire.ID != nil && wire.AccountID == nil && wire.Text == nil && wire.Options == nil && wire.Votes == nil && wire.Closed == nil
	case "results":
		return wire.ID != nil && wire.Options != nil && wire.Votes != nil
	default:
		return false
	}
}

func normalizeSignal(entry irc.ChatEntry, room string) *LiveEvent {
	if entry.Target != room || !validNick(entry.From) || entry.AccountID != "" && !validHistoryID(entry.AccountID) || !protocol.BriefText(entry.Text, 1000) || entry.Message != nil || entry.Profile != nil || entry.Context != nil || entry.SlowSeconds != 0 || entry.HistoryLimit != 0 {
		return nil
	}
	if entry.ExpiresAt.IsZero() {
		return nil
	}
	if _, err := entry.ExpiresAt.MarshalJSON(); err != nil {
		return nil
	}
	switch entry.Action {
	case "typing", "thinking":
		if entry.ID != "" || !protocol.BriefText(entry.Text, 240) || len(entry.Options) != 0 || len(entry.Votes) != 0 || entry.Closed {
			return nil
		}
	case "prepare":
		if !validHistoryID(entry.ID) || !validWaitingEntry(entry, room, entry.ID) {
			return nil
		}
	case "cancel":
		if !validHistoryID(entry.ID) || entry.AccountID != "" || entry.Text != "" || len(entry.Options) != 0 || len(entry.Votes) != 0 || entry.Closed {
			return nil
		}
	case "results":
		if !validPollEntry(entry, room, entry.ID) {
			return nil
		}
	default:
		return nil
	}
	signal := entry
	return &LiveEvent{Kind: "signal", Target: room, Actor: entry.From, Signal: &signal}
}

func briefLiveText(text string) bool {
	return protocol.BriefText(text, maxLiveEventText)
}

func nativeReason(reason string) bool {
	return len(reason) <= protocol.MaxLineLength && utf8.ValidString(reason) && !strings.ContainsAny(reason, "\r\n\x00")
}

func boundLiveEvent(live *LiveEvent) (*LiveEvent, error) {
	if live == nil || live.Target == "" {
		return nil, ErrReadUnavailable
	}
	encoded, err := json.Marshal(live)
	if err != nil {
		return nil, ErrReadUnavailable
	}
	if len(encoded) > MaxHistoryBytes {
		return nil, store.ErrCapacity
	}
	return live, nil
}

// The SDK already bounds each line. Count the actual envelope instead of
// protocol.Format, which returns an empty string for an oversized line.
func liveRawWireBytes(raw *irc.RawEvent) int {
	bytes := len(raw.Prefix) + 2 + len(raw.Command) + 2
	for _, param := range raw.Params {
		bytes += 1 + len(param)
	}
	if raw.Trailing != "" {
		bytes += 2 + len(raw.Trailing)
	}
	return bytes
}
