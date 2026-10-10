//go:build unix

package auth

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func TestMatrixVerifiedJoinedRooms(t *testing.T) {
	for _, mode := range []string{"valid", "missing", "case", "alias", "duplicate", "null", "256", "257", "wrong_account"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			s := matrixHTTPFixture(t, func(w http.ResponseWriter, r *http.Request) {
				n := calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				if r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Bearer matrix-secret" {
					t.Error("wrong request authority")
				}
				if n == 1 {
					if r.URL.Path != "/_matrix/client/v3/account/whoami" {
						t.Error("account not first")
					}
					body := `{"user_id":"@bridge:localhost"}`
					if mode == "wrong_account" {
						body = `{"user_id":"@other:localhost"}`
					}
					_, _ = io.WriteString(w, body)
					return
				}
				if r.URL.Path != "/_matrix/client/v3/joined_rooms" {
					t.Error("wrong room path")
				}
				rooms := []string{"!room"}
				switch mode {
				case "missing":
					rooms = []string{}
				case "case":
					rooms = []string{"!Room"}
				case "alias":
					rooms = []string{"#room:localhost"}
				case "duplicate":
					rooms = append(rooms, "!room")
				case "null":
					_, _ = io.WriteString(w, `{"joined_rooms":null}`)
					return
				case "256", "257":
					n, _ := strconv.Atoi(mode)
					for i := 1; i < n; i++ {
						rooms = append(rooms, "!extra"+strconv.Itoa(i))
					}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"joined_rooms": rooms})
			})
			err := s.VerifyRooms(context.Background())
			if (err == nil) != (mode == "valid" || mode == "256") {
				t.Fatal(mode, err)
			}
			want := int32(2)
			if mode == "wrong_account" {
				want = 1
			}
			if calls.Load() != want {
				t.Fatal("unexpected requests", calls.Load())
			}
		})
	}
}

func TestMatrixAccountAndRoomsShareAbsoluteDeadline(t *testing.T) {
	var calls atomic.Int32
	s := matrixHTTPFixture(t, func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		select {
		case <-time.After(1800 * time.Millisecond):
		case <-r.Context().Done():
			return
		}
		if n == 1 {
			_, _ = io.WriteString(w, `{"user_id":"@bridge:localhost"}`)
		} else {
			_, _ = io.WriteString(w, `{"joined_rooms":["!room"]}`)
		}
	})
	start := time.Now()
	if s.VerifyRooms(context.Background()) == nil {
		t.Fatal("combined expired verification accepted")
	}
	if elapsed := time.Since(start); elapsed < 2800*time.Millisecond || elapsed > 3400*time.Millisecond {
		t.Fatal("deadline renewed", elapsed)
	}
	if calls.Load() != 2 {
		t.Fatal("room verification not reached")
	}
}
