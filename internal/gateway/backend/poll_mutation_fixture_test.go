package backend

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

func pollMutationFixture(t *testing.T, target string, result irc.ChatEntry, receipt, mutate bool, preflightRaw, mutationTail string, mutationResult ...*irc.ChatEntry) (*irc.Client, <-chan string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	commands := make(chan string, 4)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer close(commands)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "USER ") {
				_, _ = fmt.Fprint(conn, ":server 005 reader CHAT=1 :features\r\n:server 001 reader :welcome\r\n")
				continue
			}
			if strings.Contains(line, `"action":"results"`) {
				if preflightRaw == "hold" {
					continue
				}
				if preflightRaw != "" {
					_, _ = fmt.Fprint(conn, preflightRaw)
					continue
				}
				_, _ = fmt.Fprint(conn, chatLine(t, result), ":server 778 reader :End of chat response\r\n")
				continue
			}
			if strings.Contains(line, `"action":"poll"`) || strings.Contains(line, `"action":"vote"`) || strings.Contains(line, `"action":"close-poll"`) {
				commands <- line
				if receipt {
					meta := protocol.MessageMetadata{Receipt: &protocol.ReceiptInfo{Accepted: true, Persisted: true}, ChatMetadata: protocol.ChatMetadata{AccountID: result.AccountID, Kind: "poll", PollOptions: result.Options, PollClosesAt: result.ExpiresAt}, ID: result.ID, From: result.From, Target: result.Target, Message: result.Text, Timestamp: time.Now().UTC()}
					_, _ = fmt.Fprintf(conn, ":server 762 reader :%s\r\n", protocol.EncodeMessageMetadata(meta))
				}
				if mutate {
					response := result
					if len(mutationResult) != 0 && mutationResult[0] != nil {
						response = *mutationResult[0]
					}
					_, _ = fmt.Fprint(conn, chatLine(t, response))
					if mutationTail == "" {
						mutationTail = ":server 778 reader :End of chat response\r\n"
					}
				}
				_, _ = fmt.Fprint(conn, mutationTail)
				return
			}
		}
	}()
	client, err := irc.DialContext(context.Background(), irc.Config{Nick: "reader", Addr: listener.Addr().String(), ReadTimeout: time.Second, WriteTimeout: time.Second})
	if err != nil {
		_ = listener.Close()
		t.Fatal(err)
	}
	return client, commands, func() {
		_ = client.Close()
		_ = listener.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("poll fixture did not stop")
		}
	}
}
