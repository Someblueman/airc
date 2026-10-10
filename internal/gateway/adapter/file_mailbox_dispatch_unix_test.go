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

func TestFileMailboxAuthenticatedIntakeAndStableReplay(t *testing.T) {
	d, s, identity, body, _ := oscoreRPCFixture(t)
	path := mailboxDirectory(t)
	var upstream string
	for i := 0; i < 2; i++ {
		if err := os.WriteFile(filepath.Join(path, "request.json"), body, 0600); err != nil {
			t.Fatal(err)
		}
		m, err := OpenFileMailbox(path)
		if err != nil {
			t.Fatal(err)
		}
		err = ProcessFileMailbox(t.Context(), m, d)
		closeErr := m.Close()
		if err != nil || closeErr != nil {
			t.Fatal(err, closeErr)
		}
		data, err := os.ReadFile(filepath.Join(path, "response.json"))
		if err != nil {
			t.Fatal(err)
		}
		var response FrameResponse
		if err := json.Unmarshal(data, &response); err != nil || response.Error != nil || response.CorrelationID != "native-rpc" || response.Result == nil || response.Result.Result == nil || !response.Result.Result.Durable {
			t.Fatal("mailbox intake failed", err, response)
		}
		job, err := s.Lookup(t.Context(), identity, "oscore-rpc-message")
		if err != nil || job.Request.Message != "first\nsecond" || job.Result.SendAttempts != 0 {
			t.Fatal("wrong mailbox intake", job, err)
		}
		if i == 0 {
			upstream = job.UpstreamID
		} else if upstream == "" || upstream != job.UpstreamID {
			t.Fatal("mailbox replay changed upstream ID")
		}
		if err := os.Remove(filepath.Join(path, "response.json")); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFileMailboxAuthenticationAndModeRefusals(t *testing.T) {
	for _, kind := range []string{"signature", "profile", "stream", "version"} {
		t.Run(kind, func(t *testing.T) {
			d, s, identity, body, _ := oscoreRPCFixture(t)
			var frame FrameRequest
			if err := json.Unmarshal(body, &frame); err != nil {
				t.Fatal(err)
			}
			code := core.InvalidRequest
			switch kind {
			case "signature":
				frame.Envelope = "invalid"
				code = core.AuthenticationFailed
			case "profile":
				frame.Profile = "bearer"
			case "stream":
				frame.Stream = true
			case "version":
				frame.Version = 2
			}
			body, err := json.Marshal(frame)
			if err != nil {
				t.Fatal(err)
			}
			path := mailboxDirectory(t)
			if err := os.WriteFile(filepath.Join(path, "request.json"), body, 0600); err != nil {
				t.Fatal(err)
			}
			m, err := OpenFileMailbox(path)
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			if err := ProcessFileMailbox(t.Context(), m, d); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(path, "response.json"))
			if err != nil {
				t.Fatal(err)
			}
			var response FrameResponse
			if err := json.Unmarshal(data, &response); err != nil || response.Error == nil || response.Error.Code != code {
				t.Fatal("wrong mailbox refusal", err, response)
			}
			if _, err := s.Lookup(t.Context(), identity, "oscore-rpc-message"); !errors.Is(err, store.ErrNotFound) {
				t.Fatal("refused mailbox reached intake", err)
			}
		})
	}
}

func TestFileMailboxCancellationRetainsInput(t *testing.T) {
	d, _, _, body, _ := oscoreRPCFixture(t)
	path := mailboxDirectory(t)
	if err := os.WriteFile(filepath.Join(path, "request.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	m, err := OpenFileMailbox(path)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if _, err := m.Claim(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := ProcessFileMailbox(ctx, m, d); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation hidden", err)
	}
	if _, err := os.Stat(filepath.Join(path, "processing.json")); err != nil {
		t.Fatal("cancellation lost input", err)
	}
	if _, err := os.Stat(filepath.Join(path, "response.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cancellation published response", err)
	}
}
