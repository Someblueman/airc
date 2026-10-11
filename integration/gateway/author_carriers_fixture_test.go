//go:build darwin || linux

package gateway_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/service"
	"github.com/Someblueman/airc/internal/testfs"
	"github.com/Someblueman/airc/pkg/irc"
	"github.com/coder/websocket"
)

// authorGateway is an in-process gateway serving plain author requests to a
// current-tree aircd, with listeners named by ID.
type authorGateway struct {
	cancel    context.CancelFunc
	done      chan error
	addresses map[string]string
}

// authorConfig is a zero-policy gateway: plaintext listeners on loopback, no
// gateway credentials, and one upstream with aircd's gateway credential.
func authorConfig(t *testing.T, socket, gatewayToken string, listeners ...config.Listener) config.Config {
	t.Helper()
	root, err := testfs.MkdirTemp("agw-author-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	c := config.Defaults()
	c.Version, c.Realm, c.StateDir = 1, "author-fixture", filepath.Join(root, "state")
	if err := os.Mkdir(c.StateDir, 0700); err != nil {
		t.Fatal(err)
	}
	c.CursorKeyFile = filepath.Join(root, "cursor.key")
	if err := auth.CreateCredential(c.CursorKeyFile); err != nil {
		t.Fatal(err)
	}
	c.Upstream = &config.Upstream{Network: "unix", Address: socket, GatewayTokenFile: gatewayToken}
	for _, l := range listeners {
		l.Enabled = true
		if l.Protocol == "unix" {
			l.Address = filepath.Join(root, l.ID+".sock")
		} else if l.Address == "" {
			// Enabled listeners need distinct addresses: reserve a free port.
			probe, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			l.Address = probe.Addr().String()
			_ = probe.Close()
		}
		if l.PasswordFile != "" {
			path := filepath.Join(root, l.ID+".password")
			if err := os.WriteFile(path, []byte(l.PasswordFile+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			l.PasswordFile = path
		}
		c.Listeners = append(c.Listeners, l)
	}
	return c
}

func startAuthorGateway(t *testing.T, c config.Config) *authorGateway {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	reader, writer := io.Pipe()
	g := &authorGateway{cancel: cancel, done: make(chan error, 1), addresses: map[string]string{}}
	go func() {
		err := service.Run(ctx, c, writer)
		_ = writer.CloseWithError(io.EOF)
		g.done <- err
	}()
	line, err := bufio.NewReader(reader).ReadBytes('\n')
	go func() { _, _ = io.Copy(io.Discard, reader) }()
	if err != nil {
		cancel()
		t.Fatalf("gateway did not start: %v %v", err, <-g.done)
	}
	var ready struct {
		Listeners []struct {
			ID      string `json:"id"`
			Address string `json:"address"`
		} `json:"listeners"`
	}
	if err := json.Unmarshal(line, &ready); err != nil {
		t.Fatal(err)
	}
	for _, l := range ready.Listeners {
		g.addresses[l.ID] = l.Address
	}
	t.Cleanup(func() { g.stop(t) })
	return g
}

func (g *authorGateway) stop(t *testing.T) {
	t.Helper()
	if g.cancel == nil {
		return
	}
	g.cancel()
	g.cancel = nil
	select {
	case err := <-g.done:
		if err != nil {
			t.Errorf("gateway stopped with %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Error("gateway did not stop")
	}
}

// httpCall sends a plain HTTP request and decodes the JSON reply into out.
func plainHTTP(t *testing.T, method, url string, body any, headers map[string]string, out any) int {
	t.Helper()
	var reader io.Reader
	if body != nil {
		data, ok := body.([]byte)
		if !ok {
			var err error
			if data, err = json.Marshal(body); err != nil {
				t.Fatal(err)
			}
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			t.Fatalf("%s %s: %v: %s", method, url, err, data)
		}
	}
	return resp.StatusCode
}

type failureBody struct {
	Error core.Failure `json:"error"`
}

// plainRequest is a contract request with an optional author, as JSON.
func plainRequest(fields map[string]any, nick, token string) map[string]any {
	r := map[string]any{"version": 1}
	for k, v := range fields {
		r[k] = v
	}
	if nick != "" {
		author := map[string]any{"nick": nick}
		if token != "" {
			author["token"] = token
		}
		r["author"] = author
	}
	return r
}

func sendRequest(id, room, message, nick, token string) map[string]any {
	return plainRequest(map[string]any{"request_id": id, "operation": "send", "target": map[string]any{"kind": "room", "name": room}, "message": message}, nick, token)
}

// waitReceipt polls receipt.get over HTTP until the job is terminal.
func waitReceipt(t *testing.T, base, id string, headers map[string]string) core.Result {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		var result core.Result
		status := plainHTTP(t, http.MethodGet, base+"/v1/requests/"+id, nil, headers, &result)
		if status != http.StatusOK {
			t.Fatalf("receipt %s: HTTP %d", id, status)
		}
		switch result.State {
		case core.Accepted, core.Blocked, core.Rejected, core.Expired, core.CredentialWait:
			return result
		}
		if time.Now().After(deadline) {
			t.Fatalf("receipt %s still %s", id, result.State)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func authorHeaders(nick, token string) map[string]string {
	h := map[string]string{"AIRC-Nick": nick}
	if token != "" {
		h["AIRC-Token"] = token
	}
	return h
}

// frameConn is a correlated connection: WebSocket or a framed Unix stream.
type frameConn struct {
	ws     *websocket.Conn
	stream net.Conn
}

func dialWebSocket(t *testing.T, address string) *frameConn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws://"+address+"/v1/socket", &websocket.DialOptions{Subprotocols: []string{"airc-gateway.v1"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	return &frameConn{ws: conn}
}

func dialFramedUnix(t *testing.T, path string) *frameConn {
	t.Helper()
	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return &frameConn{stream: conn}
}

type plainFrameResponse struct {
	CorrelationID string `json:"correlation_id"`
	Event         string `json:"event"`
	Result        *struct {
		Operation core.Operation `json:"operation"`
		Result    *core.Result   `json:"result"`
		Page      *struct {
			Messages []*irc.MessageMetadata `json:"messages"`
			Cursor   string                 `json:"cursor"`
			Status   string                 `json:"status"`
		} `json:"page"`
		Account *struct {
			Nick      string `json:"nick"`
			AccountID string `json:"account_id"`
		} `json:"account"`
	} `json:"result"`
	Error *core.Failure `json:"error"`
}

// call sends one plain frame and returns its response.
func (c *frameConn) call(t *testing.T, id string, request map[string]any) plainFrameResponse {
	t.Helper()
	frame, err := json.Marshal(map[string]any{"version": 1, "correlation_id": id, "profile": "plain", "request": request})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var reply []byte
	if c.ws != nil {
		if err := c.ws.Write(ctx, websocket.MessageText, frame); err != nil {
			t.Fatal(err)
		}
		if _, reply, err = c.ws.Read(ctx); err != nil {
			t.Fatal(err)
		}
	} else {
		_ = c.stream.SetDeadline(time.Now().Add(10 * time.Second))
		var header [4]byte
		binary.BigEndian.PutUint32(header[:], uint32(len(frame)))
		if _, err := c.stream.Write(append(header[:], frame...)); err != nil {
			t.Fatal(err)
		}
		if _, err := io.ReadFull(c.stream, header[:]); err != nil {
			t.Fatal(err)
		}
		reply = make([]byte, binary.BigEndian.Uint32(header[:]))
		if _, err := io.ReadFull(c.stream, reply); err != nil {
			t.Fatal(err)
		}
	}
	var response plainFrameResponse
	if err := json.Unmarshal(reply, &response); err != nil || response.CorrelationID != id {
		t.Fatalf("frame reply %s: %v", reply, err)
	}
	return response
}

func historyCount(t *testing.T, base, room, text string, headers map[string]string) int {
	t.Helper()
	var page struct {
		Messages []*irc.MessageMetadata `json:"messages"`
	}
	if status := plainHTTP(t, http.MethodGet, base+"/v1/history?kind=room&target="+strings.ReplaceAll(room, "#", "%23")+"&limit=100&start=retained", nil, headers, &page); status != http.StatusOK {
		t.Fatalf("history: HTTP %d", status)
	}
	count := 0
	for _, m := range page.Messages {
		if m.Message == text {
			count++
		}
	}
	return count
}
