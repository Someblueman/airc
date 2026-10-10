//go:build unix

package auth

import (
	"context"
	"github.com/Someblueman/airc/internal/gateway/xmppwire"
	"io"
	"strings"
	"testing"
)

func TestXMPPClosedSessionCannotStartRoomVerification(t *testing.T) {
	for _, mode := range []string{"close", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			s, _ := xmppSessionFixture(t, "join_valid")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			x, err := s.Connect(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer x.Close()
			if !x.usable() {
				t.Fatal("authenticated session unusable")
			}
			if mode == "close" {
				x.Close()
			} else {
				cancel()
			}
			if x.usable() {
				t.Fatal("closed or cancelled session usable")
			}
			if x.JoinRooms() == nil {
				t.Fatal("closed session verified rooms")
			}
			if len(x.joined) != 0 {
				t.Fatal("closed session retained membership")
			}
		})
	}
}

// cancelAfterRead returns actual authenticated socket bytes after cancelling
// the parent, so buffered discovery parsing cannot depend on another I/O error.
type cancelAfterRead struct {
	reader io.Reader
	cancel context.CancelFunc
	read   bool
}

func (r *cancelAfterRead) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		r.read = true
		r.cancel()
	}
	return n, err
}

func TestXMPPBufferedDiscoveryCannotVerifyAfterCancellation(t *testing.T) {
	s, _ := xmppSessionFixture(t, "join_valid")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	x, err := s.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer x.Close()
	r := &cancelAfterRead{reader: x.conn, cancel: cancel}
	x.reader = xmppwire.NewStream(io.MultiReader(strings.NewReader(`<s:stream xmlns:s="http://etherx.jabber.org/streams" xmlns="jabber:client">`), r))
	if _, err = x.reader.Open(); err != nil {
		t.Fatal(err)
	}
	if x.JoinRooms() == nil {
		t.Fatal("buffered discovery accepted after cancellation")
	}
	if !r.read {
		t.Fatal("fixture did not deliver actual discovery bytes")
	}
	if len(x.joined) != 0 {
		t.Fatal("cancelled discovery granted membership")
	}
}
