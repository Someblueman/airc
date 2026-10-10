//go:build darwin || linux

package adapter

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/pkg/irc"
)

// Scripted native protocol output exercises the real bounded SDK collector.
func localREPNativeHistory(t *testing.T, f amqpAdmissionFixture, target string, messages []irc.MessageMetadata, features string) *atomic.Int32 {
	t.Helper()
	listener, err := net.Listen("unix", f.c.Bindings[0].Address)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := auth.ReadToken(f.c.Bindings[0].AccountTokenFile)
	if err != nil {
		t.Fatal(err)
	}
	expected := base64.StdEncoding.EncodeToString([]byte("\x00" + f.c.Bindings[0].Account + "\x00" + credential))
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
					case line == "CAP REQ :sasl":
						_, _ = fmt.Fprint(conn, ":server CAP author ACK :sasl\r\n")
					case line == "AUTHENTICATE PLAIN":
						_, _ = fmt.Fprint(conn, "AUTHENTICATE +\r\n")
					case strings.HasPrefix(line, "AUTHENTICATE "):
						if line != "AUTHENTICATE "+expected {
							t.Error("wrong native author credential")
							return
						}
						_, _ = fmt.Fprint(conn, ":server 903 author :authenticated\r\n")
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
