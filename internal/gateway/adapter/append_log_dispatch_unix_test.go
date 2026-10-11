//go:build darwin || linux

package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestAppendLogMailboxIntakeAndReplay(t *testing.T) {
	f, body, _ := oscoreRPCFixture(t)
	path, m := appendMailboxFixture(t, string(body)+"\n")
	var upstream string
	for i := 0; i < 2; i++ {
		if i == 1 {
			file, err := os.OpenFile(filepath.Join(path, "logs", "00000000000000000001.jsonl"), os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				t.Fatal(err)
			}
			_, err = file.Write(append(body, '\n'))
			if err != nil {
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
		}
		if err := ProcessAppendLogMailbox(t.Context(), m, f.d, nil); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(path, "response.json"))
		if err != nil {
			t.Fatal(err)
		}
		var response FrameResponse
		if err := json.Unmarshal(data, &response); err != nil || response.Error != nil || response.CorrelationID != "native-rpc" || response.Result == nil || response.Result.Result == nil || !response.Result.Result.Durable {
			t.Fatal("append-log intake failed", err, response)
		}
		job, err := f.lookup(t, "oscore-rpc-message")
		if err != nil || job.Request.Message != "first\nsecond" || job.Result.SendAttempts != 0 {
			t.Fatal("wrong durable append-log intake", job, err)
		}
		if i == 0 {
			upstream = job.UpstreamID
		} else if upstream == "" || upstream != job.UpstreamID {
			t.Fatal("append-log replay changed upstream identity")
		}
		if err := os.Remove(filepath.Join(path, "response.json")); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAppendLogMailboxRefusalsPrecedeIntake(t *testing.T) {
	for _, kind := range []string{"request", "stream", "profile", "version", "correlation", "malformed"} {
		t.Run(kind, func(t *testing.T) {
			f, body, _ := oscoreRPCFixture(t)
			var request map[string]any
			if err := json.Unmarshal(body, &request); err != nil {
				t.Fatal(err)
			}
			code := core.InvalidRequest
			switch kind {
			case "request":
				request["request"] = map[string]any{"version": 1}
			case "stream":
				request["stream"] = true
			case "version":
				request["version"] = 2
			case "correlation":
				request["correlation_id"] = ""
			case "malformed":
			case "profile":
				request["profile"] = "authorSpec"
			}
			body, err := json.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "malformed" {
				body = []byte("{")
			}
			path, m := appendMailboxFixture(t, string(body)+"\n")
			if err := ProcessAppendLogMailbox(t.Context(), m, f.d, nil); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(path, "response.json"))
			var response FrameResponse
			if err != nil || json.Unmarshal(data, &response) != nil || response.Error == nil || response.Error.Code != code {
				t.Fatal("wrong append-log refusal", err, response)
			}
			if _, err := f.lookup(t, "oscore-rpc-message"); !errors.Is(err, store.ErrNotFound) {
				t.Fatal("append-log refusal reached durable intake", err)
			}
		})
	}
}

func TestAppendLogMailboxCancellationRetainsClaim(t *testing.T) {
	f, body, _ := oscoreRPCFixture(t)
	path, m := appendMailboxFixture(t, string(body)+"\n")
	if _, err := m.Claim(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := ProcessAppendLogMailbox(ctx, m, f.d, nil); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation hidden", err)
	}
	if _, err := os.Stat(filepath.Join(path, "claim.json")); err != nil {
		t.Fatal("cancellation lost claim", err)
	}
	if _, err := os.Stat(filepath.Join(path, "response.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cancellation published result", err)
	}
}

func TestAppendLogMailboxSealSkipsDispatch(t *testing.T) {
	f, _, _ := oscoreRPCFixture(t)
	path, m := appendMailboxFixture(t, "\n")
	if err := ProcessAppendLogMailbox(t.Context(), m, f.d, nil); err != nil {
		t.Fatal(err)
	}
	cursor, err := m.cursor()
	if err != nil || !cursor.Sealed || cursor.Offset != 1 {
		t.Fatal("seal not checkpointed", cursor, err)
	}
	if _, err := os.Stat(filepath.Join(path, "response.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("seal published response", err)
	}
	if _, err := f.lookup(t, "oscore-rpc-message"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("seal reached journal", err)
	}
}
