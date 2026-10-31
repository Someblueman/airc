//go:build darwin || linux

package gateway_test

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/adapter"
	"github.com/Someblueman/airc/gateway/internal/auth"
	"github.com/Someblueman/airc/gateway/internal/config"
	gwservice "github.com/Someblueman/airc/gateway/internal/service"
	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/internal/server"
	"github.com/Someblueman/airc/internal/testfs"
	"github.com/Someblueman/airc/internal/testserver"
	"github.com/Someblueman/airc/pkg/fallback"
	"github.com/Someblueman/airc/pkg/irc"
	"github.com/coder/websocket"
)

// The client library's follow (pkg/fallback) against this gateway. The
// library's own tests use a fake gateway; these run the real one in
// process, which only this module may link.

// followGateway runs an in-process gateway with a framed and a WebSocket
// listener in front of aircd at upstream, following with a short lease,
// and returns the listeners' endpoints.
func followGateway(t *testing.T, upstream string, lease, keepAlive int) map[string]fallback.Endpoint {
	t.Helper()
	root, err := testfs.MkdirTemp("airc-follow-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	c := config.Defaults()
	c.Version, c.Realm, c.StateDir = 1, "follow", filepath.Join(root, "state")
	if err := os.Mkdir(c.StateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	c.CursorKeyFile = filepath.Join(root, "cursor.key")
	if err := auth.CreateCredential(c.CursorKeyFile); err != nil {
		t.Fatal(err)
	}
	c.Upstream = &config.Upstream{Network: "tcp", Address: upstream}
	c.Limits.FollowSeconds, c.Limits.FollowKeepAliveSeconds = lease, keepAlive
	endpoints := map[string]fallback.Endpoint{}
	for _, name := range []string{"framed", "websocket"} {
		probe, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := probe.Addr().(*net.TCPAddr).Port
		_ = probe.Close()
		c.Listeners = append(c.Listeners, config.Listener{ID: name, Protocol: name, Address: "127.0.0.1:" + strconv.Itoa(port), Enabled: true})
		endpoints[name] = fallback.Endpoint{Carrier: protocol.Carrier{Name: name, Port: port}}
	}
	ctx, cancel := context.WithCancel(context.Background())
	reader, writer := io.Pipe()
	done := make(chan error, 1)
	go func() {
		err := gwservice.Run(ctx, c, writer)
		_ = writer.CloseWithError(io.EOF)
		done <- err
	}()
	if _, err = bufio.NewReader(reader).ReadBytes('\n'); err != nil {
		cancel()
		t.Fatalf("gateway did not start: %v %v", err, <-done)
	}
	go func() { _, _ = io.Copy(io.Discard, reader) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("gateway did not stop")
		}
	})
	return endpoints
}

// A follow on a framed or WebSocket carrier streams a room: the snapshot,
// then each post as it lands, then empty keepalives, until the lease ends
// with nil so the caller resumes from the positions it was given.
func TestFollowStreamsARoomUntilTheLeaseEnds(t *testing.T) {
	t.Parallel()
	addr := testserver.Start(t, server.Config{HistoryLimit: 100}, nil)
	endpoints := followGateway(t, addr, 2, 1)
	poster, err := irc.Dial(irc.Config{Nick: "poster", Addr: addr, Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	defer poster.Close()
	for _, name := range []string{"framed", "websocket"} {
		t.Run(name, func(t *testing.T) {
			c := &fallback.Client{Host: "127.0.0.1", Nick: "follower-" + name, Endpoints: []fallback.Endpoint{endpoints[name]}}
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			reader, err := c.ReadRooms(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			if !reader.Follows() {
				t.Fatal("the carrier does not follow")
			}
			var frames []fallback.Checked
			start := time.Now()
			posted := ""
			err = reader.Follow(ctx, fallback.CheckSet{Rooms: []fallback.RoomAfter{{Room: "#" + name}}, Limit: 10}, 5*time.Second, func(checked fallback.Checked) error {
				frames = append(frames, checked)
				if len(frames) == 1 {
					if err := poster.Send("#"+name, "live on "+name); err != nil {
						return err
					}
				}
				if n := len(checked.Rooms[0].Messages); n > 0 {
					posted = checked.Rooms[0].Messages[n-1].ID
				}
				return nil
			})
			if err != nil {
				t.Fatalf("follow ended with %v after %d frames", err, len(frames))
			}
			if elapsed := time.Since(start); elapsed < 1500*time.Millisecond || elapsed > 8*time.Second {
				t.Fatalf("lease ended after %v", elapsed)
			}
			if len(frames) < 3 || posted == "" {
				t.Fatalf("frames %d, posted %q", len(frames), posted)
			}
			// The snapshot was empty, the post arrived as its own frame, and
			// every later frame carries the post's position.
			if len(frames[0].Rooms[0].Messages) != 0 || frames[len(frames)-1].Rooms[0].Cursor != posted {
				t.Fatalf("first %+v last %+v", frames[0].Rooms[0], frames[len(frames)-1].Rooms[0])
			}
			// Resuming from that position on the same connection reads nothing twice.
			var resumed []fallback.Checked
			stop := errors.New("enough")
			err = reader.Follow(ctx, fallback.CheckSet{Rooms: []fallback.RoomAfter{{Room: "#" + name, After: posted}}, Limit: 10}, 5*time.Second, func(checked fallback.Checked) error {
				resumed = append(resumed, checked)
				return stop
			})
			if !errors.Is(err, stop) || len(resumed) != 1 || len(resumed[0].Rooms[0].Messages) != 0 {
				t.Fatalf("resume: %v %+v", err, resumed)
			}
		})
	}
}

// A client that only reads its follow sends the gateway nothing for the
// whole lease; the connection must not be dropped for that silence. The
// test shortens adapter.ConnectionIdle, which every in-process gateway in
// the package reads, so it does not run in parallel.
func TestFollowOutlivesTheConnectionIdle(t *testing.T) {
	released := adapter.ConnectionIdle
	adapter.ConnectionIdle = 500 * time.Millisecond
	t.Cleanup(func() { adapter.ConnectionIdle = released })
	addr := testserver.Start(t, server.Config{HistoryLimit: 100}, nil)
	endpoints := followGateway(t, addr, 3, 1)
	for _, name := range []string{"framed", "websocket"} {
		t.Run(name, func(t *testing.T) {
			c := &fallback.Client{Host: "127.0.0.1", Nick: "quiet-" + name, Endpoints: []fallback.Endpoint{endpoints[name]}}
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			reader, err := c.ReadRooms(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			frames := 0
			start := time.Now()
			err = reader.Follow(ctx, fallback.CheckSet{Rooms: []fallback.RoomAfter{{Room: "#quiet"}}, Limit: 10}, 5*time.Second, func(fallback.Checked) error {
				frames++
				return nil
			})
			if err != nil {
				t.Fatalf("follow ended with %v after %v and %d frames", err, time.Since(start), frames)
			}
			if elapsed := time.Since(start); elapsed < 2500*time.Millisecond {
				t.Fatalf("the follow lasted %v, not its lease", elapsed)
			}
			// The connection is still usable after the lease.
			if _, err := reader.Check(ctx, []fallback.RoomAfter{{Room: "#quiet"}}, 10, 0); err != nil {
				t.Fatalf("after the follow: %v", err)
			}
		})
	}
}

// A connection whose requests are done waits the connection idle, not the
// hour a stream gets: the read already waiting when its last request ends
// moves back to the idle timeout. Shortening adapter.ConnectionIdle keeps
// the test serial, as above.
func TestConnectionIdleAfterARequestEnds(t *testing.T) {
	released := adapter.ConnectionIdle
	adapter.ConnectionIdle = 500 * time.Millisecond
	t.Cleanup(func() { adapter.ConnectionIdle = released })
	addr := testserver.Start(t, server.Config{HistoryLimit: 100}, nil)
	endpoints := followGateway(t, addr, 3, 1)
	frame := []byte(`{"version":1,"correlation_id":"c1","profile":"plain","request":{"version":1,"request_id":"r1","operation":"capabilities.get"}}`)

	t.Run("framed", func(t *testing.T) {
		conn, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(endpoints["framed"].Port))
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		if _, err := conn.Write(binary.BigEndian.AppendUint32(nil, uint32(len(frame)))); err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Write(frame); err != nil {
			t.Fatal(err)
		}
		answered := time.Time{}
		var header [4]byte
		for {
			if _, err := io.ReadFull(conn, header[:]); err != nil {
				if answered.IsZero() {
					t.Fatal("closed before answering", err)
				}
				break
			}
			if _, err := io.CopyN(io.Discard, conn, int64(binary.BigEndian.Uint32(header[:]))); err != nil {
				t.Fatal(err)
			}
			answered = time.Now()
		}
		if waited := time.Since(answered); waited > 3*time.Second {
			t.Fatal("idle connection held for", waited)
		}
	})

	t.Run("websocket", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		conn, _, err := websocket.Dial(ctx, "ws://127.0.0.1:"+strconv.Itoa(endpoints["websocket"].Port)+"/v1/socket", &websocket.DialOptions{Subprotocols: []string{adapter.WebSocketSubprotocol}})
		if err != nil {
			t.Fatal(err)
		}
		defer conn.CloseNow()
		if err := conn.Write(ctx, websocket.MessageText, frame); err != nil {
			t.Fatal(err)
		}
		if _, _, err := conn.Read(ctx); err != nil {
			t.Fatal("no answer", err)
		}
		answered := time.Now()
		if _, _, err := conn.Read(ctx); err == nil || errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("idle socket still open", err)
		}
		if waited := time.Since(answered); waited > 3*time.Second {
			t.Fatal("idle socket held for", waited)
		}
	})
}
