package main

import (
	"context"
	"errors"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/fallback"
	"github.com/Someblueman/airc/pkg/irc"
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
				b.emit(ctx, msgIn{event: historyEvent(m).MessageEvent()})
				b.last[d.room] = m.ID
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

// sendOverCarriers posts a room message, or retries a saved one, over a
// gateway carrier with the outbox's request ID, so a native attempt that
// landed and this one are one message.
func (b *uiBackend) sendOverCarriers(ctx context.Context, fb *fallback.Client, cmd uiCmd) (status, uncertain string, err error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	box, err := openOutbox(b.opt)
	if err != nil {
		return "", cmd.requestID, err
	}
	defer box.close()
	var entry *outboundMessage
	if cmd.kind == "retry-send" {
		if entry = box.find(cmd.target); entry == nil {
			return "", cmd.target, errors.New("saved request not found; inspect airc send --pending")
		}
		if entry.ReplyTo != "" {
			return "", entry.RequestID, errWaitsForNative // its conversation is not saved
		}
	} else if entry, err = box.add(cmd.target, "", cmd.text, cmd.requestID); err != nil {
		return "", cmd.requestID, err
	}
	if entry.Result == nil {
		result, _, err := postViaFallback(ctx, b.opt, fb.Gateways().Post, entry, "")
		if err != nil {
			if cmd.kind != "retry-send" && (fallback.Definite(err) || !fallback.MayHavePosted(err)) {
				box.remove(entry.RequestID)
				if saveErr := box.save(); saveErr != nil {
					return "", entry.RequestID, saveErr
				}
				return "", "", err
			}
			return "", entry.RequestID, uncertainSend(err, entry.RequestID)
		}
		entry.Result = result
		if err := box.save(); err != nil {
			return "", entry.RequestID, acceptedFailure(err, entry.Result)
		}
	}
	r := entry.Result
	receipt := &irc.SendReceiptEvent{Receipt: r.Receipt, ID: r.ID, From: r.From, Target: r.Target}
	if r.Delivered != nil {
		receipt.Queued = !*r.Delivered
	}
	return receiptStatus(receipt) + " · over " + carrierOf(r), "", nil
}

// carrierOf names the carrier a result went over.
func carrierOf(r *sendResult) string {
	if r.Carrier == "" {
		return "native IRC"
	}
	return r.Carrier
}
