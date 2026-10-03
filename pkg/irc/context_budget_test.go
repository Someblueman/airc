package irc_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/pkg/irc"
)

func TestContextBudgetCapabilityAndLegacyAPI(t *testing.T) {
	for _, capable := range []bool{false, true} {
		t.Run(fmt.Sprint(capable), func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			requests := make(chan irc.ChatRequest, 3)
			done := make(chan struct{})
			id := strings.Repeat("a", 32)
			go func() {
				defer close(done)
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(5 * time.Second))
				scanner := bufio.NewScanner(conn)
				for scanner.Scan() {
					line := scanner.Text()
					if strings.HasPrefix(line, "USER ") {
						features := "CHAT=1 CONTEXT=1"
						if capable {
							features += " CONTEXT_BYTES=1"
						}
						fmt.Fprintf(conn, ":server 005 reader %s :features\r\n:server 001 reader :welcome\r\n", features)
					}
					if strings.HasPrefix(line, "CHAT :") {
						var r irc.ChatRequest
						if json.Unmarshal([]byte(strings.TrimPrefix(line, "CHAT :")), &r) != nil {
							return
						}
						requests <- r
						fmt.Fprintf(conn, ":server 777 reader :{\"action\":\"context\",\"context\":{\"trigger_id\":%q,\"root_id\":%q}}\r\n:server 778 reader :End of chat response\r\n", id, id)
					}
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			c, err := irc.DialContext(ctx, irc.Config{Nick: "reader", Addr: listener.Addr().String()})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			for _, budget := range []int{-1, 1, (1 << 20) + 1} {
				if _, err := irc.RequestContextWithOptions(ctx, c, id, irc.ContextOptions{Limit: 50, MaxBytes: budget}, nil); err == nil {
					t.Fatal("invalid budget accepted")
				}
			}
			_, err = irc.RequestContextWithOptions(ctx, c, id, irc.ContextOptions{Limit: 50, MaxBytes: 2048}, nil)
			if capable {
				if err != nil {
					t.Fatal(err)
				}
				if r := <-requests; r.MaxBytes != 2048 {
					t.Fatal("wire budget not sent", r)
				}
			} else if err == nil || !strings.Contains(err.Error(), "CONTEXT_BYTES") {
				t.Fatal("old daemon accepted bounded request", err)
			}
			// The published legacy API still works without sending the new field.
			result, err := irc.RequestContext(ctx, c, id, 50)
			if err != nil || result.RootID != id {
				t.Fatal(result, err)
			}
			if r := <-requests; r.MaxBytes != 0 || len(requests) != 0 {
				t.Fatal("legacy API changed or rejected call sent data", r)
			}
			c.Close()
			select {
			case <-done:
			case <-ctx.Done():
				t.Fatal("peer did not stop")
			}
		})
	}
}
