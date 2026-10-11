// Package delivery owns automatic attempts after durable admission. Caller
// disconnects do not cancel admitted work; Run uses the service's context.
package delivery

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

type Runner struct {
	journal *store.Store
	backend *backend.Backend
	workers int
	mu      sync.Mutex
	started bool
}

func New(journal *store.Store, upstream *backend.Backend, workers int) (*Runner, error) {
	if journal == nil || upstream == nil || workers < 1 || workers > 64 {
		return nil, store.ErrInvalid
	}
	return &Runner{journal: journal, backend: upstream, workers: workers}, nil
}

// Run has no unbounded queue or goroutine creation. Each lane (an author
// scope) has at most one active worker; round-robin selection
// prevents a queued flood monopolizing the workers. On shutdown, sessions close and each worker gets five seconds to
// save its already observed outcome. The caller must keep the journal open.
func (r *Runner) Run(ctx context.Context) error {
	r.mu.Lock()
	if r.started {
		r.mu.Unlock()
		return store.ErrNotReady
	}
	r.started = true
	r.mu.Unlock()
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	type completion struct {
		lane string
		err  error
	}
	done := make(chan completion, r.workers)
	active := make(map[string]bool)
	var workers sync.WaitGroup
	defer workers.Wait()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	admitted := r.journal.Admitted()
	var cursor string
	var maintenanceAt int64
	var failure error
	stopping := false
	shutdown := ctx.Done()
	for {
		if !stopping {
			now := time.Now()
			if now.Unix() != maintenanceAt {
				maintenanceCtx, finish := context.WithTimeout(workCtx, 5*time.Second)
				_, err := r.journal.Maintain(maintenanceCtx, now)
				finish()
				if err != nil {
					if workCtx.Err() == nil {
						failure = err
					}
					stopping = true
					cancel()
				}
				maintenanceAt = now.Unix()
			}
			candidates, err := r.journal.Ready(workCtx, cursor, r.workers, time.Now())
			if err != nil {
				if workCtx.Err() == nil {
					failure = err
				}
				stopping = true
				cancel()
			}
			for _, d := range candidates {
				if stopping || len(active) >= r.workers {
					break
				}
				cursor = d.Lane
				if active[d.Lane] {
					continue
				}
				active[d.Lane] = true
				workers.Go(func() { done <- completion{d.Lane, r.attempt(workCtx, d)} })
			}
		}
		if stopping && len(active) == 0 {
			return failure
		}
		for waiting := true; waiting; {
			select {
			case c := <-done:
				delete(active, c.lane)
				if c.err != nil && failure == nil {
					failure = c.err
					stopping = true
					cancel()
				}
				waiting = false
			case <-ticker.C:
				waiting = false
			case <-admitted:
				// A new job: poll now if every worker is idle, so an idle
				// gateway does not hold it until the tick. While an attempt
				// runs, its completion polls soon anyway, and polling on
				// every admission of a busy lane would only add store-lock
				// work that admissions wait behind.
				waiting = len(active) > 0
			case <-shutdown:
				shutdown = nil
				stopping = true
				cancel()
				waiting = false
			}
		}
	}
}

func (r *Runner) attempt(ctx context.Context, d store.Dispatch) error {
	g, err := r.journal.Reserve(ctx, d.Scope, d.RequestID, d.Kind, time.Now())
	if errors.Is(err, store.ErrNotReady) || errors.Is(err, auth.ErrForbidden) || ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return err
	}
	o, err := r.backend.Run(ctx, g)
	if err != nil {
		return err
	}
	// Persistence belongs to the service, including when shutdown cancelled the
	// network attempt. A failed commit stops dispatch; the durable marker prevents
	// another attempt in this process, and restart performs uncertain recovery.
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	// Diagnosis only: which AIRC account a registered author logged in as. It
	// is saved in the outcome's own transaction, so it costs no extra commit;
	// an invalid ID is left out of the save and then stops dispatch.
	var noted error
	if o.AccountID != "" {
		noted = g.NoteAccount(o.AccountID)
	}
	if o.Acceptance != (core.Acceptance{}) {
		_, err = r.journal.Accept(saveCtx, g, o.Acceptance, o.ObservedAt, time.Now())
	} else {
		_, err = r.journal.Complete(saveCtx, g, o.Completion, time.Now())
	}
	if err == nil {
		err = noted
	}
	return err
}
