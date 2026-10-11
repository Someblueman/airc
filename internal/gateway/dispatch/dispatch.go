// Package dispatch is the ingress boundary: it applies a listener's or
// route's author access policy and runs each plain contract request as the
// author it names. Adapters never contact AIRC directly; admitted mutations
// belong to the service workers.
package dispatch

import (
	"errors"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

var ErrUnsupported = errors.New(string(core.UnsupportedOperation))

type Dispatcher struct {
	journal *store.Store
	backend *backend.Backend
	cursors *auth.CursorCodec
	realm   string
	// gate is the listener's or route's author access policy.
	gate *gate
}

// newDispatcher checks the shared dependencies of every constructor: native
// reads need the backend and cursored reads the cursor key.
func newDispatcher(journal *store.Store, reader *backend.Backend, cursors *auth.CursorCodec, configured []core.Operation) (*Dispatcher, error) {
	if journal == nil || reader == nil {
		return nil, store.ErrInvalid
	}
	for _, op := range configured {
		if !op.Valid() {
			return nil, store.ErrInvalid
		}
		if (op == core.HistoryRead || op == core.Search || op.IsWatch()) && cursors == nil {
			return nil, store.ErrInvalid
		}
	}
	return &Dispatcher{journal: journal, backend: reader, cursors: cursors, realm: reader.Realm()}, nil
}

// ImplementedOperations is the current gateway intake set, not a transport-completeness claim.
func ImplementedOperations() []core.Operation {
	return []core.Operation{core.Send, core.Reply, core.ReactionAdd, core.ContextRead, core.PinsRead, core.RoomRead, core.WaitingRead, core.PollResults, core.TopicRead, core.MembersRead, core.ReceiptGet, core.HistoryRead, core.Search, core.Watch, core.EventsWatch, core.Directory, core.ChannelsRead, core.ProfileUpdate, core.PresenceSet, core.TopicSet, core.RoomUpdate, core.PinsAdd, core.PinsRemove, core.ActivitySet, core.ReplySignalSet, core.ReplySignalCancel, core.PollCreate, core.PollVote, core.PollClose, core.MessageAction, core.MessageCorrect, core.MessageRetract, core.AgentsRead}
}
