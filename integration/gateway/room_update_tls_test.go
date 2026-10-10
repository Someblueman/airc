//go:build darwin || linux

package gateway_test

import (
	"crypto/tls"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/testtls"
)

func TestRoomUpdateOverFramedTLS(t *testing.T) {
	conn, credentials, stop, socket := roomUpdateTLSFixture(t)
	defer conn.Close()
	defer stop()
	call := func(request core.Request, correlation string) adapter.FrameResponse {
		writeFrame(t, conn, adapter.FrameRequest{Version: 1, CorrelationID: correlation, Profile: "encrypted", Envelope: credentials.token(t, request, true)})
		response := readFrame(t, conn)
		if response.CorrelationID != correlation || response.Event != "result" {
			t.Fatalf("TLS room response mismatch: %+v", response)
		}
		return response
	}
	target := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	slow, limit := int64(31), 14
	request := roomUpdateRequest(t, "tls-room-update", target, &slow, &limit)
	result := assertRoomUpdate(t, call, socket, request)
	if result.Room == nil || result.Room.Room.SlowSeconds != slow || result.Room.Room.HistoryLimit != limit {
		t.Fatalf("TLS room result mismatch: %+v", result.Room)
	}
	zero := 0
	partial := roomUpdateRequest(t, "tls-room-partial", target, nil, &zero)
	result = assertRoomUpdate(t, call, socket, partial)
	if result.Room == nil || !reflect.DeepEqual(result.Room.Room, core.RoomResult{Target: *target, SlowSeconds: slow, HistoryLimit: zero}) {
		t.Fatalf("TLS partial room result mismatch: %+v", result.Room)
	}
}

func roomUpdateTLSFixture(t *testing.T) (net.Conn, *detachedCredentials, func(), string) {
	t.Helper()
	socket := fixtureWithAdmin(t, true)
	provision := dial(t, socket, "fixture-author", fixtureAccount, true, true)
	provision.Close()
	c, _ := acceptanceJournalFixture(t, socket, fixtureAccount)
	credentials := provisionDetached(t, &c)
	c.Principals[0].BearerTokenFile = ""
	c.Principals[0].Operations = dispatch.ImplementedOperations()
	c.CursorKeyFile = filepath.Join(filepath.Dir(c.StateDir), "cursor.key")
	if err := auth.CreateCredential(c.CursorKeyFile); err != nil {
		t.Fatal(err)
	}
	certificate := testtls.New(t)
	c.Listeners = []config.Listener{{ID: "stream", Protocol: "framed", Address: "127.0.0.1:0", Enabled: true, Operations: dispatch.ImplementedOperations(), TLSCertFile: certificate.CertPath, TLSKeyFile: certificate.KeyPath}}
	p := startGateway(t, gatewayBinary(t), c)
	address := gatewayAddress(t, p)
	trust := certificate.Client.Clone()
	trust.NextProtos = []string{adapter.FramedALPN}
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 3 * time.Second}, "tcp", address, trust)
	if err != nil {
		t.Fatal(err)
	}
	promoteRoomOperator(t, socket)
	stop := func() {
		if err := p.command.Process.Signal(os.Interrupt); err != nil {
			t.Fatal(err)
		}
		select {
		case <-p.done:
			if p.err != nil {
				t.Fatal(p.err, p.stderr.String())
			}
		case <-time.After(8 * time.Second):
			t.Fatal("TLS gateway did not join")
		}
	}
	return conn, credentials, stop, socket
}
