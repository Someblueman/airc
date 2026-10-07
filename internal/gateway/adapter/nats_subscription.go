package adapter

import (
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/natswire"
)

// Subscription state is owned only by the session reader/dispatcher goroutine.
type natsSubscription struct {
	id        string
	route     config.NATSRoute
	delivered uint32
	limit     *uint32
}

// control handles only subscription and heartbeat commands. False leaves
// application command authorization to the dispatcher, without acknowledging it.
func (c *natsConnection) control(f natswire.Frame) (bool, error) {
	switch f.Command {
	case "PING":
		return true, c.writeBefore(natswire.Frame{Command: "PONG"}, time.Time{}, false)
	case "PONG":
		c.pong()
		return true, nil
	case "SUB":
		if c.subscription != nil {
			return true, natswire.ErrFrame
		}
		for _, r := range c.routes {
			if r.ResponseSubject == f.Subject {
				c.subscription = &natsSubscription{id: f.SID, route: r}
				break
			}
		}
		if c.subscription == nil {
			return true, natswire.ErrFrame
		}
	case "UNSUB":
		if c.subscription == nil || c.subscription.id != f.SID {
			return true, natswire.ErrFrame
		}
		if f.MaxMessages == nil || *f.MaxMessages == 0 || *f.MaxMessages <= c.subscription.delivered {
			c.subscription = nil
		} else {
			n := *f.MaxMessages
			c.subscription.limit = &n
		}
	default:
		return false, nil
	}
	if c.verbose {
		return true, c.writeBefore(natswire.Frame{Command: "+OK"}, time.Time{}, false)
	}
	return true, nil
}

// sent is called after a complete response write. Loss after writing remains
// uncertain to the publisher; this counter is not a durable acknowledgement.
func (c *natsConnection) sent() {
	if c.subscription == nil {
		return
	}
	if c.subscription.delivered == 1<<32-1 {
		c.subscription = nil
		return
	}
	c.subscription.delivered++
	if c.subscription.limit != nil && c.subscription.delivered >= *c.subscription.limit {
		c.subscription = nil
	}
}
