//go:build (darwin && arm64) || (linux && (arm64 || amd64))

package gateway_test

import (
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/sysvqueue"
)

func TestSystemVQueueReplacementIsPreservedAndNeverAdopted(t *testing.T) {
	f := newSystemVFixtureShutdown(t, false)
	original := f.source.Identity()
	// Remove the actual ingress identifier and bind the same key to a new lifecycle.
	removeSystemVQueue(t, original.ID)
	replacement, err := sysvqueue.Create(original.Key)
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	if replacement.Identity() == original {
		t.Fatal("replacement reused original lifecycle")
	}
	if err := f.source.WriteMessage(t.Context(), []byte("never send through replaced name")); err == nil {
		t.Fatal("old producer handle ignored replacement", err)
	}
	select {
	case <-f.process.done:
		if f.process.err == nil {
			t.Fatal("replaced ingress exited without reporting failure")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("replaced ingress did not join")
	}
	opened, err := sysvqueue.Open(original.Key)
	if err != nil {
		t.Fatal("shutdown removed external replacement", err)
	}
	defer opened.Close()
	if opened.Identity() != replacement.Identity() {
		t.Fatal("shutdown changed replacement")
	}
	// A journal record for the original lifecycle cannot authorize this replacement.
	p := startGateway(t, gatewayBinary(t), f.config)
	select {
	case <-p.done:
		if p.err == nil {
			t.Fatal("startup adopted replacement")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("replacement startup did not refuse")
	}
	select {
	case ready := <-p.ready:
		t.Fatal("refused startup reported ready", string(ready))
	default:
	}
	again, err := sysvqueue.Open(original.Key)
	if err != nil {
		t.Fatal("refusal removed replacement", err)
	}
	defer again.Close()
	if again.Identity() != replacement.Identity() {
		t.Fatal("refusal changed replacement")
	}
	response, err := sysvqueue.Open(f.mailbox.Identity().Key)
	if err != nil {
		t.Fatal("response queue removed", err)
	}
	defer response.Close()
	if response.Identity() != f.mailbox.Identity() {
		t.Fatal("response queue changed")
	}
	rpcNoJobs(t, f.config.StateDir)
	if records := nativeWebTransportMessages(t, f.config.Bindings[0].Address, "#gateway-fixture"); len(records) != 0 {
		t.Fatal("replacement admitted native input", records)
	}
}
