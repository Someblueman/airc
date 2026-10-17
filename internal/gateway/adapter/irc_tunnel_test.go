package adapter

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// fakeAIRC accepts one connection at a time and echoes each line it reads,
// prefixed with "echo ", so a test can see exactly what reached it and in
// what order. A line "QUIT" closes the connection.
func fakeAIRC(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				scanner := bufio.NewScanner(conn)
				for scanner.Scan() {
					line := scanner.Text()
					if _, err := io.WriteString(conn, "echo "+line+"\r\n"); err != nil {
						return
					}
					if line == "QUIT" {
						return
					}
				}
			}()
		}
	}()
	return listener.Addr().String()
}

func tunnelUpstream(t *testing.T) IRCUpstream {
	t.Helper()
	return IRCUpstream{Network: "tcp", Address: fakeAIRC(t), GatewayToken: strings.Repeat("a", 64), Listener: "tunnel"}
}

func TestIRCWebSocketTunnelAssertsTheSourceThenRelaysLines(t *testing.T) {
	server, err := IRCWebSocketServer(tunnelUpstream(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(server.Handler)
	defer ts.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/", &websocket.DialOptions{Subprotocols: []string{IRCWebSocketText}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	if conn.Subprotocol() != IRCWebSocketText {
		t.Fatalf("subprotocol %q", conn.Subprotocol())
	}
	read := func() string {
		t.Helper()
		kind, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if kind != websocket.MessageText {
			t.Fatalf("message kind %v", kind)
		}
		return string(data)
	}
	// WEBIRC with the client's loopback source reached the upstream before
	// the client's first line, and each message is one line, CRLF or not.
	if err := conn.Write(ctx, websocket.MessageText, []byte("NICK bot\r\n")); err != nil {
		t.Fatal(err)
	}
	if got := read(); got != "echo WEBIRC 127.0.0.1 :"+strings.Repeat("a", 64) {
		t.Fatalf("first upstream line %q", got)
	}
	if got := read(); got != "echo NICK bot" {
		t.Fatalf("relayed line %q", got)
	}
	if err := conn.Write(ctx, websocket.MessageText, []byte("USER u 0 * :u")); err != nil {
		t.Fatal(err)
	}
	if got := read(); got != "echo USER u 0 * :u" {
		t.Fatalf("relayed line %q", got)
	}
	// The upstream closing ends the socket cleanly.
	if err := conn.Write(ctx, websocket.MessageText, []byte("QUIT")); err != nil {
		t.Fatal(err)
	}
	if got := read(); got != "echo QUIT" {
		t.Fatalf("relayed line %q", got)
	}
	if _, _, err := conn.Read(ctx); websocket.CloseStatus(err) != websocket.StatusNormalClosure {
		t.Fatalf("after the upstream closed: %v", err)
	}
}

func TestIRCWebSocketTunnelRefusesMultilineMessagesAndOrigins(t *testing.T) {
	server, err := IRCWebSocketServer(tunnelUpstream(t), []string{"https://app.example"})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(server.Handler)
	defer ts.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(ts.URL, "http") + "/v1/irc"
	if _, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {"https://evil.example"}}}); err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("an unknown origin was not refused: %v", err)
	}
	conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {"https://app.example"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	if err := conn.Write(ctx, websocket.MessageText, []byte("NICK a\r\nNICK b")); err != nil {
		t.Fatal(err)
	}
	for {
		_, _, err := conn.Read(ctx) // the WEBIRC echo arrives first
		if err == nil {
			continue
		}
		if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
			t.Fatalf("two lines in one message: %v", err)
		}
		return
	}
}

func TestIRCConnectTunnelRelaysPipelinedBytes(t *testing.T) {
	server, err := IRCConnectServer(tunnelUpstream(t))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(server.Handler)
	defer ts.Close()
	conn, err := net.Dial("tcp", strings.TrimPrefix(ts.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	// The first IRC line rides in the same packet as the CONNECT.
	if _, err := io.WriteString(conn, "CONNECT airc:6667 HTTP/1.1\r\nHost: airc:6667\r\n\r\nNICK bot\r\n"); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("CONNECT: %v %v", resp, err)
	}
	line := func() string {
		t.Helper()
		s, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimRight(s, "\r\n")
	}
	if got := line(); got != "echo WEBIRC 127.0.0.1 :"+strings.Repeat("a", 64) {
		t.Fatalf("first upstream line %q", got)
	}
	if got := line(); got != "echo NICK bot" {
		t.Fatalf("pipelined line %q", got)
	}
	if _, err := io.WriteString(conn, "QUIT\r\n"); err != nil {
		t.Fatal(err)
	}
	if got := line(); got != "echo QUIT" {
		t.Fatalf("relayed line %q", got)
	}
	if _, err := reader.ReadString('\n'); err != io.EOF {
		t.Fatalf("after the upstream closed: %v", err)
	}
}

func TestIRCConnectTunnelRefusesOtherMethods(t *testing.T) {
	server, err := IRCConnectServer(tunnelUpstream(t))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(server.Handler)
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed || resp.Header.Get("Allow") != http.MethodConnect {
		t.Fatalf("GET: %d %q", resp.StatusCode, resp.Header.Get("Allow"))
	}
}
