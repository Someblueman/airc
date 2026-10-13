package adapter

import (
	"context"
	"net"
	"sync"
)

// ServeAccepted serves l until ctx ends. It closes l when ctx is done, runs
// each accepted connection's work in its own goroutine and returns only after
// all of that work has returned. accept decides what happens to a connection:
// it returns the work to run, nil to drop the connection (accept then closes
// it), or an error to stop serving. Cancellation returns nil; any other
// Accept failure is returned.
func ServeAccepted(parent context.Context, l net.Listener, accept func(ctx context.Context, conn net.Conn) (func(), error)) error {
	ctx, cancel := context.WithCancel(parent)
	var work sync.WaitGroup
	closed := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = l.Close(); close(closed) })
	defer func() {
		cancel()
		if !stop() {
			<-closed
		}
		work.Wait()
	}()
	for {
		conn, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		serve, err := accept(ctx, conn)
		if err != nil {
			return err
		}
		if serve != nil {
			work.Go(serve)
		}
	}
}

// slotted runs serve while holding one of slots, or closes conn and drops it
// when every slot is taken.
func slotted(slots chan struct{}, conn net.Conn, serve func()) func() {
	select {
	case slots <- struct{}{}:
	default:
		closeMQTTConnection(conn)
		return nil
	}
	return func() {
		defer func() { <-slots }()
		serve()
	}
}
