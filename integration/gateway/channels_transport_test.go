//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/testtls"
	"github.com/Someblueman/airc/pkg/irc"
)

// TestChannelsReadNativeMetadataAndPart maps channels.read to the native room
// list with members, retained message counts and topics, never a direct
// conversation, and reflects a native PART on the next read.
func TestChannelsReadNativeMetadataAndPart(t *testing.T) {
	f := newReactionHTTPFixture(t)
	live := seedChannels(t, f.socket)
	beforeHistory := channelHistorySnapshot(t, f.socket)
	want := nativeChannels(t, f.socket)
	if len(want) < 3 {
		t.Fatalf("native CHANNELS snapshot too small: %+v", want)
	}
	checkChannel := func(name string, messages, members int, topic string) {
		t.Helper()
		for _, channel := range want {
			if channel.Name == name {
				if channel.Messages != messages || channel.Members != members || channel.Topic != topic || (messages > 0 && channel.LastActivity.IsZero()) {
					t.Fatalf("native channel %s metadata=%+v", name, channel)
				}
				return
			}
		}
		t.Fatalf("native CHANNELS omitted %s: %+v", name, want)
	}
	checkChannel("#channels-history", 2, 0, "")
	checkChannel("#channels-topic", 0, 0, "whole topic 🙂")
	checkChannel("#channels-Case", 0, 0, "case-sensitive upper")
	checkChannel("#channels-case", 0, 0, "case-sensitive lower")
	checkChannel("#channels-live", 0, 1, "")
	for _, channel := range want {
		if channel.Name == "channels-topic" {
			t.Fatal("native discovery leaked direct-message target")
		}
	}
	request := core.Request{Version: 1, RequestID: "channels-read", Operation: core.ChannelsRead}
	status, result := reactionHTTPCall(t, f, f.cred.token(t, request, true))
	if status != http.StatusOK || result.Channels == nil || !channelsEqual(result.Channels.Channels, want) {
		t.Fatalf("channels=%+v want=%+v status=%d", result.Channels, want, status)
	}
	status, body := headerChannels(t, f, "", f.authorSpec)
	var header core.ChannelsResult
	if status != http.StatusOK || json.Unmarshal(body, &header) != nil || !channelsEqual(header.Channels, want) {
		t.Fatalf("GET channels=%+v want=%+v status=%d body=%s", header.Channels, want, status, body)
	}
	if err := live.Part("#channels-live", "done"); err != nil {
		t.Fatal(err)
	}
	next(t, live, func(e *irc.PartEvent) bool { return e.Agent == "channels-live" })
	after := nativeChannels(t, f.socket)
	status, fresh := reactionHTTPCall(t, f, f.cred.token(t, request, true))
	if status != http.StatusOK || fresh.Channels == nil || !channelsEqual(fresh.Channels.Channels, after) || channelsEqual(after, want) {
		t.Fatalf("post-PART snapshot=%+v native=%+v before=%+v status=%d", fresh.Channels, after, want, status)
	}
	if !reflect.DeepEqual(beforeHistory, channelHistorySnapshot(t, f.socket)) {
		t.Fatal("room discovery changed native history")
	}
}

// TestChannelsReadRequiresListenerOperation refuses a read the listener does
// not grant before any native work.
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
