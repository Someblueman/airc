package irc_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/server"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestConcurrentPostsAppendInReceiptSequence(t *testing.T) {
	const writers, perWriter = 6, 20
	path := filepath.Join(t.TempDir(), "history.jsonl")
	address := startConfigured(t, server.Config{HistoryLimit: writers * perWriter}, path)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	receipts := make(chan *irc.SendReceiptEvent, writers*perWriter)
	errors := make(chan error, writers)
	var done sync.WaitGroup
	for writer := range writers {
		done.Go(func() {
			client, err := irc.DialContext(ctx, irc.Config{Nick: fmt.Sprintf("writer%d", writer), Addr: address, Ephemeral: true})
			if err != nil {
				errors <- err
				return
			}
			defer client.Close()
			for i := range perWriter {
				if err := client.Send("#room", fmt.Sprintf("writer%d post%d", writer, i)); err != nil {
					errors <- err
					return
				}
				for {
					select {
					case event, ok := <-client.Events():
						if !ok {
							errors <- fmt.Errorf("writer%d disconnected before its receipt", writer)
							return
						}
						if receipt, ok := event.(*irc.SendReceiptEvent); ok {
							receipts <- receipt
							goto nextPost
						}
					case <-ctx.Done():
						errors <- ctx.Err()
						return
					}
				}
			nextPost:
			}
		})
	}
	done.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
	if t.Failed() {
		return
	}
	close(receipts)
	confirmed := make(map[string]*irc.SendReceiptEvent, writers*perWriter)
	for receipt := range receipts {
		confirmed[receipt.ID] = receipt
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != writers*perWriter || len(confirmed) != len(lines) {
		t.Fatalf("appends=%d, distinct receipts=%d, want %d", len(lines), len(confirmed), writers*perWriter)
	}
	for i, line := range lines {
		var message server.Message
		if err := json.Unmarshal([]byte(line), &message); err != nil {
			t.Fatal(err)
		}
		receipt := confirmed[message.ID]
		if message.Seq != uint64(i+1) || receipt == nil || receipt.Message != message.Body || receipt.Seq != message.Seq {
			t.Fatalf("append %d does not match its ordered receipt: %+v", i, message)
		}
	}
}
