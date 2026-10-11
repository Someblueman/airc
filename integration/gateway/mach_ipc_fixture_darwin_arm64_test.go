//go:build darwin && arm64

package gateway_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/pkg/irc"
)

type machIPCFixture struct {
	process     *gatewayProcess
	client      string
	serviceName string
	config      config.Config
	credentials *detachedCredentials
	observer    *irc.Client
}

// newMachIPCFixture admits only callerUID; requests name their author.
func newMachIPCFixture(t *testing.T, callerUID uint32) machIPCFixture {
	t.Helper()
	native := fixtureWithAdmin(t, true)
	const account = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	provision := dial(t, native, "fixture-author", account, true, true)
	_ = provision.Close()
	promoteRoomOperator(t, native)
	c, _ := acceptanceJournalFixture(t, native, account)
	c.CursorKeyFile = filepath.Join(filepath.Dir(c.StateDir), "cursor.key")
	if err := auth.CreateCredential(c.CursorKeyFile); err != nil {
		t.Fatal(err)
	}
	operations := dispatch.ImplementedOperations()
	credentials := provisionDetached(t, &c)
	serviceName := fmt.Sprintf("com.airc.gateway.test.%d.%d", os.Getpid(), time.Now().UnixNano())
	uid := callerUID
	listener := config.Listener{
		ID: "mach", Protocol: "mach_ipc", Enabled: true, Address: serviceName, Operations: operations,
		MachIPC: &config.MachIPC{Callers: []config.MachIPCCaller{{UID: &uid}}},
	}
	c.Listeners = []config.Listener{listener}
	binary := currentGatewayBinary(t)
	process := startGateway(t, binary, c)
	registerGatewayShutdownCheck(t, process)
	if got := gatewayAddress(t, process); got != serviceName {
		t.Fatalf("Mach bootstrap service name=%q want=%q", got, serviceName)
	}
	observer := dial(t, native, "mach-observer", "", false, false)
	if err := observer.Join("#gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	next(t, observer, func(event *irc.JoinEvent) bool {
		return event.Agent == "mach-observer" && event.Channel == "#gateway-fixture"
	})
	return machIPCFixture{
		process: process, client: buildMachIPCClient(t), serviceName: serviceName,
		config: c, credentials: credentials, observer: observer,
	}
}

func machFrame(t *testing.T, f machIPCFixture, request core.Request) frameRequest {
	t.Helper()
	requestBody := f.credentials.token(t, request, true)
	digest := sha256.Sum256([]byte(requestBody))
	return frameRequest{Version: 1, CorrelationID: hex.EncodeToString(digest[:]), Profile: "plain", Request: requestBody}
}

func machFrameBytes(t *testing.T, frame frameRequest) []byte {
	t.Helper()
	data, err := json.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
