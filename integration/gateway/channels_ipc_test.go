//go:build darwin || linux

package gateway_test

import (
	"net/http"
	"os"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/testtls"
)

func TestChannelsReadOverUnixStream(t *testing.T) {
	conn, credentials, stop, _, socket := unixStreamFixture(t)
	defer conn.Close()
	defer stop()
	want := nativeChannels(t, socket)
	request := core.Request{Version: 1, RequestID: "channels-unix", Operation: core.ChannelsRead}
	status, result := contextFramedCall(t, conn, credentials.token(t, request, true), "channels-unix", true)
	if status != http.StatusOK || result.Channels == nil || !channelsEqual(result.Channels.Channels, want) {
		t.Fatalf("unix channels=%+v want=%+v status=%d", result.Channels, want, status)
	}
	seedChannels(t, socket)
	want = nativeChannels(t, socket)
	request.RequestID = "channels-unix-seeded"
	status, result = contextFramedCall(t, conn, credentials.token(t, request, true), "channels-unix-seeded", true)
	if status != http.StatusOK || result.Channels == nil || !channelsEqual(result.Channels.Channels, want) {
		t.Fatalf("seeded unix channels=%+v want=%+v status=%d", result.Channels, want, status)
	}
}

func TestChannelsReadOverStdio(t *testing.T) {
	socket, config, credentials, observer := stdioFixtureConfig(t)
	defer observer.Close()
	seedChannels(t, socket)
	want := nativeChannels(t, socket)
	process := startStdio(t, gatewayBinary(t), config)
	request := core.Request{Version: 1, RequestID: "channels-stdio-signed", Operation: core.ChannelsRead}
	for _, encrypted := range []bool{false, true} {
		if encrypted {
			request.RequestID = "channels-stdio-encrypted"
		}
		profile := "plain"
		process.write(t, frameRequest{Version: 1, CorrelationID: request.RequestID, Profile: profile, Request: credentials.token(t, request, encrypted)})
		response := process.read(t)
		if response.Event != "result" || response.Result == nil || response.Result.Channels == nil || !channelsEqual(response.Result.Channels.Channels, want) {
			t.Fatalf("stdio %s channels=%+v want=%+v", profile, response, want)
		}
	}
	if err := process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	if err := process.wait(); err != nil {
		t.Fatalf("stdio gateway did not join: %v", err)
	}
}

func TestChannelsReadRequiresListenerOperation(t *testing.T) {
	socket := fixture(t)
	provision := dial(t, socket, "fixture-author", fixtureAccount, true, true)
	_ = provision.Close()
	c, _ := acceptanceJournalFixture(t, socket, fixtureAccount)
	cert := testtls.New(t)
	c.Listeners = []config.Listener{{ID: "channels", Protocol: "http", Enabled: true, Address: "127.0.0.1:0", TLSCertFile: cert.CertPath, TLSKeyFile: cert.KeyPath, Operations: []core.Operation{core.Send}}}
	p := startGateway(t, gatewayBinary(t), c)
	address := gatewayAddress(t, p)
	token := fixtureAuthor + ":" + fixtureAccount
	transport := &http.Transport{TLSClientConfig: cert.Client}
	t.Cleanup(transport.CloseIdleConnections)
	request, err := http.NewRequest(http.MethodGet, "https://"+address+"/v1/channels", nil)
	if err != nil {
		t.Fatal(err)
	}
	setAuthorHeaders(request, token)
	response, err := (&http.Client{Transport: transport}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNotImplemented {
		t.Fatalf("listener without channels.read status=%d", response.StatusCode)
	}
}
