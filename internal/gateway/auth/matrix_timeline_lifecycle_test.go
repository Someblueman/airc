//go:build unix

package auth

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"
)

func matrixSyncPreamble(w http.ResponseWriter, r *http.Request) bool {
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/_matrix/client/v3/account/whoami":
		_, _ = io.WriteString(w, `{"user_id":"@bridge:localhost"}`)
		return true
	case "/_matrix/client/v3/joined_rooms":
		_, _ = io.WriteString(w, `{"joined_rooms":["!room"]}`)
		return true
	}
	return false
}

func TestMatrixInitialAndIdleTimelineAreNotAdmission(t *testing.T) {
	for _, since := range []string{"", "cursor"} {
		t.Run("since="+since, func(t *testing.T) {
			s := matrixHTTPFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if matrixSyncPreamble(w, r) {
					return
				}
				_, _ = io.WriteString(w, `{"next_batch":"cursor"}`)
			})
			p, err := s.ReadTimeline(context.Background(), since)
			if err != nil || !p.Valid() || p.Initial() != (since == "") || len(p.Events()) != 0 {
				t.Fatal(p, err)
			}
		})
	}
}

func TestMatrixSyncCancellationCannotMintPage(t *testing.T) {
	entered := make(chan struct{})
	done := make(chan struct{})
	s := matrixHTTPFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if matrixSyncPreamble(w, r) {
			return
		}
		close(entered)
		<-r.Context().Done()
		close(done)
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		p, err := s.ReadTimeline(ctx, "cursor")
		if p.Valid() {
			err = nil
		}
		result <- err
	}()
	<-entered
	cancel()
	if err := <-result; err == nil {
		t.Fatal("cancelled sync granted page")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("sync request did not join")
	}
}
