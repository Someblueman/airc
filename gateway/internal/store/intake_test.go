//go:build darwin || linux

package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/core"
)

func TestConcurrentCoalescingAndRestart(t *testing.T) {
	c, identity, other := fixture(t)
	ctx := context.Background()
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var wg sync.WaitGroup
	created := make(chan bool, 32)
	for range 32 {
		wg.Go(func() {
			j, new, err := s.AdmitAuthor(ctx, identity, request(), 0, time.Unix(1000, 0))
			if err != nil {
				t.Error(err)
				return
			}
			if j.Result.State != core.Queued || !j.Result.Durable || j.Request.Message != "first\nsecond" {
				t.Error("wrong saved intent")
			}
			created <- new
		})
	}
	wg.Wait()
	close(created)
	count := 0
	for new := range created {
		if new {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("created %d operations", count)
	}
	if _, err := s.LookupAuthor(ctx, other, "request"); !errors.Is(err, ErrNotFound) {
		t.Fatal("author could read another job", err)
	}
	r := request()
	r.Message = "different"
	if _, _, err := s.AdmitAuthor(ctx, identity, r, 0, time.Unix(1000, 0)); !errors.Is(err, ErrConflict) {
		t.Fatal("conflicting body accepted", err)
	}
	r = request()
	p := core.Retry
	r.DeliveryPolicy = &p
	if _, _, err := s.AdmitAuthor(ctx, identity, r, 0, time.Unix(1000, 0)); !errors.Is(err, ErrConflict) {
		t.Fatal("explicit policy conflicted with omission silently", err)
	}
	before, err := s.LookupAuthor(ctx, identity, "request")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	c.Retry.Policy = core.ReceiptOnly
	c.Retry.LifetimeSeconds = 100
	s, err = Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	after, new, err := s.AdmitAuthor(ctx, identity, request(), 0, time.Unix(1001, 0))
	if err != nil || new || after.Result.ExpiresAt != before.Result.ExpiresAt || after.Policy != core.Retry || after.UpstreamID != before.UpstreamID {
		t.Fatalf("restart changed frozen defaults: %+v %v", after, err)
	}
}

func TestQuota(t *testing.T) {
	c, identity, other := fixture(t)
	c.Limits.Pending = 1
	c.Limits.PendingPerAuthor = 1
	s, err := Open(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if _, _, err := s.AdmitAuthor(ctx, Submission{}, request(), 0, time.Unix(1000, 0)); !errors.Is(err, ErrInvalid) {
		t.Fatal("submission without an author accepted")
	}
	if _, _, err := s.AdmitAuthor(ctx, identity, request(), 0, time.Unix(1000, 0)); err != nil {
		t.Fatal(err)
	}
	if _, new, err := s.AdmitAuthor(ctx, identity, request(), 0, time.Unix(1000, 0)); err != nil || new {
		t.Fatal("duplicate blocked by full quota", err)
	}
	r := request()
	r.RequestID = "other-request"
	if _, _, err := s.AdmitAuthor(ctx, other, r, 0, time.Unix(1000, 0)); !errors.Is(err, ErrCapacity) {
		t.Fatal("global quota bypassed", err)
	}
}
