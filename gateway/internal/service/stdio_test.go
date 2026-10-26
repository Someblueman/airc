package service

import (
	"context"
	"errors"
	"os"
	"testing"
)

func TestStdioCancellationPreservesIndependentFailure(t *testing.T) {
	failure := errors.New("journal failed")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := stdioError(ctx, context.Canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("unexpected cancellation hidden: %v", err)
	}
	cancel()
	if err := stdioError(ctx, errors.Join(context.Canceled, os.ErrClosed)); err != nil {
		t.Fatalf("expected shutdown errors: %v", err)
	}
	err := stdioError(ctx, errors.Join(context.Canceled, errors.Join(os.ErrClosed, failure)))
	if !errors.Is(err, failure) || errors.Is(err, context.Canceled) || errors.Is(err, os.ErrClosed) {
		t.Fatalf("shutdown hid independent failure: %v", err)
	}
}
