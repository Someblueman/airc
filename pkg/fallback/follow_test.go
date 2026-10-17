package fallback

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	gwconfig "github.com/Someblueman/airc/internal/gateway/config"
	gwservice "github.com/Someblueman/airc/internal/gateway/service"
	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/internal/server"
	"github.com/Someblueman/airc/internal/testfs"
	"github.com/Someblueman/airc/internal/testserver"
	"github.com/Someblueman/airc/pkg/irc"
)

// followGateway runs an in-process gateway with a framed and a WebSocket
// listener in front of aircd at upstream, following with a short lease,
// and returns the listeners' endpoints.
func followGateway(t *testing.T, upstream string, lease, keepAlive int) map[string]Endpoint {
	t.Helper()
	root, err := testfs.MkdirTemp("airc-follow-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	c := gwconfig.Defaults()
	c.Version, c.Realm, c.StateDir = 1, "follow", filepath.Join(root, "state")
	if err := os.Mkdir(c.StateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	c.CursorKeyFile = filepath.Join(root, "cursor.key")
	if err := auth.CreateCredential(c.CursorKeyFile); err != nil {
		t.Fatal(err)
	}
	c.Upstream = &gwconfig.Upstream{Network: "tcp", Address: upstream}
	c.Limits.FollowSeconds, c.Limits.FollowKeepAliveSeconds = lease, keepAlive
	endpoints := map[string]Endpoint{}
	for _, name := range []string{"framed", "websocket"} {
		probe, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := probe.Addr().(*net.TCPAddr).Port
		_ = probe.Close()
		c.Listeners = append(c.Listeners, gwconfig.Listener{ID: name, Protocol: name, Address: "127.0.0.1:" + strconv.Itoa(port), Enabled: true})
		endpoints[name] = Endpoint{Carrier: protocol.Carrier{Name: name, Port: port}}
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
	addr := testserver.Start(t, server.Config{HistoryLimit: 100}, nil)
	endpoints := followGateway(t, addr, 2, 1)
	poster, err := irc.Dial(irc.Config{Nick: "poster", Addr: addr, Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	defer poster.Close()
	for _, name := range []string{"framed", "websocket"} {
		t.Run(name, func(t *testing.T) {
			c := &Client{Host: "127.0.0.1", Nick: "follower-" + name, Endpoints: []Endpoint{endpoints[name]}}
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
			var frames []Checked
			start := time.Now()
			posted := ""
			err = reader.Follow(ctx, CheckSet{Rooms: []RoomAfter{{Room: "#" + name}}, Limit: 10}, 5*time.Second, func(checked Checked) error {
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
			var resumed []Checked
			stop := errors.New("enough")
			err = reader.Follow(ctx, CheckSet{Rooms: []RoomAfter{{Room: "#" + name, After: posted}}, Limit: 10}, 5*time.Second, func(checked Checked) error {
				resumed = append(resumed, checked)
				return stop
			})
			if !errors.Is(err, stop) || len(resumed) != 1 || len(resumed[0].Rooms[0].Messages) != 0 {
				t.Fatalf("resume: %v %+v", err, resumed)
			}
		})
	}
}

// A one-shot HTTP carrier has no stream: Follow says so at once, so a
// caller polls with CheckAll instead.
func TestFollowIsRefusedOnARequestCarrier(t *testing.T) {
	g := newFakeGateway(t)
	c := &Client{Host: "127.0.0.1", Nick: "alice", Endpoints: []Endpoint{g.endpoint(t)}}
	reader, err := c.ReadRooms(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if reader.Follows() {
		t.Fatal("an HTTP carrier claims to follow")
	}
	if err := reader.Follow(t.Context(), CheckSet{Rooms: []RoomAfter{{Room: "#room"}}, Limit: 10}, 0, func(Checked) error { return nil }); !errors.Is(err, ErrNoFollow) {
		t.Fatalf("Follow on HTTP: %v", err)
	}
}
