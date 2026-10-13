package service

import (
	"context"
	"errors"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/config"
)

// RunFIFO owns one configured private pair before acquiring journal ownership.
// The private pair is the access gate; each frame names its author.
func RunFIFO(ctx context.Context, c config.Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if err := c.CheckFiles(); err != nil {
		return err
	}
	listener, ok := soleListener(c, "fifo")
	if !ok {
		return errors.New("serve-fifo requires exactly one enabled fifo listener")
	}
	stream, err := adapter.OpenFIFOStream(listener.Address)
	if err != nil {
		return err
	}
	return runLocalStream(ctx, c, stream, "fifo")
}
