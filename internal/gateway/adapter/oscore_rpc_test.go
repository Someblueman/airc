//go:build darwin || linux

package adapter

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

func TestOSCORERPCContextOwnerPrecedesIntakeAndReplay(t *testing.T) {
	d, s, identity, body, owner := oscoreRPCFixture(t)
	requests := make(chan struct{}, 1)
	for _, field := range []string{"principal", "binding"} {
		wrong := owner
		if field == "principal" {
			wrong.Principal = "other"
		} else {
			wrong.Binding = "other"
		}
		code, response := oscoreRPCRequest(t.Context(), body, d, requests, wrong, nil)
		if code != codes.Unauthorized || response.Error == nil || response.Error.Code != core.AuthenticationFailed {
			t.Fatal("context owner mismatch accepted", field, code, response)
		}
		if _, err := s.Lookup(t.Context(), identity, "oscore-rpc-message"); !errors.Is(err, store.ErrNotFound) {
			t.Fatal("owner mismatch reached intake", err)
		}
	}
	var upstream string
	for i := 0; i < 2; i++ {
		code, response := oscoreRPCRequest(t.Context(), body, d, requests, owner, nil)
		if code != codes.Content || response.Error != nil || response.Sequence != 1 || response.CorrelationID != "native-rpc" || response.Result == nil || response.Result.Result == nil || !response.Result.Result.Durable {
			t.Fatal("owner intake failed", code, response)
		}
		job, err := s.Lookup(t.Context(), identity, "oscore-rpc-message")
		if err != nil || job.Principal != owner.Principal || job.Binding != owner.Binding || job.Request.Message != "first\nsecond" || job.Result.SendAttempts != 0 {
			t.Fatal("wrong durable intake", job, err)
		}
		if i == 0 {
			upstream = job.UpstreamID
		} else if upstream == "" || upstream != job.UpstreamID {
			t.Fatal("replay changed stable upstream ID")
		}
		if len(requests) != 0 {
			t.Fatal("request admission leaked")
		}
	}
}

func TestOSCORERPCRejectsStreamingAndFullAdmissionBeforeIntake(t *testing.T) {
	d, s, identity, body, owner := oscoreRPCFixture(t)
	requests := make(chan struct{}, 1)
	var request FrameRequest
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatal(err)
	}
	request.Stream = true
	stream, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	code, response := oscoreRPCRequest(t.Context(), stream, d, requests, owner, nil)
	if code != codes.BadRequest || response.Error == nil {
		t.Fatal("stream accepted", code, response)
	}
	requests <- struct{}{}
	code, response = oscoreRPCRequest(t.Context(), body, d, requests, owner, nil)
	if code != codes.ServiceUnavailable || response.Error == nil || response.Error.Code != core.CapacityExceeded || !response.Error.Transient || len(requests) != 1 {
		t.Fatal("full admission accepted", code, response)
	}
	if _, err := s.Lookup(t.Context(), identity, "oscore-rpc-message"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("rejected request reached intake", err)
	}
}
