package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestAwaitReplyKeepsBufferedResponseAfterDisconnect(t *testing.T) {
	for name, reply := range map[string]string{
		"completed": ":server 774 observer :end of directory\r\n",
		"rejected":  ":server 481 observer :permission denied\r\n",
	} {
		t.Run(name, func(t *testing.T) {
			failed := 0
			// Both channels are ready before awaiting. Repetition exposes the
			// old select choosing Done before an already received response.
			for range 24 {
				client := disconnectedReplyClient(t, reply)
				completed := false
				err := awaitReply(t.Context(), client, "lost reply", func(event irc.Event) (bool, error) {
					_, completed = event.(*irc.EndOfDirectoryEvent)
					return completed, nil
				})
				if name == "completed" {
					if err != nil || !completed {
						failed++
					}
				} else {
					var rejected *irc.RejectedError
					if !errors.As(err, &rejected) || rejected.Code != "481" {
						failed++
					}
				}
			}
			if failed != 0 {
				t.Fatalf("lost %d of 24 buffered %s responses", failed, name)
			}
		})
	}
}

// Send a real terminal response, close its TCP carrier, and wait until the
// client has observed closure before handing its buffered Events to the test.
func disconnectedReplyClient(t *testing.T, reply string) *irc.Client {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			finished <- err
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			switch {
			case strings.HasPrefix(scanner.Text(), "USER "):
				if _, err := fmt.Fprintf(conn, ":server 005 observer AIRC=%d :features\r\n:server 001 observer :welcome\r\n", protocol.Level); err != nil {
					finished <- err
					return
				}
			case scanner.Text() == "DIRECTORY":
				_, err := fmt.Fprint(conn, reply)
				finished <- err
				return
			}
		}
		finished <- scanner.Err()
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		if err := <-finished; err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	client, err := irc.DialContext(ctx, irc.Config{Nick: "observer", Addr: listener.Addr().String()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	if err := client.Raw("DIRECTORY"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-client.Done():
	case <-ctx.Done():
		t.Fatal("client did not observe scripted disconnect", ctx.Err())
	}
	return client
}
