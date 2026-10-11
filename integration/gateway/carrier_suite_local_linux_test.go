//go:build linux

package gateway_test

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"syscall"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/config"
)

var _ = registerSuiteCarriers(suiteCarrier{name: "unixpacket", listener: config.Listener{Protocol: "unixpacket"}, configure: prepareLocalSocket, dial: dialUnixPacketSuite, results: true, reads: true, session: true, watch: true, watchModes: []string{"long_poll", "unixpacket_records"}, wire: unixSuiteWire})

// unixPacketSuiteClient sends one frame per sequenced-packet record on a
// connection, which is the session.
type unixPacketSuiteClient struct {
	*localSuiteClient
	conn *net.UnixConn
}

func dialUnixPacketSuite(_ *testing.T, d suiteDial) (suiteClient, error) {
	conn, err := net.DialUnix("unixpacket", nil, &net.UnixAddr{Name: d.address, Net: "unixpacket"})
	if err != nil {
		return nil, err
	}
	c := &unixPacketSuiteClient{conn: conn}
	c.localSuiteClient = &localSuiteClient{write: c.write, read: c.read, done: func() { _ = conn.Close() }}
	return c, nil
}

func (c *unixPacketSuiteClient) write(t *testing.T, _ string, frame []byte) {
	t.Helper()
	_ = c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if n, _, err := c.conn.WriteMsgUnix(frame, nil, nil); err != nil || n != len(frame) {
		t.Fatalf("record write: %d %v", n, err)
	}
}

// read returns one whole record, or nil once the gateway has closed.
func (c *unixPacketSuiteClient) read(t *testing.T, _ string, _ time.Duration) []byte {
	t.Helper()
	_ = c.conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	body := make([]byte, adapter.MaxResponseFrameBytes+1)
	n, _, flags, _, err := c.conn.ReadMsgUnix(body, nil)
	switch {
	case n == 0 && (errors.Is(err, io.EOF) || errors.Is(err, syscall.ECONNRESET)):
		return nil
	case err != nil || flags&syscall.MSG_TRUNC != 0 || n > adapter.MaxResponseFrameBytes:
		t.Fatalf("record read: %d %d %v", n, flags, err)
	}
	return body[:n]
}

func (c *unixPacketSuiteClient) watch(t *testing.T, request []byte) (func() *backend.HistoryPage, func()) {
	t.Helper()
	c.write(t, "watch", localSuiteFrame("watch", request, true))
	return func() *backend.HistoryPage {
		t.Helper()
		body := c.read(t, "watch", 0)
		if body == nil {
			return nil
		}
		var response adapter.FrameResponse
		if err := json.Unmarshal(body, &response); err != nil {
			t.Fatalf("watch record %s: %v", body, err)
		}
		if response.Event == "end" || response.Error != nil {
			return nil
		}
		if response.CorrelationID != "watch" || response.Page == nil {
			t.Fatalf("watch record %+v", response)
		}
		return response.Page
	}, c.close
}
