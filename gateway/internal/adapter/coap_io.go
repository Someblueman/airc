package adapter

import (
	"context"
	"io"

	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"github.com/plgd-dev/go-coap/v3/message/pool"
)

// coapPeer is what the adapter uses of a go-coap connection. The adapter owns
// admission and cancellation; the peer owns its transport framing.
type coapPeer interface {
	AcquireMessage(context.Context) *pool.Message
	ReleaseMessage(*pool.Message)
	WriteMessage(*pool.Message) error
	Close() error
}

type coapResponder interface {
	SetResponse(codes.Code, message.MediaType, io.ReadSeeker, ...message.Option) error
	Message() *pool.Message
}

func (s *coapState) setNotificationMID(msg *pool.Message) {
	if s == nil || msg == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	watch := s.active[string(msg.Token())]
	if watch != nil && msg.Type() == message.Confirmable && msg.HasOption(message.Observe) {
		watch.mid = msg.MessageID()
	}
}

// sent records transport-visible success only after the carrier write returns.
// This is the sole activity/ready transition for all CoAP peer consumers.
func (s *coapState) sent(msg *pool.Message, activity func()) {
	if s == nil || msg == nil {
		return
	}
	s.mu.Lock()
	watch := s.active[string(msg.Token())]
	authenticated := s.authenticated[string(msg.Token())]
	if watch != nil && msg.Code() == codes.Content {
		if observe, err := msg.Observe(); err == nil && observe == 0 {
			watch.readyOnce.Do(func() { close(watch.ready) })
		}
	}
	s.mu.Unlock()
	if authenticated && msg.Code() == codes.Content && activity != nil {
		activity()
	}
}
