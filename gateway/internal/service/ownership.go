package service

import (
	"encoding/json"
	"net"
	"net/http"
	"sync"

	"github.com/Someblueman/airc/gateway/internal/core"
)

type limitedListener struct {
	net.Listener
	slots chan struct{}
}

func (l limitedListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		select {
		case l.slots <- struct{}{}:
			bounded := &limitedConn{Conn: conn, slots: l.slots}
			packet, ok := conn.(*net.UnixConn)
			if wrapped, isWrapped := conn.(interface{ UnixPacket() *net.UnixConn }); isWrapped {
				packet, ok = wrapped.UnixPacket(), true
			}
			if ok && packet.LocalAddr().Network() == "unixpacket" {
				return &limitedUnixPacketConn{limitedConn: bounded, packet: packet}, nil
			}
			return bounded, nil
		default:
			_ = conn.Close()
		}
	}
}

type limitedConn struct {
	net.Conn
	slots chan struct{}
	once  sync.Once
}

func (c *limitedConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { <-c.slots })
	return err
}

// Retain record methods while Close continues to release the global slot once.
type limitedUnixPacketConn struct {
	*limitedConn
	packet *net.UnixConn
}

func (c *limitedUnixPacketConn) ReadMsgUnix(body, control []byte) (int, int, int, *net.UnixAddr, error) {
	return c.packet.ReadMsgUnix(body, control)
}

func (c *limitedUnixPacketConn) WriteMsgUnix(body, control []byte, address *net.UnixAddr) (int, int, error) {
	return c.packet.WriteMsgUnix(body, control, address)
}

type handlers struct {
	mu     sync.Mutex
	closed bool
	work   sync.WaitGroup
}

func (h *handlers) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		if h.closed {
			h.mu.Unlock()
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(struct {
				Version int          `json:"version"`
				Error   core.Failure `json:"error"`
			}{1, core.Failure{Code: core.UpstreamUnavailable, Transient: true}})
			return
		}
		h.work.Add(1)
		h.mu.Unlock()
		defer h.work.Done()
		next.ServeHTTP(w, r)
	})
}

func (h *handlers) stop() {
	h.mu.Lock()
	h.closed = true
	h.mu.Unlock()
}
