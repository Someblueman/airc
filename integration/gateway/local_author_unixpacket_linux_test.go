//go:build linux

package gateway_test

import (
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
)

// A Unix sequenced-packet listener takes plain author frames, one record
// each, behind the owner-only socket and peer credential check.
func TestLocalAuthorsOverUnixPacket(t *testing.T) {
	f := newLocalAuthorFixture(t)
	c, root := f.config(t, config.Listener{ID: "packet", Protocol: "unixpacket"})
	c.Listeners[0].Address = filepath.Join(root, "packet.sock")
	g := startAuthorGateway(t, c)
	conn, err := net.DialUnix("unixpacket", nil, &net.UnixAddr{Name: g.addresses["packet"], Net: "unixpacket"})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	exerciseLocalAuthors(t, f, "packet", func(id string, request map[string]any) plainFrameResponse {
		if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
			t.Fatal(err)
		}
		if _, _, err := conn.WriteMsgUnix(plainFrame(t, id, request), nil, nil); err != nil {
			t.Fatal(err)
		}
		reply := make([]byte, 64<<10)
		n, _, _, _, err := conn.ReadMsgUnix(reply, nil)
		if err != nil {
			t.Fatal(err)
		}
		return decodePlainResponse(t, id, reply[:n])
	})
}
