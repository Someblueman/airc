package adapter

import (
	"crypto/rand"
	"errors"
	"slices"
	"strconv"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/stompwire"
)

// stompResultDeadline bounds how long a delivered result may stay unacked; a
// variable only so tests can shorten it.
var stompResultDeadline = 30 * time.Second

type stompSubscription struct {
	id    string
	route config.STOMPRoute
}

type stompPending struct {
	id       string
	body     []byte
	deadline time.Time
}

func (c *stompConnection) serveSTOMP(routes []config.STOMPRoute, d *dispatch.Dispatcher, requests chan struct{}) {
	var subscription *stompSubscription
	var pending *stompPending
	for {
		deadline := time.Time{}
		if pending != nil {
			deadline = pending.deadline
			if !time.Now().Before(deadline) {
				return
			}
		}
		f, err := c.readBefore(deadline)
		if err != nil || (!deadline.IsZero() && !time.Now().Before(deadline)) {
			return
		}
		if f.Command == "" {
			continue
		}
		switch f.Command {
		case "SUBSCRIBE":
			if subscription != nil || !stompControlHeaders(f, "id", "destination", "ack") {
				c.fail("invalid STOMP subscription")
				return
			}
			id, _ := f.Header("id")
			destination, _ := f.Header("destination")
			ack, _ := f.Header("ack")
			if len(id) < 1 || len(id) > 255 || ack != "client-individual" {
				c.fail("invalid STOMP subscription")
				return
			}
			for _, route := range routes {
				if destination == route.ResponseDestination {
					subscription = &stompSubscription{id: id, route: route}
					break
				}
			}
			if subscription == nil {
				c.fail("unauthorized STOMP subscription")
				return
			}
		case "UNSUBSCRIBE":
			id, _ := f.Header("id")
			if !stompControlHeaders(f, "id") || subscription == nil || id != subscription.id || pending != nil {
				c.fail("invalid STOMP unsubscribe")
				return
			}
			subscription = nil
		case "SEND":
			if pending != nil {
				c.fail("STOMP result awaiting acknowledgement")
				return
			}
			// Without a subscription a SEND is answered by its receipt only.
			destination, _ := f.Header("destination")
			var route *config.STOMPRoute
			for i := range routes {
				if destination == routes[i].RequestDestination {
					route = &routes[i]
					break
				}
			}
			if route == nil || subscription != nil && subscription.route.RequestDestination != route.RequestDestination {
				c.fail("unknown STOMP destination")
				return
			}
			a := admitSTOMP(c.ctx, d, dispatch.Ingress{Source: c.source, Session: c.session}, *route, f, requests)
			if a.refusal != "" {
				c.fail(a.refusal)
				return
			}
			body, err := stompResultMessage(*route, a.requestID, a.result, a.failure)
			if errors.Is(err, errResultCapacity) {
				a.failure = &core.Failure{Code: core.CapacityExceeded, Transient: true}
				body, err = stompResultMessage(*route, a.requestID, nil, a.failure)
			}
			if err != nil {
				c.fail("STOMP result unavailable")
				return
			}
			if a.failure != nil {
				headers := []stompwire.Header{{Key: "message", Value: "STOMP request failed"}, {Key: "content-type", Value: "application/json"}, {Key: "content-length", Value: strconv.Itoa(len(body))}}
				if receipt, ok := f.Header("receipt"); ok {
					headers = append(headers, stompwire.Header{Key: "receipt-id", Value: receipt})
				}
				_ = c.writeBefore(stompwire.Frame{Command: "ERROR", Headers: headers, Body: body}, time.Time{}, true)
				return
			}
			if subscription == nil {
				break
			}
			pending = &stompPending{id: rand.Text(), body: body, deadline: time.Now().Add(stompResultDeadline)}
			if c.stompReceipt(f, pending.deadline, false) != nil {
				return
			}
			message := stompwire.Frame{Command: "MESSAGE", Headers: []stompwire.Header{
				{Key: "subscription", Value: subscription.id}, {Key: "destination", Value: subscription.route.ResponseDestination},
				{Key: "message-id", Value: pending.id}, {Key: "ack", Value: pending.id},
				{Key: "content-type", Value: "application/json"}, {Key: "content-length", Value: strconv.Itoa(len(pending.body))},
			}, Body: pending.body}
			if c.writeBefore(message, pending.deadline, false) != nil {
				return
			}
			continue
		case "ACK", "NACK":
			id, _ := f.Header("id")
			if !stompControlHeaders(f, "id") || pending == nil || id != pending.id {
				c.fail("invalid STOMP acknowledgement")
				return
			}
			if f.Command == "NACK" {
				c.fail("STOMP result declined; replay request")
				return
			}
			pending = nil
		case "DISCONNECT":
			if !stompControlHeaders(f) {
				c.fail("invalid STOMP disconnect")
				return
			}
			_ = c.stompReceipt(f, deadline, true)
			return
		default:
			c.fail("unsupported STOMP command")
			return
		}
		if c.stompReceipt(f, deadline, false) != nil {
			return
		}
	}
}

func stompControlHeaders(f stompwire.Frame, allowed ...string) bool {
	if len(f.Body) != 0 {
		return false
	}
	for _, h := range f.Headers {
		if h.Key != "receipt" && !slices.Contains(allowed, h.Key) {
			return false
		}
	}
	if receipt, ok := f.Header("receipt"); ok && (len(receipt) < 1 || len(receipt) > 255) {
		return false
	}
	return true
}

func (c *stompConnection) stompReceipt(f stompwire.Frame, deadline time.Time, terminal bool) error {
	if receipt, ok := f.Header("receipt"); ok {
		return c.writeBefore(stompwire.Frame{Command: "RECEIPT", Headers: []stompwire.Header{{Key: "receipt-id", Value: receipt}}}, deadline, terminal)
	}
	return nil
}
