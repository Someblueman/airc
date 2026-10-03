package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/Someblueman/airc/internal/protocol"
)

func runOutboxCommand(ctx context.Context, opt options, retry string, pending bool, forget string, stdout io.Writer) error {
	b, err := openOutbox(opt)
	if err != nil {
		return err
	}
	defer b.close()
	if pending {
		entries := []outboundMessage{}
		for _, e := range b.Entries {
			if e.Result == nil {
				entries = append(entries, e)
			}
		}
		if opt.json {
			return json.NewEncoder(stdout).Encode(entries)
		}
		for _, e := range entries {
			if _, err := fmt.Fprintf(stdout, "%s %s\n", e.RequestID, e.Target); err != nil {
				return err
			}
		}
		return nil
	}
	id := retry
	if forget != "" {
		id = forget
	}
	if !protocol.ValidRequestID(id) {
		return errors.New("a valid request ID is required")
	}
	e := b.find(id)
	if e == nil {
		return errors.New("request ID is not in this server/nickname's outbox; inspect airc send --pending")
	}
	if forget != "" {
		for i := range b.Entries {
			if b.Entries[i].RequestID == id {
				b.Entries = append(b.Entries[:i], b.Entries[i+1:]...)
				break
			}
		}
		if err := b.save(); err != nil {
			return err
		}
		if opt.json {
			return json.NewEncoder(stdout).Encode(map[string]string{"code": "forgotten", "request_id": id})
		}
		_, err := fmt.Fprintln(stdout, "Forgot outbox request", id)
		return err
	}
	if e.Result == nil {
		r, c, err := recoverSend(ctx, opt, e)
		if err != nil {
			return uncertainSend(err, id)
		}
		defer closeOneShot(opt, c)
		e.Result = r
		if err := b.save(); err != nil {
			e := acceptedFailure(err, r)
			e.Code = "accepted_state_failed"
			e.Phase = "outbox"
			return e
		}
	}
	if err := outputSend(ctx, opt, e.Result, false, nil, 0, nil, stdout, io.Discard); err != nil {
		return acceptedFailure(err, e.Result)
	}
	return nil
}
