//go:build darwin || linux

package gateway_test

import (
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/pion/logging"
	"github.com/pion/stun/v4"
	"github.com/pion/turn/v5"
)

type localTURN struct {
	server              *turn.Server
	address             string
	username, password  string
	requests, responses *atomic.Int64
}

func newLocalTURN(t *testing.T) localTURN {
	t.Helper()
	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	counting := &countingPacketConn{PacketConn: conn, requests: &atomic.Int64{}, responses: &atomic.Int64{}}
	const username, password, realm = "airc-test", "turn password with spaces", "airc.test"
	server, err := turn.NewServer(turn.ServerConfig{
		PacketConnConfigs: []turn.PacketConnConfig{{PacketConn: counting, RelayAddressGenerator: &turn.RelayAddressGeneratorStatic{RelayAddress: net.ParseIP("127.0.0.1"), Address: "127.0.0.1"}}},
		AuthHandler: func(attrs *turn.RequestAttributes) (string, []byte, bool) {
			if attrs.Username != username {
				return "", nil, false
			}
			return username, turn.GenerateAuthKey(username, realm, password), true
		},
		Realm: realm, LoggerFactory: logging.NewDefaultLoggerFactory(),
	})
	if err != nil {
		_ = conn.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close(); _ = conn.Close() })
	return localTURN{server: server, address: conn.LocalAddr().String(), username: username, password: password, requests: counting.requests, responses: counting.responses}
}

type countingPacketConn struct {
	net.PacketConn
	requests, responses *atomic.Int64
}

func (c *countingPacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	n, addr, err := c.PacketConn.ReadFrom(p)
	if err == nil {
		var message stun.Message
		if message.UnmarshalBinary(p[:n]) == nil && message.Type.Method == stun.MethodBinding {
			if message.Type.Class == stun.ClassRequest {
				c.requests.Add(1)
			}
			if message.Type.Class == stun.ClassSuccessResponse || message.Type.Class == stun.ClassErrorResponse {
				c.responses.Add(1)
			}
		}
	}
	return n, addr, err
}

func (c *countingPacketConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	var message stun.Message
	if message.UnmarshalBinary(p) == nil && message.Type.Method == stun.MethodBinding {
		if message.Type.Class == stun.ClassRequest {
			c.requests.Add(1)
		}
		if message.Type.Class == stun.ClassSuccessResponse || message.Type.Class == stun.ClassErrorResponse {
			c.responses.Add(1)
		}
	}
	return c.PacketConn.WriteTo(p, addr)
}

func writeICEPassword(t *testing.T, password string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "turn.password")
	if err := os.WriteFile(path, []byte(password), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
