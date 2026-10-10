package service

import (
	"context"
	"errors"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/config"
)

// RunFIFO owns one configured private pair before acquiring journal ownership.
// The single active provisioned sender is enforced by config validation; the
// shared framed dispatcher still verifies every signed/encrypted envelope.
func RunFIFO(ctx context.Context, c config.Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if err := c.CheckFiles(); err != nil {
		return err
	}
	var address string
	count := 0
	for _, l := range c.Listeners {
		if l.Enabled {
			count++
			if l.Protocol == "fifo" {
				address = l.Address
			}
		}
	}
	if count != 1 || address == "" {
		return errors.New("serve-fifo requires exactly one enabled fifo listener")
	}
	stream, err := adapter.OpenFIFOStream(address)
	if err != nil {
		return err
	}
	return runLocalStream(ctx, c, stream, "fifo")
}
