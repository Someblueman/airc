package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestMCPCapacityChild(t *testing.T) {
	if len(os.Args) == 0 || os.Args[len(os.Args)-1] != "capacity-child" {
		return
	}
	time.Sleep(time.Hour) // cancelled by the parent's CommandContext
}

func TestMCPReservedCapacityAndRelease(t *testing.T) {
	a := mcpAdapter{binary: os.Args[0], slots: make(chan struct{}, 4), waits: make(chan struct{}, 2)}
	args := []string{"-test.run=^TestMCPCapacityChild$", "--", "capacity-child"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan struct{}, 4)
	for i := 0; i < 4; i++ {
		go func(waiting bool) {
			defer func() { done <- struct{}{} }()
			a.call(ctx, args, "", time.Hour, waiting)
		}(i < 2)
		deadline := time.Now().Add(time.Second)
		for len(a.slots) != i+1 {
			if time.Now().After(deadline) {
				t.Fatal("call did not acquire capacity")
			}
			time.Sleep(time.Millisecond)
		}
		if i == 1 {
			_, _, err := a.call(ctx, args, "", time.Hour, true)
			if err == nil || !strings.Contains(err.Error(), "two waits") {
				t.Fatal("wait limit not enforced", err)
			}
		}
	}
	if _, _, err := a.call(ctx, args, "", time.Hour, false); err == nil || !strings.Contains(err.Error(), "four calls") {
		t.Fatal("total call limit not enforced", err)
	}
	cancel()
	for i := 0; i < 4; i++ {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("cancelled child did not exit")
		}
	}
	if len(a.slots) != 0 || len(a.waits) != 0 {
		t.Fatal("cancellation leaked capacity")
	}
	// Failed child starts must also return both reservations.
	a.binary = "/nonexistent/airc"
	a.call(context.Background(), nil, "", time.Second, true)
	if len(a.slots) != 0 || len(a.waits) != 0 {
		t.Fatal("failed child leaked capacity")
	}
}
