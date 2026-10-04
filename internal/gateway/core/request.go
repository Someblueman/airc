// Package core defines the gateway's normalized operation contract. Parsing
// does not authenticate, authorize, admit, or advertise an implementation.
package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

const MaxEnvelopeBytes = 16 << 10

type Operation string

const (
	Send           Operation = "send"
	Reply          Operation = "reply"
	ReactionAdd    Operation = "reaction.add"
	ContextRead    Operation = "context.read"
	PinsRead       Operation = "pins.read"
	RoomRead       Operation = "room.read"
	WaitingRead    Operation = "waiting.read"
	PollResults    Operation = "poll.results"
	TopicRead      Operation = "topic.read"
	MembersRead    Operation = "members.read"
	ChannelsRead   Operation = "channels.read"
	ReceiptGet     Operation = "receipt.get"
	HistoryRead    Operation = "history.read"
	Watch          Operation = "watch"
	EventsWatch    Operation = "events.watch"
	Search         Operation = "search"
	Directory      Operation = "directory"
	ProfileUpdate  Operation = "profile.update"
	PresenceSet    Operation = "presence.set"
	TopicSet       Operation = "topic.set"
	RoomUpdate     Operation = "room.update"
	PinsAdd        Operation = "pins.add"
	PinsRemove     Operation = "pins.remove"
	ActivitySet    Operation = "activity.set"
	MessageAction  Operation = "message.action"
	MessageCorrect Operation = "message.correct"
	MessageRetract Operation = "message.retract"
	// CapabilitiesGet is authenticated discovery, not a configurable AIRC permission.
	CapabilitiesGet Operation = "capabilities.get"
)

// IsWatch identifies reads that subscribe under bounded watch leases.
func (op Operation) IsWatch() bool { return op == Watch || op == EventsWatch }

type Policy string

const (
	Retry       Policy = "retry"
	ReceiptOnly Policy = "receipt_only"
)

type Target struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// Request contains caller intent only. Identity, backend address, credentials,
// and return routes are deliberately absent. Times are integer Unix seconds;
// pointers distinguish omitted defaults from explicitly authenticated values.
type Request struct {
	Version        int               `json:"version"`
	RequestID      string            `json:"request_id"`
	Operation      Operation         `json:"operation"`
	Target         *Target           `json:"target,omitempty"`
	Message        string            `json:"message,omitempty"`
	Reaction       string            `json:"reaction,omitempty"`
	ParentID       string            `json:"parent_id,omitempty"`
	PollID         string            `json:"poll_id,omitempty"`
	ThreadMode     string            `json:"thread_mode,omitempty"`
	LookupID       string            `json:"lookup_id,omitempty"`
	Cursor         string            `json:"cursor,omitempty"`
	Start          string            `json:"start,omitempty"`
	Query          string            `json:"query,omitempty"`
	From           string            `json:"from,omitempty"`
	Limit          int               `json:"limit,omitempty"`
	MaxBytes       int               `json:"max_bytes,omitempty"`
	IssuedAt       *int64            `json:"issued_at,omitempty"`
	ExpiresAt      *int64            `json:"expires_at,omitempty"`
	DeliveryPolicy *Policy           `json:"delivery_policy,omitempty"`
	Profile        *ProfilePatch     `json:"profile,omitempty"`
	Presence       *PresenceInput    `json:"presence,omitempty"`
	Topic          *string           `json:"topic,omitempty"`
	Room           *RoomPatch        `json:"room,omitempty"`
	Activity       *ActivityInput    `json:"activity,omitempty"`
	ReplySignal    *ReplySignalInput `json:"reply_signal,omitempty"`
	Poll           *PollInput        `json:"poll,omitempty"`
	Choice         int               `json:"choice,omitempty"`
}

func (r *Request) Normalize() error {
	if r.Version != 1 || !validID(r.RequestID, 64) {
		return errors.New("invalid version or request ID")
	}
	if r.Target != nil {
		if err := r.Target.Normalize(); err != nil {
			return err
		}
	}
	r.Message = irc.NormalizeMessage(r.Message)
	if !utf8.ValidString(r.Message) || strings.ContainsRune(r.Message, 0) || len(r.Message) > 4096 {
		return errors.New("invalid message")
	}
	if r.Start != "" && (r.Operation != HistoryRead && !r.Operation.IsWatch() || r.Start != "recent" && r.Start != "retained" || r.Cursor != "") {
		return errors.New("invalid initial history selector")
	}
	mutation := r.Operation == Send || r.Operation == Reply || r.Operation == ReactionAdd || r.Operation == ProfileUpdate || r.Operation == PresenceSet || r.Operation == TopicSet || r.Operation == RoomUpdate || r.Operation == PinsAdd || r.Operation == PinsRemove || r.Operation == ActivitySet || r.Operation == ReplySignalSet || r.Operation == ReplySignalCancel || r.Operation == PollCreate || r.Operation == PollVote || r.Operation == PollClose || r.Operation == MessageAction || r.Operation == MessageCorrect || r.Operation == MessageRetract
	if r.Operation == PollCreate && r.Poll != nil {
		poll := *r.Poll
		poll.Options = append([]string(nil), r.Poll.Options...)
		r.Poll = &poll
	}
	if r.Operation == ReplySignalSet && r.ReplySignal != nil {
		input := *r.ReplySignal
		r.ReplySignal = &input
	}
	if r.Operation == PresenceSet && r.Presence != nil {
		presence := *r.Presence
		presence.Note = strings.TrimSpace(presence.Note)
		r.Presence = &presence
	}
	if r.Operation == TopicSet && r.Topic != nil {
		if !protocol.BriefText(*r.Topic, 400) {
			return errors.New("invalid topic")
		}
		topic := strings.TrimSpace(*r.Topic)
		r.Topic = &topic
	}
	if r.PollID != "" && r.Operation != PollResults && r.Operation != PollVote && r.Operation != PollClose {
		return errors.New("poll selector on non-poll operation")
	}
	if mutation {
		switch r.Operation {
		case Send:
			if r.Target == nil || strings.TrimSpace(r.Message) == "" || r.ParentID != "" || r.Reaction != "" {
				return errors.New("send requires target and message")
			}
		case Reply:
			if r.Target == nil || strings.TrimSpace(r.Message) == "" || !messageID(r.ParentID) || r.Reaction != "" {
				return errors.New("reply requires target, message, and parent")
			}
		case ReactionAdd:
			if r.Target == nil || r.Target.Kind != "room" || r.Message != "" || !messageID(r.ParentID) || !protocol.ValidReaction(r.Reaction) {
				return errors.New("reaction requires room target, parent, and valid reaction")
			}
		case ProfileUpdate:
			if r.Target != nil || r.Message != "" || r.Reaction != "" || r.ParentID != "" || !r.Profile.valid() || r.Presence != nil {
				return errors.New("profile update requires a self profile patch")
			}
		case PresenceSet:
			if r.Target != nil || r.Message != "" || r.Reaction != "" || r.ParentID != "" || !r.Presence.valid() || r.Profile != nil {
				return errors.New("presence set requires self presence input")
			}
		case TopicSet:
			if r.Target == nil || r.Target.Kind != "room" || r.Topic == nil || r.Message != "" || r.Reaction != "" || r.ParentID != "" || !cardText(*r.Topic, 400) {
				return errors.New("topic set requires room target and valid topic")
			}
		case RoomUpdate:
			if r.Target == nil || r.Target.Kind != "room" || r.Message != "" || r.Reaction != "" || r.ParentID != "" || !r.Room.valid() {
				return errors.New("room update requires room target and settings")
			}
		case PinsAdd, PinsRemove:
			if r.Target == nil || r.Target.Kind != "room" || !messageID(r.ParentID) || r.Message != "" || r.Reaction != "" {
				return errors.New("pin mutation requires room target and parent")
			}
		case ActivitySet:
			if r.Target == nil || r.Target.Kind != "room" && r.Target.Kind != "direct" || r.Message != "" || r.Reaction != "" || r.ParentID != "" || !r.Activity.valid() {
				return errors.New("activity set requires target and valid activity")
			}
		case ReplySignalSet:
			if r.Target == nil || r.Target.Kind != "room" && r.Target.Kind != "direct" || !messageID(r.ParentID) || r.Message != "" || r.Reaction != "" || !r.ReplySignal.valid() {
				return errors.New("reply signal set requires target, parent, and valid input")
			}
		case ReplySignalCancel:
			if r.Target == nil || r.Target.Kind != "room" && r.Target.Kind != "direct" || !messageID(r.ParentID) || r.Message != "" || r.Reaction != "" || r.ReplySignal != nil {
				return errors.New("reply signal cancel requires target and parent")
			}
		case PollCreate:
			if r.Target == nil || r.Target.Kind != "room" || !r.Poll.valid() || r.PollID != "" || r.Choice != 0 || r.Message != "" || r.Reaction != "" || r.ParentID != "" {
				return errors.New("poll create requires room target and valid poll")
			}
		case PollVote:
			if r.Target == nil || r.Target.Kind != "room" || !protocol.ValidMessageID(r.PollID) || r.Poll != nil || r.Choice < 1 || r.Choice > 8 || r.Message != "" || r.Reaction != "" || r.ParentID != "" {
				return errors.New("poll vote requires room target, poll, and choice")
			}
		case PollClose:
			if r.Target == nil || r.Target.Kind != "room" || !protocol.ValidMessageID(r.PollID) || r.Poll != nil || r.Choice != 0 || r.Message != "" || r.Reaction != "" || r.ParentID != "" {
				return errors.New("poll close requires room target and poll")
			}
		case MessageAction:
			if r.Target == nil || r.Target.Kind != "room" && r.Target.Kind != "direct" || strings.TrimSpace(r.Message) == "" || r.ParentID != "" || r.Reaction != "" {
				return errors.New("message action requires target and message")
			}
		case MessageCorrect:
			if r.Target == nil || r.Target.Kind != "room" && r.Target.Kind != "direct" || !protocol.ValidMessageID(r.ParentID) || strings.TrimSpace(r.Message) == "" || r.Reaction != "" {
				return errors.New("message correction requires target, parent, and message")
			}
		case MessageRetract:
			if r.Target == nil || r.Target.Kind != "room" && r.Target.Kind != "direct" || !protocol.ValidMessageID(r.ParentID) || r.Reaction != "" || r.Message != "" && strings.TrimSpace(r.Message) == "" {
				return errors.New("message retraction requires target and parent")
			}
		}
		if r.Cursor != "" || r.Query != "" || r.From != "" || r.Limit != 0 || r.MaxBytes != 0 || r.LookupID != "" || r.ThreadMode != "" || r.Profile != nil && r.Operation != ProfileUpdate || r.Presence != nil && r.Operation != PresenceSet || r.Topic != nil && r.Operation != TopicSet || r.Room != nil && r.Operation != RoomUpdate || r.Activity != nil && r.Operation != ActivitySet || r.ReplySignal != nil && r.Operation != ReplySignalSet || r.Poll != nil && r.Operation != PollCreate || r.Choice != 0 && r.Operation != PollVote {
			return errors.New("read arguments on mutation")
		}
	} else {
		if r.From != "" && r.Operation != Search {
			return errors.New("sender filter on non-search operation")
		}
		if r.Message != "" || r.Reaction != "" || r.Profile != nil || r.Presence != nil || r.Topic != nil || r.Room != nil || r.Activity != nil || r.ReplySignal != nil || r.Poll != nil || r.Choice != 0 || r.ParentID != "" && r.Operation != ContextRead && r.Operation != HistoryRead && r.Operation != Search && !r.Operation.IsWatch() && r.Operation != WaitingRead || r.ThreadMode != "" && r.Operation != HistoryRead && r.Operation != Search && !r.Operation.IsWatch() || r.DeliveryPolicy != nil || r.IssuedAt != nil || r.ExpiresAt != nil {
			return errors.New("mutation arguments on read")
		}
		switch r.Operation {
		case CapabilitiesGet, ChannelsRead:
			if r.LookupID != "" || r.Target != nil || r.Cursor != "" || r.Query != "" || r.Limit != 0 || r.MaxBytes != 0 {
				return errors.New("invalid capability query")
			}
		case ReceiptGet:
			if !validID(r.LookupID, 64) || r.Target != nil || r.Cursor != "" || r.Query != "" || r.Limit != 0 || r.MaxBytes != 0 {
				return errors.New("invalid receipt query")
			}
		case ContextRead:
			if r.ThreadMode != "" || r.Target == nil || r.Target.Kind != "room" || !messageID(r.ParentID) || r.Cursor != "" || r.Query != "" || r.From != "" || r.LookupID != "" || r.Limit < 0 || r.Limit > 1000 || r.MaxBytes < 0 || r.MaxBytes < MinContextBytes && r.MaxBytes != 0 || r.MaxBytes > MaxContextBytes {
				return errors.New("invalid context query")
			}
		case HistoryRead, Watch, EventsWatch, Search:
			if r.Target == nil || r.LookupID != "" || r.Limit < 1 || r.Limit > 1000 || len(r.Cursor) > 2048 || r.MaxBytes != 0 || r.ThreadMode != "" && (r.Target.Kind != "room" || !protocol.ValidMessageID(r.ParentID)) || r.ThreadMode == "" && r.ParentID != "" {
				return errors.New("invalid history or event query")
			}
			if r.ThreadMode != "" && r.ThreadMode != "thread" && r.ThreadMode != "replies" {
				return errors.New("invalid history conversation mode")
			}
			if r.Operation == Search {
				if strings.TrimSpace(r.Query) == "" || len(r.Query) > 256 || !utf8.ValidString(r.Query) || strings.IndexFunc(r.Query, unicode.IsControl) >= 0 {
					return errors.New("invalid search query")
				}
				if r.From == "*" {
					r.From = ""
				}
				if r.From != "" {
					from := Target{Kind: "direct", Name: r.From}
					if err := from.Normalize(); err != nil {
						return errors.New("invalid search sender")
					}
					r.From = from.Name
				}
			} else if r.Query != "" {
				return errors.New("search query on non-search operation")
			}
		case PinsRead:
			if r.Target == nil || r.Target.Kind != "room" || r.LookupID != "" || r.Cursor != "" || r.Query != "" || r.From != "" || r.Limit != 0 || r.MaxBytes != 0 {
				return errors.New("invalid pins query")
			}
		case RoomRead:
			if r.Target == nil || r.Target.Kind != "room" || r.ParentID != "" || r.LookupID != "" || r.Cursor != "" || r.Query != "" || r.From != "" || r.Limit != 0 || r.MaxBytes != 0 {
				return errors.New("invalid room query")
			}
		case WaitingRead:
			if r.Target == nil || r.Target.Kind != "room" || !protocol.ValidMessageID(r.ParentID) || r.LookupID != "" || r.Cursor != "" || r.Query != "" || r.From != "" || r.Limit != 0 || r.MaxBytes != 0 {
				return errors.New("invalid waiting query")
			}
		case PollResults:
			if r.Target == nil || r.Target.Kind != "room" || !protocol.ValidMessageID(r.PollID) || r.ParentID != "" || r.LookupID != "" || r.Cursor != "" || r.Query != "" || r.From != "" || r.Limit != 0 || r.MaxBytes != 0 {
				return errors.New("invalid poll results query")
			}
		case TopicRead, MembersRead:
			if r.Target == nil || r.Target.Kind != "room" || r.ParentID != "" || r.LookupID != "" || r.Cursor != "" || r.Query != "" || r.From != "" || r.Limit != 0 || r.MaxBytes != 0 {
				return errors.New("invalid room snapshot query")
			}
		case Directory:
			if r.Target != nil || r.LookupID != "" || r.Cursor != "" || r.Limit < 1 || r.Limit > 256 || r.MaxBytes != 0 {
				return errors.New("invalid directory query")
			}
			if r.Query != "" {
				nick := Target{Kind: "direct", Name: r.Query}
				if err := nick.Normalize(); err != nil {
					return errors.New("invalid directory nickname")
				}
				r.Query = nick.Name
			}
		default:
			return errors.New("unsupported operation")
		}
	}
	if r.DeliveryPolicy != nil && *r.DeliveryPolicy != Retry && *r.DeliveryPolicy != ReceiptOnly {
		return errors.New("invalid delivery policy")
	}
	if r.IssuedAt != nil && *r.IssuedAt < 0 || r.ExpiresAt != nil && *r.ExpiresAt < 0 {
		return errors.New("invalid operation time")
	}
	if r.IssuedAt != nil && r.ExpiresAt != nil && *r.ExpiresAt <= *r.IssuedAt {
		return errors.New("expiry must follow issue time")
	}
	return nil
}

// Normalize validates the pinned AIRC target grammar and canonicalizes only
// direct-message nicknames. Authorization must still be checked separately.
func (t *Target) Normalize() error {
	switch t.Kind {
	case "room":
		if len(t.Name) < 2 || len(t.Name) > 64 || !utf8.ValidString(t.Name) || t.Name[0] != '#' && t.Name[0] != '&' {
			return errors.New("invalid room")
		}
		for _, r := range t.Name {
			if unicode.IsControl(r) || r <= ' ' || r == ',' || r == ':' {
				return errors.New("invalid room")
			}
		}
	case "direct":
		if len(t.Name) < 1 || len(t.Name) > 30 {
			return errors.New("invalid nickname")
		}
		for i, r := range t.Name {
			letter := r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || strings.ContainsRune("[]\\`_^{|}", r)
			if !letter && !(i > 0 && (r >= '0' && r <= '9' || r == '-')) {
				return errors.New("invalid nickname")
			}
		}
		t.Name = strings.ToLower(t.Name)
	default:
		return errors.New("invalid target kind")
	}
	return nil
}

func validID(s string, max int) bool {
	if len(s) < 1 || len(s) > max {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

func messageID(s string) bool {
	if len(s) != 32 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// Fingerprint represents explicit normalized intent, including which defaults
// were omitted. Admission must atomically reuse the first stored effective
// values for repeated omitted fields rather than resolving defaults again.
func (r Request) Fingerprint() (string, error) {
	// Clone the target so value-receiver normalization cannot modify the caller.
	if r.Target != nil {
		target := *r.Target
		r.Target = &target
	}
	if err := r.Normalize(); err != nil {
		return "", err
	}
	r.RequestID = ""             // identity is scoped independently from content
	data, err := json.Marshal(r) // fixed struct order, never map iteration
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(append([]byte("airc-gateway-intent-v1\x00"), data...))
	return fmt.Sprintf("%x", sum), nil
}

// UpstreamID binds stable operation identity to a realm, backend, and verified
// principal. Credential IDs and ephemeral socket addresses must not be inputs.
func UpstreamID(realm, binding, principal, requestID string) (string, error) {
	if realm == "" || binding == "" || principal == "" || !validID(requestID, 64) {
		return "", errors.New("invalid request scope")
	}
	data, _ := json.Marshal([4]string{realm, binding, principal, requestID})
	sum := sha256.Sum256(append([]byte("airc-gateway-upstream-id-v1\x00"), data...))
	return fmt.Sprintf("%x", sum), nil
}
