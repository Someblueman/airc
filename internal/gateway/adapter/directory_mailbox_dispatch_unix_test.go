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

func TestDirectoryMailboxAuthenticatedOwnerAndReplay(t *testing.T) {
	d, s, identity, body, owner := oscoreRPCFixture(t)
	path, m := directoryMailboxFixture(t)
	var upstream string
	for i := 0; i < 2; i++ {
		if err := os.WriteFile(filepath.Join(path, "inbox", "native-rpc.json"), body, 0600); err != nil {
			t.Fatal(err)
		}
		if err := ProcessDirectoryMailbox(t.Context(), m, d, owner.Principal, owner.Binding); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(path, "results", "native-rpc.json"))
		if err != nil {
			t.Fatal(err)
		}
		var response FrameResponse
		if err := json.Unmarshal(data, &response); err != nil || response.Error != nil || response.CorrelationID != "native-rpc" || response.Result == nil || response.Result.Result == nil || !response.Result.Result.Durable {
			t.Fatal("directory intake failed", err, response)
		}
		job, err := s.Lookup(t.Context(), identity, "oscore-rpc-message")
		if err != nil || job.Principal != owner.Principal || job.Binding != owner.Binding || job.Request.Message != "first\nsecond" || job.Result.SendAttempts != 0 {
			t.Fatal("wrong durable directory intake", job, err)
		}
		if i == 0 {
			upstream = job.UpstreamID
		} else if upstream == "" || upstream != job.UpstreamID {
			t.Fatal("directory replay changed upstream identity")
		}
		if err := os.Remove(filepath.Join(path, "results", "native-rpc.json")); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDirectoryMailboxRefusalsPrecedeIntake(t *testing.T) {
	for _, kind := range []string{"principal", "binding", "envelope", "name", "stream", "profile"} {
		t.Run(kind, func(t *testing.T) {
			d, s, identity, body, owner := oscoreRPCFixture(t)
			var request FrameRequest
			if err := json.Unmarshal(body, &request); err != nil {
				t.Fatal(err)
			}
			code := core.AuthenticationFailed
			switch kind {
			case "principal":
				owner.Principal = "other"
			case "binding":
				owner.Binding = "other"
			case "envelope":
				request.Envelope = "invalid"
			case "name":
				request.CorrelationID = "other"
				code = core.InvalidRequest
			case "stream":
				request.Stream = true
				code = core.InvalidRequest
			case "profile":
				request.Profile = "bearer"
				code = core.InvalidRequest
			}
			body, err := json.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}
			path, m := directoryMailboxFixture(t)
			if err := os.WriteFile(filepath.Join(path, "inbox", "native-rpc.json"), body, 0600); err != nil {
				t.Fatal(err)
			}
			if err := ProcessDirectoryMailbox(t.Context(), m, d, owner.Principal, owner.Binding); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(path, "results", "native-rpc.json"))
			var response FrameResponse
			if err != nil || json.Unmarshal(data, &response) != nil || response.Error == nil || response.Error.Code != code {
				t.Fatal("wrong directory refusal", err, response)
			}
			if _, err := s.Lookup(t.Context(), identity, "oscore-rpc-message"); !errors.Is(err, store.ErrNotFound) {
				t.Fatal("directory refusal reached durable intake", err)
			}
		})
	}
}

func TestDirectoryMailboxCancellationRetainsClaim(t *testing.T) {
	d, _, _, body, owner := oscoreRPCFixture(t)
	path, m := directoryMailboxFixture(t)
	if err := os.WriteFile(filepath.Join(path, "inbox", "native-rpc.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.Claim(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := ProcessDirectoryMailbox(ctx, m, d, owner.Principal, owner.Binding); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation hidden", err)
	}
	if _, err := os.Stat(filepath.Join(path, "processing", "native-rpc.json")); err != nil {
		t.Fatal("cancellation lost claim", err)
	}
	if _, err := os.Stat(filepath.Join(path, "results", "native-rpc.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cancellation published result", err)
	}
}
