package adapter

import (
	"context"
	"errors"
	"github.com/Someblueman/airc/internal/gateway/sysvqueue"
	"net"
	"strconv"
)

// SystemVQueueAddr is a fixed, provisioned key, never producer identity.
type SystemVQueueAddr struct{ Key int32 }

func (*SystemVQueueAddr) Network() string  { return "system_v_queue" }
func (a *SystemVQueueAddr) String() string { return strconv.FormatInt(int64(a.Key), 10) }

// NewSystemVQueuePackets transfers all supplied handles only on success.
// Responses use exclusively provisioned destinations; producer packets cannot
// choose another queue. A successful receive remains destructive, not admission.
func NewSystemVQueuePackets(ingress *sysvqueue.Queue, responses map[int32]*sysvqueue.Queue) (*queuePackets, error) {
	if ingress == nil || ingress.Identity().Key <= 0 || len(responses) == 0 {
		return nil, sysvqueue.ErrInvalid
	}
	routes := make(map[int32]*sysvqueue.Queue, len(responses))
	for key, q := range responses {
		if q == nil || key <= 0 || q.Identity().Key != key || key == ingress.Identity().Key {
			return nil, sysvqueue.ErrInvalid
		}
		routes[key] = q
	}
	return &queuePackets{
		local: &SystemVQueueAddr{Key: ingress.Identity().Key},
		read:  ingress.ReadMessage,
		write: func(ctx context.Context, body []byte, dest net.Addr) error {
			addr, ok := dest.(*SystemVQueueAddr)
			if !ok || addr == nil || routes[addr.Key] == nil {
				return sysvqueue.ErrInvalid
			}
			return routes[addr.Key].WriteMessage(ctx, body)
		},
		closeQueues: func() error {
			err := ingress.Close()
			for _, q := range routes {
				err = errors.Join(err, q.Close())
			}
			return err
		},
		operations: make(map[*queuePacketOperation]struct{}),
	}, nil
}
