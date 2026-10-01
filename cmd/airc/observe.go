package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/Someblueman/airc/pkg/irc"
	"strings"
	"time"
)

func awaitObservation(ctx context.Context, client *irc.Client, count int) error {
	return awaitObservationWith(ctx, client, count, nil, false)
}

// awaitObservationWith waits for count subscription acknowledgements. other, if
// set, sees every unrelated event so a live stream does not lose any while
// subscriptions are being added.
func awaitObservationWith(ctx context.Context, client *irc.Client, count int, other func(irc.Event), expiredThreads bool) error {
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for acknowledged := 0; acknowledged < count; {
		select {
		case event, ok := <-client.Events():
			if !ok {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return errors.New("server disconnected before observation started")
			}
			if raw, isRaw := event.(*irc.RawEvent); isRaw && (raw.Command == "765" || expiredThreads && raw.Command == "430" && len(raw.Params) > 1 && strings.HasPrefix(raw.Params[1], "thread:")) {
				acknowledged++
			} else if err := serverError(event); err != nil {
				return fmt.Errorf("cannot wait for messages: %w", err)
			} else if other != nil {
				other(event)
			}
		case <-client.Done():
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return errors.New("server disconnected before observation started")
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return errors.New("timed out waiting for server to confirm observation")
		}
	}
	return nil
}
