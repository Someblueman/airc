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

func TestOSCORERPCIntakeAndReplay(t *testing.T) {
	f, body, _ := oscoreRPCFixture(t)
	requests := make(chan struct{}, 1)
	var upstream string
	for i := 0; i < 2; i++ {
		code, response := oscoreRPCRequest(t.Context(), body, f.d, requests, f.ingress())
		if code != codes.Content || response.Error != nil || response.Sequence != 1 || response.CorrelationID != "native-rpc" || response.Result == nil || response.Result.Result == nil || !response.Result.Result.Durable {
			t.Fatal("intake failed", code, response)
		}
		job, err := f.lookup(t, "oscore-rpc-message")
		if err != nil || job.Request.Message != "first\nsecond" || job.Result.SendAttempts != 0 {
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
	f, body, _ := oscoreRPCFixture(t)
	requests := make(chan struct{}, 1)
	var frame map[string]any
	if err := json.Unmarshal(body, &frame); err != nil {
		t.Fatal(err)
	}
	frame["stream"] = true
	stream, err := json.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	code, response := oscoreRPCRequest(t.Context(), stream, f.d, requests, f.ingress())
	if code != codes.BadRequest || response.Error == nil {
		t.Fatal("stream accepted", code, response)
	}
	requests <- struct{}{}
	code, response = oscoreRPCRequest(t.Context(), body, f.d, requests, f.ingress())
	if code != codes.ServiceUnavailable || response.Error == nil || response.Error.Code != core.CapacityExceeded || !response.Error.Transient || len(requests) != 1 {
		t.Fatal("full admission accepted", code, response)
	}
	if _, err := f.lookup(t, "oscore-rpc-message"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("rejected request reached intake", err)
	}
}
