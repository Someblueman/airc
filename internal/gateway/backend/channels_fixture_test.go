package backend

import (
	"bufio"
	"context"
	"fmt"
	"github.com/Someblueman/airc/pkg/irc"
	"net"
	"strings"
	"testing"
	"time"
)

func channelsFixture(t *testing.T, lines []string, continuous bool) (*irc.Client, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "USER ") {
				_, _ = fmt.Fprint(conn, ":server 001 reader :welcome\r\n")
			}
			if line != "CHANNELS" {
				continue
			}
			for _, response := range lines {
				if _, err := fmt.Fprint(conn, response); err != nil {
					return
				}
			}
			if continuous {
				for {
					if _, err := fmt.Fprint(conn, ":server NOTICE reader :noise\r\n"); err != nil {
						return
					}
					time.Sleep(time.Millisecond)
				}
			}
			return
		}
	}()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	client, err := irc.DialContext(ctx, irc.Config{Nick: "reader", Addr: listener.Addr().String(), ReadTimeout: time.Second, WriteTimeout: time.Second})
	stop := func() {
		if client != nil {
			_ = client.Close()
		}
		_ = listener.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("channels fixture did not join")
		}
	}
	if err != nil {
		stop()
		t.Fatal(err)
	}
	return client, stop
}
