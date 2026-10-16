package server

import (
	"context"
	"os"
	"sync"

	"github.com/Someblueman/airc/internal/atomicfile"
)

// groupCommit lets concurrent posts share one history-file sync. A post writes
// its line under messageMu, releases it while it waits for a sync covering that
// line, and takes it back in ticket order, so the file, broadcasts and receipts
// all keep message order. Whichever waiter finds no sync running syncs
// everything written so far; the posts that wrote meanwhile ride on the next.
type groupCommit struct {
	mu       sync.Mutex
	file     *os.File
	mode     atomicfile.Sync
	issued   uint64 // the last ticket handed to a post
	resumed  uint64 // the last ticket whose post has taken messageMu back
	appended uint64 // the last ticket whose line was written to file
	durable  uint64 // every line written through this ticket is synced
	syncing  bool
	err      error                                 // a failed sync: no later line on this file becomes durable
	synced   chan struct{}                         // closed when the running sync ends
	turns    map[uint64]chan struct{}              // closed when the ticket before the key resumes
	syncFile func(*os.File, atomicfile.Sync) error // tests replace atomicfile.File
}

// init starts syncing file, the history file just opened or swapped in
// with every earlier line already synced.
func (c *groupCommit) init(file *os.File, mode atomicfile.Sync) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.file, c.mode, c.err = file, mode, nil
	c.durable = c.appended
}

// idle reports that no post is between its write and taking messageMu back.
func (c *groupCommit) idle() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.issued == c.resumed
}

func (c *groupCommit) issue() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.issued++
	return c.issued
}

// wrote records that ticket's line is in the file. Tickets are written in
// order because the caller holds messageMu.
func (c *groupCommit) wrote(ticket uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.appended = ticket
}

// waitDurable returns once a sync covers ticket's line, or with the error
// that means it never will.
func (c *groupCommit) waitDurable(ticket uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for c.durable < ticket {
		if c.err != nil {
			return c.err
		}
		if c.syncing {
			synced := c.synced
			c.mu.Unlock()
			<-synced
			c.mu.Lock()
			continue
		}
		if c.file == nil {
			return os.ErrClosed
		}
		c.syncing, c.synced = true, make(chan struct{})
		target, file, sync := c.appended, c.file, c.syncFile
		if sync == nil {
			sync = atomicfile.File
		}
		c.mu.Unlock()
		err := sync(file, c.mode)
		c.mu.Lock()
		c.syncing = false
		close(c.synced)
		if err != nil {
			c.err = err
		} else {
			c.durable = max(c.durable, target)
		}
	}
	return nil
}

// drain syncs every written line. The caller holds messageMu, so nothing
// new is written meanwhile.
func (c *groupCommit) drain() error {
	c.mu.Lock()
	appended := c.appended
	c.mu.Unlock()
	return c.waitDurable(appended)
}

// drainContext is drain bounded by ctx; a stalled sync is left running.
func (c *groupCommit) drainContext(ctx context.Context) {
	done := make(chan struct{})
	go func() {
		_ = c.drain()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// awaitTurn returns once every earlier ticket's post has taken messageMu
// back; the caller then takes it and calls resume.
func (c *groupCommit) awaitTurn(ticket uint64) {
	c.mu.Lock()
	if c.resumed == ticket-1 {
		c.mu.Unlock()
		return
	}
	if c.turns == nil {
		c.turns = make(map[uint64]chan struct{})
	}
	turn := make(chan struct{})
	c.turns[ticket] = turn
	c.mu.Unlock()
	<-turn
}

func (c *groupCommit) resume(ticket uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.resumed = ticket
	if next, ok := c.turns[ticket+1]; ok {
		close(next)
		delete(c.turns, ticket+1)
	}
}

// retire closes file if it is still the one being synced, and reports
// whether it did, so a failure is handled once.
func (c *groupCommit) retire(file *os.File) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if file == nil || c.file != file {
		return false
	}
	c.file = nil
	_ = file.Close()
	return true
}
