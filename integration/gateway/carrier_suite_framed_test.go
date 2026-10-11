//go:build darwin || linux

package gateway_test

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/coder/websocket"
)

// frameSuiteClient speaks correlated plain frames: WebSocket text messages,
// or 4-byte length-prefixed frames on a TCP or Unix stream.
type frameSuiteClient struct {
	ws       *websocket.Conn
	stream   net.Conn
	sequence int
}

func dialWebSocketSuite(t *testing.T, d suiteDial) (suiteClient, error) {
	ctx, cancel := suiteContext(t)
	defer cancel()
	url, options := "ws://"+d.address+"/v1/socket", &websocket.DialOptions{Subprotocols: []string{adapter.WebSocketSubprotocol}}
	if d.tls != nil {
		url, options.HTTPClient = "wss://"+d.address+"/v1/socket", &http.Client{Transport: &http.Transport{TLSClientConfig: d.tls}}
	}
	conn, _, err := websocket.Dial(ctx, url, options)
	if err != nil {
		return nil, err
	}
	conn.SetReadLimit(adapter.MaxResponseFrameBytes)
	return &frameSuiteClient{ws: conn}, nil
}

func dialFramedSuite(network string) func(*testing.T, suiteDial) (suiteClient, error) {
	return func(_ *testing.T, d suiteDial) (suiteClient, error) {
		if d.tls != nil {
			d.tls.NextProtos = []string{adapter.FramedALPN}
		}
		conn, err := suiteConnect(network, d)
		if err != nil {
			return nil, err
		}
		return &frameSuiteClient{stream: conn}, nil
	}
}

// write sends one frame; the request is embedded verbatim, so it may be
// invalid JSON. It reports false when the peer has closed.
func (c *frameSuiteClient) write(t *testing.T, id string, request []byte, stream bool) bool {
	t.Helper()
	frame := fmt.Appendf(nil, `{"version":1,"correlation_id":%q,"profile":"plain","stream":%t,"request":%s}`, id, stream, request)
	if c.ws != nil {
		ctx, cancel := suiteContext(t)
		defer cancel()
		return c.ws.Write(ctx, websocket.MessageText, frame) == nil
	}
	_ = c.stream.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, err := c.stream.Write(binary.BigEndian.AppendUint32(nil, uint32(len(frame))))
	if err == nil {
		_, err = c.stream.Write(frame)
	}
	return err == nil
}

// read returns the next response, or nil once the peer has closed.
func (c *frameSuiteClient) read(t *testing.T) *adapter.FrameResponse {
	t.Helper()
	var body []byte
	var err error
	if c.ws != nil {
		ctx, cancel := suiteContext(t)
		_, body, err = c.ws.Read(ctx)
		cancel()
		if errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("no WebSocket reply")
		}
	} else {
		_ = c.stream.SetReadDeadline(time.Now().Add(10 * time.Second))
		var header [4]byte
		if _, err = io.ReadFull(c.stream, header[:]); err == nil {
			body = make([]byte, binary.BigEndian.Uint32(header[:]))
			_, err = io.ReadFull(c.stream, body)
		}
		if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
			t.Fatal("no framed reply")
		}
	}
	if err != nil {
		return nil
	}
	var response adapter.FrameResponse
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("frame %s: %v", body, err)
	}
	return &response
}

func (c *frameSuiteClient) call(t *testing.T, request []byte) suiteReply {
	t.Helper()
	c.sequence++
	id := fmt.Sprint("call-", c.sequence)
	if !c.write(t, id, request, false) {
		return suiteReply{closed: true}
	}
	response := c.read(t)
	switch {
	case response == nil:
		return suiteReply{closed: true}
	case response.CorrelationID != id:
		t.Fatalf("uncorrelated reply %+v", response)
	case response.Error != nil:
		return suiteReply{failure: response.Error}
	}
	return suiteReply{result: response.Result}
}

func (c *frameSuiteClient) send(t *testing.T, request []byte) {
	t.Helper()
	c.sequence++
	if !c.write(t, fmt.Sprint("send-", c.sequence), request, false) {
		t.Fatal("connection closed")
	}
}

func (c *frameSuiteClient) watch(t *testing.T, request []byte) (func() *backend.HistoryPage, func()) {
	t.Helper()
	if !c.write(t, "watch", request, true) {
		t.Fatal("connection closed")
	}
	return func() *backend.HistoryPage {
		t.Helper()
		response := c.read(t)
		if response == nil || response.Event == "end" || response.Error != nil {
			return nil
		}
		if response.CorrelationID != "watch" || response.Page == nil {
			t.Fatalf("watch frame %+v", response)
		}
		return response.Page
	}, c.close
}

func (c *frameSuiteClient) close() {
	if c.ws != nil {
		_ = c.ws.CloseNow()
	} else {
		_ = c.stream.Close()
	}
}

// suiteConnect opens a stream connection, over TLS when d asks for it.
func suiteConnect(network string, d suiteDial) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: 3 * time.Second}
	if d.tls != nil {
		return tls.DialWithDialer(dialer, network, d.address, d.tls)
	}
	return dialer.Dial(network, d.address)
}

// unixSuiteWire: the gateway creates its Unix socket owner-only.
func unixSuiteWire(t *testing.T, e *suiteEnv) {
	info, err := os.Lstat(e.address)
	if err != nil || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0600 {
		t.Fatal("Unix listener is not an owner-only socket", info, err)
	}
}
