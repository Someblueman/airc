package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/server"
)

// Drop a real server's first receipt, then permit the receipt-only reconnect.
func dropFirstReceipt(t *testing.T, address string) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for connection := 0; ; connection++ {
			front, err := l.Accept()
			if err != nil {
				return
			}
			back, err := net.Dial("tcp", address)
			if err != nil {
				front.Close()
				return
			}
			go func(drop bool) {
				defer front.Close()
				defer back.Close()
				done := make(chan struct{})
				go func() { io.Copy(back, front); back.Close(); close(done) }()
				scanner := bufio.NewScanner(back)
				for scanner.Scan() {
					line := scanner.Text()
					if drop && strings.Contains(line, " 762 ") {
						break
					}
					if _, err := io.WriteString(front, line+"\r\n"); err != nil {
						break
					}
				}
				front.Close()
				back.Close()
				<-done
			}(connection == 0)
		}
	}()
	return l.Addr().String()
}

func TestSendRecoversLostReceiptWithoutDuplicate(t *testing.T) {
	agentEnv(t)
	address := cliTestServer(t)
	proxy := dropFirstReceipt(t, address)
	out := mustCLI(t, proxy, "send", "--nick", "writer", "--channel", "room", "--message", "once", "--json")
	var receipt sendResult
	if err := json.Unmarshal([]byte(out), &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.RequestID == "" || receipt.Receipt == nil || !receipt.Receipt.Accepted || receipt.Receipt.Persisted {
		t.Fatal(out)
	}
	if got := historyIDs(t, mustCLI(t, address, "history", "#room", "--json")); len(got) != 1 || got[0] != receipt.ID {
		t.Fatal(got)
	}
	if cached := mustCLI(t, proxy, "send", "--nick", "writer", "--retry", receipt.RequestID, "--json"); cached != out {
		t.Fatalf("receipt changed: %s", cached)
	}
}

func TestConfirmedReceiptSurvivesOutputFailureAndHistoryEviction(t *testing.T) {
	agentEnv(t)
	address := cliTestServerWith(t, server.Config{HistoryLimit: 1})
	err := run([]string{"send", "--addr", address, "--nick", "writer", "--channel", "room", "--message", "once", "--request-id", "durable-request", "--json"}, strings.NewReader(""), brokenOutput{}, io.Discard)
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal(err)
	}
	send(t, address, "other", "#room", "evict")
	out := mustCLI(t, address, "send", "--nick", "writer", "--retry", "durable-request", "--json")
	var receipt sendResult
	if err := json.Unmarshal([]byte(out), &receipt); err != nil || receipt.Message != "once" {
		t.Fatal(out, err)
	}
	if got := historyIDs(t, mustCLI(t, address, "history", "#room", "--json")); len(got) != 1 || got[0] == receipt.ID {
		t.Fatal(got)
	}
}

func TestUnknownRetryNeverPostsAndStaysRecoverable(t *testing.T) {
	agentEnv(t)
	address := cliTestServer(t)
	box, err := openOutbox(options{addr: address, nick: "writer"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := box.add("#room", "", "uncertain", "missing-request"); err != nil {
		t.Fatal(err)
	}
	box.close()
	out, stderr, err := cli(t, address, "send", "--nick", "writer", "--retry", "missing-request", "--json")
	if err == nil || out != "" {
		t.Fatal(out, err)
	}
	var result commandFailure
	if err := json.Unmarshal([]byte(stderr), &result); err != nil {
		t.Fatal(err)
	}
	if result.Code != "delivery_unknown" || result.Retryable || result.RequestID != "missing-request" {
		t.Fatal(stderr)
	}
	if got := mustCLI(t, address, "history", "#room", "--json"); got != "" {
		t.Fatal("retry posted a message", got)
	}
	if pending := mustCLI(t, address, "send", "--nick", "writer", "--pending", "--json"); !strings.Contains(pending, "missing-request") {
		t.Fatal(pending)
	}
	mustCLI(t, address, "send", "--nick", "writer", "--forget", "missing-request")
	if pending := mustCLI(t, address, "send", "--nick", "writer", "--pending", "--json"); strings.TrimSpace(pending) != "[]" {
		t.Fatal(pending)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := recoverSend(ctx, options{addr: address, nick: "writer"}, &outboundMessage{RequestID: "missing-request"}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
