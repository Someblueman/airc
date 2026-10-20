//go:build unix

package auth

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/xmppwire"
)

type delayedXMPPRead struct {
	reader   io.Reader
	buffered chan struct{}
}

func (r *delayedXMPPRead) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		close(r.buffered)
		time.Sleep(xmppJoinDeadline + 100*time.Millisecond)
	}
	return n, err
}

func TestXMPPBufferedJoinCannotClearExpiredDeadline(t *testing.T) {
	// The fixture's read outlasts the join deadline; a short one keeps it brief.
	deadline := xmppJoinDeadline
	t.Cleanup(func() { xmppJoinDeadline = deadline })
	xmppJoinDeadline = time.Second
	s, _ := xmppSessionFixture(t, "join_valid")
	x, err := s.Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer x.Close()
	// Retain the authenticated connection's actual confirmation bytes, but delay
	// delivery from an already completed read. This isolates buffered parsing
	// after socket I/O succeeded; it is not a separate authentication fixture.
	buffered := make(chan struct{})
	x.reader = xmppwire.NewStream(io.MultiReader(strings.NewReader(`<s:stream xmlns:s="http://etherx.jabber.org/streams" xmlns="jabber:client">`), &delayedXMPPRead{reader: x.conn, buffered: buffered}))
	if _, err = x.reader.Open(); err != nil {
		t.Fatal(err)
	}
	if err = x.JoinRooms(); err == nil {
		t.Fatal("buffered confirmation cleared expired deadline")
	}
	select {
	case <-buffered:
	default:
		t.Fatal("fixture did not buffer confirmation before deadline")
	}
	if _, err = x.conn.Write([]byte("closed")); err == nil {
		t.Fatal("expired session still open")
	}
}
