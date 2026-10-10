package core

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
	AgentsRead     Operation = "agents.read"
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

// IsMutation identifies operations admitted as durable jobs.
func (op Operation) IsMutation() bool {
	switch op {
	case Send, Reply, ReactionAdd, ProfileUpdate, PresenceSet, TopicSet, RoomUpdate, PinsAdd, PinsRemove, ActivitySet, ReplySignalSet, ReplySignalCancel, PollCreate, PollVote, PollClose, MessageAction, MessageCorrect, MessageRetract:
		return true
	}
	return false
}

// IsNativeRead identifies reads and watches that need a native read grant.
// Receipt and capability lookups are handled locally, without that grant.
func (op Operation) IsNativeRead() bool {
	switch op {
	case HistoryRead, Search, Watch, EventsWatch, Directory, ContextRead, PinsRead, RoomRead, WaitingRead, PollResults, TopicRead, MembersRead, ChannelsRead, AgentsRead:
		return true
	}
	return false
}

// Valid reports whether op is a configurable AIRC permission.
func (op Operation) Valid() bool {
	return op.IsMutation() || op.IsNativeRead() || op == ReceiptGet
}
