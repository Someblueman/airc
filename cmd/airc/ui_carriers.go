package main

import (
	"context"
	"errors"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/fallback"
)

// errWaitsForNative refuses, draft kept, what only native IRC can do while
// the UI continues over another carrier.
var errWaitsForNative = errors.New("native IRC is unreachable; this waits until it returns; draft kept")

// bridge keeps the UI's rooms live over the server's other carriers while
// native IRC is unreachable, from the last message shown in each, and posts
// plain messages there with the outbox's request IDs. Direct messages and
// everything else wait for native IRC, whose session then catches up.
func (b *uiBackend) bridge(ctx context.Context) error {
	starts := map[string]string{}
	for name := range b.observed {
		if protocol.IsChannel(name) {
			starts[name] = b.last[name]
		}
	}
	if len(starts) == 0 {
		return nil
	}
	fb, _, err := racingClient(b.opt, preferLast)
	if err != nil || fb == nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	pages, wait := followRooms(ctx, fb, starts)
	defer wait()
	defer cancel()
	announced := ""
	for {
		select {
		case <-ctx.Done():
			return nil
		case d := <-pages:
			if carrier := d.page.Endpoint.String(); carrier != announced {
				announced = carrier
				b.emit(ctx, statusIn{text: continuingText(carrier, true)})
			}
			if d.page.Gap {
				b.emit(ctx, statusIn{text: "some messages in " + d.room + " from while native IRC was unreachable are no longer available", isError: true})
			}
			for _, m := range d.page.Messages {
				b.translate(ctx, historyEvent(m).MessageEvent()) // notifies and advances b.last
			}
		case cmd := <-b.cmds:
			switch {
			case cmd.kind == "send" || cmd.kind == "retry-send":
				acceptance, id, err := b.sendOverCarriers(ctx, fb, cmd)
				b.emit(ctx, deliveryIn{cmd: cmd, err: err, uncertainID: id, acceptance: acceptance})
			case confirmedUICmd(cmd):
				b.emit(ctx, deliveryIn{cmd: cmd, err: errWaitsForNative})
			default:
				b.emit(ctx, statusIn{text: errWaitsForNative.Error(), isError: true})
			}
		}
	}
}

// sendOverCarriers posts a message, or retries a saved one, over a gateway
// carrier with the outbox's request ID, so a native attempt that landed and
// this one are one message. A saved reply waits for native IRC, since its
// conversation is not saved.
func (b *uiBackend) sendOverCarriers(ctx context.Context, fb *fallback.Client, cmd uiCmd) (status, uncertain string, err error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	attempt := func(ctx context.Context, entry *outboundMessage) (*sendResult, bool, error) {
		if entry.ReplyTo != "" {
			return nil, false, errWaitsForNative
		}
		result, _, err := postViaFallback(ctx, b.opt, fb.Gateways().Post, entry, "")
		return result, true, err
	}
	definite := func(err error, attempted bool) bool {
		return !attempted || fallback.Definite(err) || !fallback.MayHavePosted(err)
	}
	status, uncertain, err = b.deliver(ctx, cmd, attempt, definite)
	if err == nil {
		status += " · over another carrier"
	}
	return status, uncertain, err
}
