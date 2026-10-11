//go:build darwin || linux

package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/nngwire"
	"github.com/Someblueman/airc/internal/gateway/zmqwire"
	"github.com/Someblueman/airc/pkg/irc"
)

// localREPCarrier is a request-reply socket protocol served by
// serveLocalREPConnection.
type localREPCarrier struct {
	protocol string
	wire     localREPWire
	// handshake completes the client side of the protocol handshake.
	handshake func(t *testing.T, c net.Conn)
	// request frames one request body, correlated where the protocol can.
	request func(body []byte) ([]byte, error)
	// reply reads one reply body.
	reply func(t *testing.T, c net.Conn) []byte
	// malformed is a framed request the protocol cannot carry; partial is
	// a handshake prefix.
	malformed, partial []byte
}

func localREPCarriers() []localREPCarrier {
	return []localREPCarrier{
		{
			protocol: "nng", wire: nngREP, handshake: nngClient,
			request: func(body []byte) ([]byte, error) { return nngwire.Reply(0x80000001, body) },
			reply: func(t *testing.T, c net.Conn) []byte {
				t.Helper()
				body, err := nngwire.ReadRequest(c)
				if err != nil {
					t.Fatal(err)
				}
				if body.Correlation != 0x80000001 {
					t.Fatal("wire correlation", body.Correlation)
				}
				return body.Body
			},
			// A frame without the backtrace's terminal bit.
			malformed: []byte{0, 0, 0, 0, 0, 0, 0, 5, 0, 0, 0, 1},
			partial:   []byte{0, 'S'},
		},
		{
			protocol: "zeromq", wire: zeroMQREP, handshake: zeroMQClient,
			request: zmqwire.Reply,
			reply: func(t *testing.T, c net.Conn) []byte {
				t.Helper()
				traffic, err := zmqwire.ReadTraffic(c)
				if err != nil {
					t.Fatal(err)
				}
				return traffic.Body
			},
			// A multipart request.
			malformed: []byte{1, 0, 1, 1, 'x'},
			partial:   []byte{0xff, 0},
		},
	}
}

func nngClient(t *testing.T, c net.Conn) {
	t.Helper()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	var greeting [8]byte
	if _, err := io.ReadFull(c, greeting[:]); err != nil || greeting != nngwire.Greeting() {
		t.Fatal("REP greeting", greeting, err)
	}
	if _, err := c.Write([]byte{0, 'S', 'P', 0, 0, 0x30, 0, 0}); err != nil {
		t.Fatal(err)
	}
}

func zeroMQClient(t *testing.T, c net.Conn) {
	t.Helper()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	if err := zmqwire.ReadGreeting(c); err != nil {
		t.Fatal(err)
	}
	greeting := zmqwire.Greeting()
	ready := zmqwire.Ready()
	ready[len(ready)-1] = 'Q'
	if _, err := c.Write(append(greeting[:], ready...)); err != nil {
		t.Fatal(err)
	}
	f, err := zmqwire.ReadFrame(c)
	if err != nil || !f.Command || !bytes.Contains(f.Body, []byte("REP")) {
		t.Fatal("server READY", err)
	}
}

// localREPSession is one served connection and its client end.
type localREPSession struct {
	c        localREPCarrier
	f        plainFixture
	client   net.Conn
	requests chan struct{}
	cancel   context.CancelFunc
	done     chan struct{}
}

func startLocalREP(t *testing.T, c localREPCarrier, f plainFixture, requests chan struct{}) *localREPSession {
	t.Helper()
	server, client := net.Pipe()
	ctx, cancel := context.WithCancel(t.Context())
	s := &localREPSession{c: c, f: f, client: client, requests: requests, cancel: cancel, done: make(chan struct{})}
	go func() { serveLocalREPConnection(ctx, server, f.d, requests, c.wire); close(s.done) }()
	t.Cleanup(func() { cancel(); _ = client.Close(); <-s.done })
	return s
}

func (s *localREPSession) send(t *testing.T, body string) LocalREPResponse {
	t.Helper()
	wire, err := s.c.request([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.client.Write(wire); err != nil {
		t.Fatal(err)
	}
	var response LocalREPResponse
	if err = json.Unmarshal(s.c.reply(t, s.client), &response); err != nil {
		t.Fatal(err)
	}
	return response
}

// joined waits for the served connection to end.
func (s *localREPSession) joined(t *testing.T, why string) {
	t.Helper()
	select {
	case <-s.done:
	case <-time.After(time.Second):
		t.Fatal(why)
	}
}

// TestLocalREPCarriers exercises the request-reply carriers' shared owner
// once per protocol: durable replay, refusals, capacity, conflicts, resend
// after reconnect, whole reads, malformed framing and joined shutdown.
func TestLocalREPCarriers(t *testing.T) {
	for _, c := range localREPCarriers() {
		t.Run(c.protocol, func(t *testing.T) {
			t.Parallel() // Each carrier has its own journal and sockets.
			t.Run("send-replay-and-refusals", func(t *testing.T) {
				f := newLocalREPFixture(t, c.protocol)
				s := startLocalREP(t, c, f, make(chan struct{}, 1))
				c.handshake(t, s.client)
				payload := f.body(t, mqttSNRequest())
				for range 2 {
					response := s.send(t, payload)
					if response.Error != nil || response.Result == nil || response.RequestID != mqttSNRequest().RequestID || response.Result.Result == nil || !response.Result.Result.Durable {
						t.Fatalf("send %+v", response)
					}
				}
				job, err := f.lookup(t, mqttSNRequest().RequestID)
				if err != nil || job.Request.Message != mqttSNRequest().Message {
					t.Fatalf("job %+v %v", job, err)
				}
				for _, body := range []string{"malformed", `{"version":1,"request_id":"no-author","operation":"send","target":{"kind":"room","name":"#fixture"},"message":"x"}`} {
					if response := s.send(t, body); response.Error == nil || response.Result != nil {
						t.Fatalf("refusal %+v", response)
					}
				}
				s.requests <- struct{}{}
				if response := s.send(t, payload); response.Error == nil || response.Error.Code != core.CapacityExceeded || len(s.requests) != 1 {
					t.Fatalf("capacity %+v", response)
				}
				<-s.requests
				s.cancel()
				s.joined(t, "connection not joined")
				if len(s.requests) != 0 {
					t.Fatal("request slot leaked")
				}
			})
			t.Run("conflict-keeps-original-intent", func(t *testing.T) {
				f := newLocalREPFixture(t, c.protocol)
				s := startLocalREP(t, c, f, make(chan struct{}, 1))
				c.handshake(t, s.client)
				request := mqttSNRequest()
				if response := s.send(t, f.body(t, request)); response.Error != nil {
					t.Fatal(response.Error)
				}
				request.Message = "changed body"
				if response := s.send(t, f.body(t, request)); response.Error == nil || response.Error.Code != core.IDConflict || response.Result != nil {
					t.Fatalf("conflict %+v", response)
				}
				if job, err := f.lookup(t, request.RequestID); err != nil || job.Request.Message != mqttSNRequest().Message {
					t.Fatal("conflict changed the original intent", job, err)
				}
			})
			t.Run("resend-after-reconnect", func(t *testing.T) {
				f := newLocalREPFixture(t, c.protocol)
				request := mqttSNRequest()
				var upstreamID string
				for range 2 {
					s := startLocalREP(t, c, f, make(chan struct{}, 1))
					c.handshake(t, s.client)
					if reply := s.send(t, f.body(t, request)); reply.Error != nil || reply.RequestID != request.RequestID || reply.Result == nil || reply.Result.Result == nil || !reply.Result.Result.Durable {
						t.Fatalf("resend %+v", reply)
					}
					s.cancel()
					s.joined(t, "connection not joined")
					job, err := f.lookup(t, request.RequestID)
					if err != nil || job.Request.Message != request.Message || upstreamID != "" && job.UpstreamID != upstreamID {
						t.Fatal("resend replaced durable job", job, err)
					}
					upstreamID = job.UpstreamID
				}
				if upstreamID == "" {
					t.Fatal("missing durable identity")
				}
			})
			for _, large := range []bool{false, true} {
				// Scripted native CHECK output exercises the SDK; it is not
				// native qualification.
				t.Run(map[bool]string{false: "whole-history", true: "oversized-reply"}[large], func(t *testing.T) {
					f := newLocalREPFixture(t, c.protocol)
					messages := []irc.MessageMetadata{{ID: fmt.Sprintf("%032x", 1), Target: "#fixture", From: "native-author", Timestamp: time.Unix(1000, 0).UTC(), Message: "exact\n世界"}, {ID: fmt.Sprintf("%032x", 2), Target: "#fixture", From: "native-author", Timestamp: time.Unix(1000, 0).UTC(), Message: "whole end"}}
					if large {
						for i := range 20 {
							messages = append(messages, irc.MessageMetadata{ID: fmt.Sprintf("%032x", i+3), Target: "#fixture", From: "native-author", Timestamp: time.Unix(1000, 0).UTC(), Message: strings.Repeat("x", 4096)})
						}
					}
					reads := localREPNativeHistory(t, f, mqttSNRequest().Target.Name, messages, "CHECK=1")
					s := startLocalREP(t, c, f, make(chan struct{}, 1))
					c.handshake(t, s.client)
					response := s.send(t, f.body(t, core.Request{Version: 1, RequestID: "read", Operation: core.HistoryRead, Target: mqttSNRequest().Target, Limit: len(messages)}))
					if large {
						if response.Result != nil || response.Error == nil || response.Error.Code != core.CapacityExceeded {
							t.Fatalf("oversized reply %+v", response)
						}
					} else {
						if response.Error != nil || response.Result == nil || response.Result.Page == nil {
							t.Fatalf("read %+v failure %+v", response, response.Error)
						}
						page := response.Result.Page
						if len(page.Messages) != 2 || page.Messages[0].Message != "exact\n世界" || page.Messages[1].Message != "whole end" || page.Status != "more" || !page.Gap || page.Cursor == "" {
							t.Fatalf("whole page %+v", page)
						}
					}
					if reads.Load() != 1 {
						t.Fatalf("native reads %d", reads.Load())
					}
				})
			}
			t.Run("malformed-request-never-admits", func(t *testing.T) {
				f := newLocalREPFixture(t, c.protocol)
				s := startLocalREP(t, c, f, make(chan struct{}, 1))
				c.handshake(t, s.client)
				if _, err := s.client.Write(c.malformed); err != nil {
					t.Fatal(err)
				}
				s.joined(t, "malformed request retained owner")
				if _, err := f.lookup(t, mqttSNRequest().RequestID); err == nil {
					t.Fatal("malformed request admitted")
				}
			})
			t.Run("joined-partial-handshake", func(t *testing.T) {
				s := startLocalREP(t, c, newLocalREPFixture(t, c.protocol), make(chan struct{}, 1))
				if _, err := s.client.Read(make([]byte, 1)); err != nil {
					t.Fatal(err)
				}
				go func() { _, _ = io.Copy(io.Discard, s.client) }()
				if _, err := s.client.Write(c.partial); err != nil {
					t.Fatal(err)
				}
				s.cancel()
				s.joined(t, "partial handshake not joined")
			})
			for _, reply := range []bool{false, true} {
				t.Run(map[bool]string{false: "joined-blocked-greeting", true: "joined-blocked-reply"}[reply], func(t *testing.T) {
					f := newLocalREPFixture(t, c.protocol)
					s := startLocalREP(t, c, f, make(chan struct{}, 1))
					if reply {
						c.handshake(t, s.client)
						wire, err := c.request([]byte(f.body(t, mqttSNRequest())))
						if err != nil {
							t.Fatal(err)
						}
						if _, err = s.client.Write(wire); err != nil {
							t.Fatal(err)
						}
						deadline := time.Now().Add(time.Second)
						for len(s.requests) == 0 && time.Now().Before(deadline) {
							time.Sleep(time.Millisecond)
						}
						if len(s.requests) != 1 {
							t.Fatal("request not admitted")
						}
					}
					s.cancel()
					s.joined(t, "blocked owner not joined")
					if len(s.requests) != 0 {
						t.Fatal("request slot leaked")
					}
				})
			}
		})
	}
}
