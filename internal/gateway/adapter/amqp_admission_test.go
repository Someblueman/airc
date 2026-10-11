//go:build darwin || linux

package adapter

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

// TestAMQPAdmissionRefusesBeforeIntake covers AMQP's own refusals; the
// refusals every carrier shares are in TestCarrierAdmission.
func TestAMQPAdmissionRefusesBeforeIntake(t *testing.T) {
	for _, kind := range []string{"route-operation", "partial"} {
		t.Run(kind, func(t *testing.T) {
			f := newAMQPAuthorFixture(t)
			slots := make(chan struct{}, 1)
			in := f.body(t, amqpAuthorRequest())
			route := f.route()
			switch kind {
			case "route-operation":
				route.Operations = []core.Operation{core.HistoryRead}
			case "partial":
				in.complete = false
			}
			admission := admitAMQP(t.Context(), f.d, f.in, route, in, slots)
			if admission.result != nil || len(slots) != 0 {
				t.Fatal(kind, admission)
			}
			if kind == "route-operation" && (admission.failure == nil || admission.failure.Code != core.Forbidden || admission.requestID != "amqp-stable-request") {
				t.Fatal(admission, admission.failure)
			}
			if kind == "partial" && admission.condition != "amqp:illegal-state" {
				t.Fatal(admission)
			}
			if f.jobs(t, "amqp-stable-request") != 0 {
				t.Fatal("refusal reached intake")
			}
		})
	}
}
