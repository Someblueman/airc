package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/Someblueman/airc/pkg/irc"
)

func TestBlockedHistoryAppendAllowsQueriesButWithholdsSendReceipt(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	// Fill a real pipe without blocking; it acts as a stalled history device.
	total := 0
	raw, err := writer.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	if err := raw.Control(func(fd uintptr) {
		for _, block := range [][]byte{make([]byte, 4096), make([]byte, 1)} {
			for {
				n, writeErr := syscall.Write(int(fd), block)
				if n > 0 {
					total += n
				}
				if errors.Is(writeErr, syscall.EAGAIN) {
					break
				}
				if writeErr != nil {
					err = writeErr
					return
				}
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := New(Config{HistoryLimit: 16, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	srv.histFile = writer
	served := make(chan error, 1)
	go func() { served <- srv.Serve(listener) }()
	defer func() {
		writer.Close()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			t.Error(err)
		}
		<-served
	}()
	sender, err := irc.Dial(irc.Config{Nick: "writer", Addr: listener.Addr().String(), Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	if err := sender.Send("#room", "committed"); err != nil {
		t.Fatal(err)
	}
	// Registration and a history query must complete while the append is stuck.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	query, err := irc.DialContext(ctx, irc.Config{Nick: "reader", Addr: listener.Addr().String(), Ephemeral: true})
	if err != nil {
		t.Fatalf("history I/O blocked registration: %v", err)
	}
	defer query.Close()
	found := false
	for !found {
		if err := query.History("#room", 1); err != nil {
			t.Fatal(err)
		}
		for {
			select {
			case event := <-query.Events():
				if _, ok := event.(*irc.HistoryEvent); ok {
					found = true
				}
				if _, ok := event.(*irc.EndOfHistoryEvent); ok {
					goto nextQuery
				}
			case <-ctx.Done():
				t.Fatal("history I/O blocked a query")
			}
		}
	nextQuery:
	}
	for len(sender.Events()) > 0 {
		if _, ok := (<-sender.Events()).(*irc.SendReceiptEvent); ok {
			t.Fatal("send was confirmed before its history append")
		}
	}
	drained := make(chan Message, 1)
	go func() {
		io.CopyN(io.Discard, reader, int64(total))
		line, _ := bufio.NewReader(reader).ReadBytes('\n')
		var message Message
		json.Unmarshal(line, &message)
		drained <- message
	}()
	for {
		select {
		case event := <-sender.Events():
			if receipt, ok := event.(*irc.SendReceiptEvent); ok {
				if receipt.Message != "committed" {
					t.Fatal("wrong send receipt")
				}
				var message Message
				select {
				case message = <-drained:
				case <-ctx.Done():
					t.Fatal("history record was not readable after the send receipt")
				}
				if message.Body != "committed" || message.ID != receipt.ID {
					t.Fatal("receipt did not match the appended record")
				}
				return
			}
		case <-ctx.Done():
			t.Fatal("send did not resume after history I/O recovered")
		}
	}
}
