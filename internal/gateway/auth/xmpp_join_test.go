//go:build unix

package auth

import (
	"context"
	"testing"
	"time"
)

func TestXMPPJoinProvisionedRooms(t *testing.T) {
	s, _ := xmppSessionFixture(t, "join_valid")
	// A second fixed room has its own route.
	s.routes["other@muc.localhost"] = s.routes["room@muc.localhost"]
	x, err := s.Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer x.Close()
	if err = x.JoinRooms(); err != nil {
		t.Fatal(err)
	}
}

func TestXMPPJoinRefusalsCloseSession(t *testing.T) {
	for _, mode := range []string{"join_anonymous", "join_assigned", "join_error", "join_foreign", "join_early_message", "join_budget"} {
		t.Run(mode, func(t *testing.T) {
			s, _ := xmppSessionFixture(t, mode)
			x, err := s.Connect(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer x.Close()
			start := time.Now()
			if err = x.JoinRooms(); err == nil {
				t.Fatal("false own join accepted")
			}
			if mode == "join_foreign" && time.Since(start) < 2800*time.Millisecond {
				t.Fatal("foreign presence bypassed deadline")
			}
			if _, err = x.conn.Write([]byte("closed")); err == nil {
				t.Fatal("failed join kept session open")
			}
		})
	}
	if err := (*XMPPSession)(nil).JoinRooms(); err == nil {
		t.Fatal("nil session accepted")
	}
}
