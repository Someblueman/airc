//go:build darwin || linux

package adapter

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestMachIPCRequiresExactUIDAndAuthenticatedPrincipalBeforeIntake(t *testing.T) {
	for _, refusal := range []string{"allowed", "unknown-uid", "other-principal", "stream", "signed", "correlation", "missing-route"} {
		t.Run(refusal, func(t *testing.T) {
			f, request, protection, _ := sctpApplicationFixture(t)
			const uid = uint32(501)
			callers := []MachCaller{{UID: uid, Principal: f.identity.ID()}}
			if refusal == "other-principal" {
				callers = []MachCaller{{UID: uid, Principal: "other"}, {UID: uid + 1, Principal: f.identity.ID()}}
			}
			m, err := NewMachIPC(f.d, make(chan struct{}, 1), protection, callers)
			if err != nil {
				t.Fatal(err)
			}
			callerUID := uid
			switch refusal {
			case "unknown-uid":
				callerUID++
			case "stream":
				request.Stream = true
			case "signed":
				request.Profile = "signed"
			case "correlation":
				request.CorrelationID = "unbound-label"
			case "missing-route":
				clear(protection.routes)
			}
			body, err := json.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}
			_, verified, err := m.authenticate(body, callerUID)
			if refusal == "allowed" {
				if err != nil || verified.PrincipalID() != f.identity.ID() || verified.Binding() != f.identity.Binding() {
					t.Fatalf("caller did not authenticate to configured identity: %v", err)
				}
			} else if err == nil {
				t.Fatal("unauthorized request authenticated")
			}
			if _, err := f.s.Lookup(t.Context(), f.identity, amqpRequest().RequestID); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("authentication reached durable intake: %v", err)
			}
		})
	}
}

func TestMachIPCRejectsDuplicateAndUnspecifiedCallerGrants(t *testing.T) {
	f, _, protection, _ := sctpApplicationFixture(t)
	for _, callers := range [][]MachCaller{nil, {{UID: ^uint32(0), Principal: f.identity.ID()}}, {{UID: 501}}, {{UID: 501, Principal: f.identity.ID()}, {UID: 501, Principal: f.identity.ID()}}} {
		if _, err := NewMachIPC(f.d, make(chan struct{}, 1), protection, callers); !errors.Is(err, store.ErrInvalid) {
			t.Fatalf("invalid grants accepted: %v", err)
		}
	}
}
