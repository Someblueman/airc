package adapter

import (
	"errors"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/natswire"
)

// serveNATS has no publication queue: the sole reader completes admission and
// the bounded response write before consuming another command.
func (c *natsConnection) serveNATS(realm string, d *dispatch.Dispatcher, sealer *auth.ResponseSealer, requests chan struct{}) {
	for {
		f, err := c.readBefore(time.Time{})
		if err != nil {
			return
		}
		handled, err := c.control(f)
		if err != nil {
			c.fail()
			return
		}
		if handled {
			continue
		}
		if f.Command != "PUB" || c.subscription == nil {
			c.fail()
			return
		}
		sub := c.subscription
		a := admitNATS(c.ctx, d, c.identity, sub.route, f, requests)
		if a.refused {
			c.fail()
			return
		}
		scope := natsResponseScope(realm, c.identity.ID(), c.identity.Binding(), sub.route)
		payload, err := natsResultMessage(sealer, scope, sub.route, a.requestID, a.result, a.failure, time.Now())
		if errors.Is(err, auth.ErrResponseCapacity) {
			payload, err = natsResultMessage(sealer, scope, sub.route, a.requestID, nil, &core.Failure{Code: core.CapacityExceeded, Transient: true}, time.Now())
		}
		if err != nil {
			c.fail()
			return
		}
		if c.verbose {
			if c.writeBefore(natswire.Frame{Command: "+OK"}, time.Time{}, false) != nil {
				return
			}
		}
		if c.writeBefore(natswire.Frame{Command: "MSG", Subject: sub.route.ResponseSubject, SID: sub.id, Payload: payload}, time.Time{}, false) != nil {
			return
		}
		c.sent()
	}
}
