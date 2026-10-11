package adapter

import (
	"context"
	"io"

	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"github.com/plgd-dev/go-coap/v3/message/pool"
)

// coapPeer is the minimal association surface shared by go-coap UDP sessions
// and the TCP/TLS consumer. The adapter owns admission and cancellation; the
// peer owns its transport framing.
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
// It and wrote are the sole activity/ready transition for all CoAP peer
// consumers.
func (s *coapState) sent(msg *pool.Message, activity func()) {
	if s == nil || msg == nil {
		return
	}
	observeZero := false
	if msg.Code() == codes.Content {
		observe, err := msg.Observe()
		observeZero = err == nil && observe == 0
	}
	s.wrote(msg.Token(), msg.Code(), observeZero, activity)
}

// wrote is sent for a message already returned to its pool, given its token,
// its code and whether it carried Observe 0. Plain UDP calls it from its
// receive hook once a response is written (ServeCoAPUDP).
func (s *coapState) wrote(token message.Token, code codes.Code, observeZero bool, activity func()) {
	s.mu.Lock()
	watch := s.active[string(token)]
	authenticated := s.authenticated[string(token)]
	if watch != nil && code == codes.Content && observeZero {
		watch.readyOnce.Do(func() { close(watch.ready) })
	}
	s.mu.Unlock()
	if authenticated && code == codes.Content && activity != nil {
		activity()
	}
}
