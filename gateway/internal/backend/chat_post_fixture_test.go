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

func chatPostWireFixture(t *testing.T, wire string) (*irc.Client, <-chan string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	writes := make(chan string, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "USER ") {
				_, _ = fmt.Fprintf(conn, ":server 005 reader AIRC=%d :features\r\n:server 001 reader :welcome\r\n", protocol.Level)
			}
			if strings.Contains(line, `"action":"action"`) {
				writes <- line
				_, _ = fmt.Fprint(conn, wire)
				return
			}
		}
	}()
	client, err := irc.DialContext(context.Background(), irc.Config{Nick: "reader", Addr: listener.Addr().String(), ReadTimeout: time.Second, WriteTimeout: time.Second})
	if err != nil {
		_ = listener.Close()
		t.Fatal(err)
	}
	return client, writes, func() {
		_ = client.Close()
		_ = listener.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("CHAT fixture did not join")
		}
	}
}

func chatPostWire(t *testing.T, meta protocol.MessageMetadata) (string, string, string) {
	t.Helper()
	receiptMeta := meta
	receiptMeta.Receipt = &protocol.ReceiptInfo{Accepted: true, Persisted: true}
	receipt := protocol.Format("server", "762", []string{"reader"}, protocol.EncodeMessageMetadata(receiptMeta))
	entry := irc.ChatEntry{Action: "action", Target: meta.Target, ID: meta.ID, Message: &meta}
	return receipt, chatLine(t, entry), ":server 778 reader :End of chat response\r\n"
}
