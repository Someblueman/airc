//go:build darwin && arm64

package gateway_test

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/pkg/irc"
)

type machIPCFixture struct {
	process           *gatewayProcess
	client            string
	serviceName       string
	config            config.Config
	credentials       *detachedCredentials
	observer          *irc.Client
	responseSigning   ed25519.PublicKey
	responseRecipient *ecdsa.PrivateKey
	routeID           string
}

func newMachIPCFixture(t *testing.T, callerUID uint32, callerPrincipal string) machIPCFixture {
	t.Helper()
	if callerPrincipal == "" {
		callerPrincipal = "device"
	}
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
	c.Principals[0].Operations = operations
	c.Principals[0].Targets = []core.Target{{Kind: "room", Name: "#gateway-fixture"}, {Kind: "direct", Name: "fixture-author"}}
	credentials := provisionDetached(t, &c)
	responseSigning, responseRecipient := provisionUDPResponseCredentials(t, &c)
	c.ResponseSigningKey.ID = "mach-response"
	if callerPrincipal != c.Principals[0].ID {
		addMachPrincipal(t, &c, callerPrincipal, operations)
	}
	serviceName := fmt.Sprintf("com.airc.gateway.test.%d.%d", os.Getpid(), time.Now().UnixNano())
	uid := callerUID
	listener := config.Listener{
		ID: "mach", Protocol: "mach_ipc", Enabled: true, Address: serviceName, Operations: operations,
		MachIPC: &config.MachIPC{Callers: []config.MachIPCCaller{{UID: &uid, Principal: callerPrincipal}}},
	}
	routeID := "mach-return-" + callerPrincipal
	c.Listeners = []config.Listener{listener}
	c.ReturnRoutes = []config.ReturnRoute{{
		ID: routeID, Principal: callerPrincipal, Protocol: "mach_ipc", Listener: listener.ID,
		Destination: "reply_port", RecipientKeyID: "mach-reader",
		RecipientPublicKeyFile: filepath.Join(filepath.Dir(c.StateDir), "udp-reader-public.pem"),
	}}
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
		config: c, credentials: credentials, observer: observer, responseSigning: responseSigning,
		responseRecipient: responseRecipient, routeID: routeID,
	}
}

func addMachPrincipal(t *testing.T, c *config.Config, principal string, operations []core.Operation) {
	t.Helper()
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publicJWK, err := json.Marshal(map[string]string{
		"kty": "OKP", "crv": "Ed25519", "x": base64.RawURLEncoding.EncodeToString(public),
	})
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(c.Principals[0].BearerTokenFile)
	publicPath := filepath.Join(root, "signing-"+principal+".json")
	if err := os.WriteFile(publicPath, publicJWK, 0644); err != nil {
		t.Fatal(err)
	}
	bearerPath := filepath.Join(root, "bearer-"+principal+".token")
	if err := auth.CreateCredential(bearerPath); err != nil {
		t.Fatal(err)
	}
	c.Principals = append(c.Principals, config.Principal{
		ID: principal, Binding: c.Principals[0].Binding, BearerTokenFile: bearerPath,
		SigningKeys: []config.SigningKey{{ID: "signer-" + principal, PublicKeyFile: publicPath}},
		Operations:  operations, Targets: []core.Target{{Kind: "room", Name: "#gateway-fixture"}, {Kind: "direct", Name: "fixture-author"}},
	})
}

func machFrame(t *testing.T, f machIPCFixture, request core.Request) adapter.FrameRequest {
	t.Helper()
	envelope := f.credentials.token(t, request, true)
	digest := sha256.Sum256([]byte(envelope))
	return adapter.FrameRequest{Version: 1, CorrelationID: hex.EncodeToString(digest[:]), Profile: "encrypted", Envelope: envelope}
}

func machFrameBytes(t *testing.T, frame adapter.FrameRequest) []byte {
	t.Helper()
	data, err := json.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
