//go:build unix

package auth

import (
	"context"
	"testing"
)

func TestXMPPUnverifiedRoomDiscoveryCannotJoin(t *testing.T) {
	for _, mode := range []string{"join_discovery_wrong_id", "join_discovery_wrong_room", "join_discovery_anonymous", "join_discovery_no_sid", "join_discovery_error"} {
		t.Run(mode, func(t *testing.T) {
			s, _ := xmppSessionFixture(t, mode)
			x, err := s.Connect(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer x.Close()
			if err = x.JoinRooms(); err == nil {
				t.Fatal("uncorrelated/unsupported room joined")
			}
			if len(x.joined) != 0 || len(x.occupants) != 0 {
				t.Fatal("failed discovery retained room authority")
			}
			if _, err = x.conn.Write([]byte("closed")); err == nil {
				t.Fatal("discovery failure kept session open")
			}
		})
	}
}
