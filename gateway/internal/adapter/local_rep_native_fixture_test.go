//go:build darwin || linux

package adapter

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/config"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/pkg/irc"
)

// newLocalREPFixture is an NNG or ZeroMQ listener's dispatcher; requests are
// plain contract JSON naming the fixture's guest author.
func newLocalREPFixture(t *testing.T, protocol string) plainFixture {
	t.Helper()
	return newPlainFixture(t, config.Listener{ID: protocol, Protocol: protocol, Operations: []core.Operation{core.Send, core.ContextRead, core.HistoryRead, core.Search}})
}

// localREPNativeHistory serves the fixture's upstream: scripted native
// protocol output exercises the real bounded SDK collector for the guest
// author's ephemeral read session.
func localREPNativeHistory(t *testing.T, f plainFixture, target string, messages []irc.MessageMetadata, features string) *atomic.Int32 {
	t.Helper()
	listener, err := net.Listen("unix", f.c.Upstream.Address)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	reads := new(atomic.Int32)
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			func() {
				defer conn.Close()
				closed := make(chan struct{})
				stop := context.AfterFunc(ctx, func() { _ = conn.Close(); close(closed) })
				defer func() {
					if !stop() {
						<-closed
					}
				}()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				scanner := bufio.NewScanner(conn)
				ephemeral := false
				for scanner.Scan() {
					line := scanner.Text()
					switch {
					case strings.HasPrefix(line, "AUTHENTICATE") || strings.HasPrefix(line, "CAP REQ"):
						t.Error("guest read authenticated")
						return
					case line == "EPHEMERAL":
						ephemeral = true
					case strings.HasPrefix(line, "USER "):
						if !ephemeral {
							t.Error("native read was not ephemeral")
							return
						}
						_, _ = fmt.Fprintf(conn, ":server 766 author :ephemeral\r\n:server 005 author %s :features\r\n:server 001 author :welcome\r\n", features)
					case strings.HasPrefix(line, "CHECK :"):
						reads.Add(1)
						if len(messages) == 0 {
							t.Error("unexpected native read")
							return
						}
						var request irc.CheckRequest
						if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "CHECK :")), &request); err != nil || len(request.Targets) != 1 || request.Targets[0].Target != target {
							t.Error("wrong bounded native selector", err)
							return
						}
						for i := range messages {
							entry := irc.CheckEntry{Kind: "message", Target: messages[i].Target, Targets: []int{0}, Message: &messages[i]}
							data, _ := json.Marshal(entry)
							_, _ = fmt.Fprintf(conn, ":server 784 author :%s\r\n", data)
						}
						marker := irc.CheckEntry{Kind: "page", Target: target, Status: "more", Cursor: messages[len(messages)-1].ID, Gap: true}
						data, _ := json.Marshal(marker)
						_, _ = fmt.Fprintf(conn, ":server 784 author :%s\r\n:server 785 author :end\r\n", data)
					}
				}
			}()
		}
	}()
	t.Cleanup(func() { cancel(); _ = listener.Close(); <-done })
	return reads
}
