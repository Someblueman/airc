//go:build darwin || linux

package gateway_test

import (
	"crypto/ed25519"
	"os"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/testtls"
)

type stompFixture struct {
	*fileMailboxFixture
	process         *gatewayProcess
	address, bearer string
	cert            testtls.Certificate
	responseSigning ed25519.PublicKey
}

func prepareSTOMPFixture(t *testing.T) *stompFixture {
	t.Helper()
	f := &stompFixture{fileMailboxFixture: newFileMailboxFixture(t), cert: testtls.New(t)}
	ops := []core.Operation{core.Send, core.ContextRead, core.HistoryRead, core.Search}
	f.config.Principals[0].Operations = append(append([]core.Operation{}, ops...), core.ReceiptGet)
	f.config.Listeners = []config.Listener{{ID: "stomp", Protocol: "stomp", Enabled: true, Address: "127.0.0.1:0", TLSCertFile: f.cert.CertPath, TLSKeyFile: f.cert.KeyPath, Operations: ops, STOMP: &config.STOMP{Routes: []config.STOMPRoute{{Principal: f.config.Principals[0].ID, RequestDestination: "request", ResponseDestination: "response", Target: core.Target{Kind: "room", Name: "#gateway-fixture"}, Operations: ops}}}}}
	f.responseSigning, _ = provisionUDPResponseCredentials(t, &f.config)
	var err error
	f.bearer, err = auth.ReadToken(f.config.Principals[0].BearerTokenFile)
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func (f *stompFixture) start(t *testing.T) {
	t.Helper()
	f.process = startGateway(t, f.binary, f.config)
	f.address = gatewayAddress(t, f.process)
}
func (f *stompFixture) stop(t *testing.T) {
	t.Helper()
	if err := f.process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.process.done:
		if f.process.err != nil {
			t.Fatal(f.process.err, f.process.stderr.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("STOMP service did not join")
	}
}
