//go:build darwin || linux

package adapter

import (
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/amqpwire"
	"github.com/Someblueman/airc/internal/gateway/core"
)

// TestAMQPAdmissionTTLBoundsNativeRead: a message header's TTL, not only the
// caller's context, bounds a read's admission (TestCarrierAdmission covers
// the parent deadline).
func TestAMQPAdmissionTTLBoundsNativeRead(t *testing.T) {
	f := newAMQPAuthorFixture(t)
	assertNativeClosed := stalledNativeRead(t, f.c.Upstream.Address)
	in := f.body(t, map[string]any{"version": 1, "request_id": "amqp-read", "operation": core.ContextRead, "parent_id": strings.Repeat("a", 32), "target": map[string]string{"kind": "room", "name": "#fixture"}, "limit": 1, "author": map[string]string{"nick": "amqp-guest"}})
	headerValue := amqpwire.Value{
		Descriptor: &amqpwire.Value{Code: 0x80, Number: 0x70},
		Children:   []amqpwire.Value{{Code: 0xc0, Children: []amqpwire.Value{{Code: 0x40}, {Code: 0x40}, {Code: 0x70, Number: 150}}}},
	}
	header, err := amqpwire.EncodeValue(headerValue, 65536)
	if err != nil {
		t.Fatal(err)
	}
	in.encoded = append(header, in.encoded...)
	started := time.Now()
	slots := make(chan struct{}, 1)
	admission := admitAMQP(t.Context(), f.d, f.in, f.route(), in, slots)
	if admission.condition != "" || admission.result != nil || admission.failure == nil || !admission.failure.Transient || admission.requestID != "amqp-read" || time.Since(started) > time.Second || len(slots) != 0 {
		t.Fatal(admission, time.Since(started))
	}
	assertNativeClosed()
	if f.jobs(t, "amqp-read") != 0 {
		t.Fatal("read admitted mutation")
	}
}
