//go:build darwin || linux

package gateway_test

import (
	"bufio"
	"crypto/tls"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
)

type stompPeer struct {
	conn   *tls.Conn
	reader *bufio.Reader
}

func (f *stompFixture) connect(t *testing.T) *stompPeer {
	t.Helper()
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 3 * time.Second}, "tcp", f.address, f.cert.Client.Clone())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.NetConn().Close() })
	if state := conn.ConnectionState(); state.Version != tls.VersionTLS13 || len(state.VerifiedChains) == 0 || state.NegotiatedProtocol != "" {
		t.Fatal(state)
	}
	p := &stompPeer{conn: conn, reader: bufio.NewReader(conn)}
	sqWrite(t, conn, "CONNECT", []string{"accept-version:1.2", "host:" + f.config.Realm, "login:" + f.config.Principals[0].ID, "passcode:" + f.bearer, "heart-beat:0,0"}, nil)
	if frame := p.read(t); frame.command != "CONNECTED" || frame.headers["version"] != "1.2" || frame.headers["heart-beat"] != "1000,1000" {
		t.Fatal(frame)
	}
	sqWrite(t, conn, "SUBSCRIBE", []string{"id:result", "destination:response", "ack:client-individual", "receipt:subscribed"}, nil)
	if frame := p.read(t); frame.command != "RECEIPT" || frame.headers["receipt-id"] != "subscribed" {
		t.Fatal(frame)
	}
	return p
}

func (p *stompPeer) read(t *testing.T) sqFrame {
	t.Helper()
	if err := p.conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	f, err := sqDecode(p.reader)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (p *stompPeer) submit(t *testing.T, body []byte) {
	t.Helper()
	if err := p.conn.SetWriteDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	sqWrite(t, p.conn, "SEND", []string{"destination:request", "content-type:application/json", "receipt:intake"}, body)
}

func (p *stompPeer) call(t *testing.T, f *stompFixture, r core.Request) adapter.STOMPResult {
	t.Helper()
	p.submit(t, stompPlainBody(t, r))
	frame := p.read(t)
	if frame.command == "RECEIPT" {
		if frame.headers["receipt-id"] != "intake" {
			t.Fatal(frame)
		}
		frame = p.read(t)
		if frame.command != "MESSAGE" || frame.headers["destination"] != "response" || frame.headers["subscription"] != "result" || frame.headers["ack"] == "" || frame.headers["message-id"] == "" {
			t.Fatal(frame)
		}
	} else if frame.command != "ERROR" || frame.headers["receipt-id"] != "intake" {
		t.Fatal(frame)
	}
	if frame.headers["content-type"] != "application/json" {
		t.Fatal(frame)
	}
	var result adapter.STOMPResult
	if err := json.Unmarshal(frame.body, &result); err != nil {
		t.Fatalf("STOMP result is not plain JSON: %v %q", err, frame.body)
	}
	if result.Version != 1 || result.Protocol != "stomp/1.2" || result.RequestID != r.RequestID || result.RequestDestination != "request" || result.ResponseDestination != "response" || (result.Result == nil) == (result.Error == nil) {
		t.Fatal(result)
	}
	if frame.command == "MESSAGE" {
		if result.Error != nil {
			t.Fatal("failure delivered as successful MESSAGE", result)
		}
		sqWrite(t, p.conn, "ACK", []string{"id:" + frame.headers["ack"], "receipt:acked"}, nil)
		if receipt := p.read(t); receipt.command != "RECEIPT" || receipt.headers["receipt-id"] != "acked" {
			t.Fatal(receipt)
		}
	} else if result.Error == nil {
		t.Fatal("ERROR without failure result", result)
	}
	return result
}

func (p *stompPeer) accepted(t *testing.T, f *stompFixture, request core.Request, messageID string) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for {
		response := p.call(t, f, request)
		if response.Error != nil || response.Result == nil || response.Result.Result == nil || !response.Result.Result.Durable {
			t.Fatal(response)
		}
		result := response.Result.Result
		if result.State == core.Accepted {
			if result.MessageID != messageID || result.SendAttempts != 1 {
				t.Fatal(result)
			}
			return
		}
		if !time.Now().Before(deadline) {
			t.Fatal("native receipt did not commit", result)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
