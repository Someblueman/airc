package core

// ErrorCode is stable across transport-specific error encodings.
type ErrorCode string

const (
	AuthenticationFailed ErrorCode = "authentication_failed"
	Forbidden            ErrorCode = "forbidden"
	UnsupportedOperation ErrorCode = "unsupported_operation"
	InvalidRequest       ErrorCode = "invalid_request"
	IDConflict           ErrorCode = "id_conflict"
	CapacityExceeded     ErrorCode = "capacity_exceeded"
	UpstreamUnavailable  ErrorCode = "upstream_unavailable"
	DeliveryUncertain    ErrorCode = "delivery_uncertain"
)

type Failure struct {
	Code         ErrorCode `json:"code"`
	Transient    bool      `json:"transient"`
	UpstreamCode int       `json:"upstream_code,omitempty"`
}

type State string

const (
	Queued      State = "queued"
	Dispatching State = "dispatching"
	RetryWait   State = "retry_wait"
	Uncertain   State = "uncertain"
	Accepted    State = "accepted"
	Rejected    State = "rejected"
	Expired     State = "expired"
	Blocked     State = "blocked"
)

// Result distinguishes gateway responsibility from upstream observations.
// Nil receipt facts mean unknown; recipient_connected never means read.
type Result struct {
	Version             int                         `json:"version"`
	RequestID           string                      `json:"request_id"`
	State               State                       `json:"state"`
	Durable             bool                        `json:"durable"`
	MessageID           string                      `json:"message_id,omitempty"`
	Accepted            *bool                       `json:"accepted"`
	Persisted           *bool                       `json:"persisted"`
	RecipientConnected  *bool                       `json:"recipient_connected"`
	DuplicateRisk       bool                        `json:"duplicate_risk"`
	AcceptanceUncertain bool                        `json:"acceptance_uncertain"`
	SendAttempts        int                         `json:"send_attempts"`
	LookupAttempts      int                         `json:"lookup_attempts"`
	ExpiresAt           int64                       `json:"expires_at"`
	NextAttemptAt       *int64                      `json:"next_attempt_at,omitempty"`
	NextAction          string                      `json:"next_action"`
	Failure             *Failure                    `json:"error,omitempty"`
	Card                *CardAcknowledgement        `json:"card,omitempty"`
	Topic               *TopicAcknowledgement       `json:"topic,omitempty"`
	Room                *RoomAcknowledgement        `json:"room,omitempty"`
	Pin                 *PinAcknowledgement         `json:"pin,omitempty"`
	Activity            *ActivityAcknowledgement    `json:"activity,omitempty"`
	ReplySignal         *ReplySignalAcknowledgement `json:"reply_signal,omitempty"`
	Poll                *PollAcknowledgement        `json:"poll,omitempty"`
}

func (o Operation) Valid() bool {
	switch o {
	case Send, Reply, ReactionAdd, ContextRead, PinsRead, RoomRead, WaitingRead, PollResults, TopicRead, ReceiptGet, HistoryRead, Watch, Search, Directory, ProfileUpdate, PresenceSet, TopicSet, RoomUpdate, PinsAdd, PinsRemove, ActivitySet, ReplySignalSet, ReplySignalCancel, PollCreate, PollVote, PollClose:
		return true
	}
	return false
}

// EffectiveOperations preserves implemented order and never accepts client
// claims. Each set comes from a trusted adapter/config/principal/upstream view.
func EffectiveOperations(implemented, configured, allowed, upstream []Operation) []Operation {
	contains := func(set []Operation, op Operation) bool {
		for _, x := range set {
			if x == op {
				return true
			}
		}
		return false
	}
	result := make([]Operation, 0)
	for _, op := range implemented {
		if op.Valid() && !contains(result, op) && contains(configured, op) && contains(allowed, op) && contains(upstream, op) {
			result = append(result, op)
		}
	}
	return result
}
