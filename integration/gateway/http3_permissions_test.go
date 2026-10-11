//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"github.com/Someblueman/airc/internal/gateway/core"
	"net/http"
	"testing"
)

func TestHTTP3DeniedAndUnknownRequestsCreateNoJobs(t *testing.T) {
	f := newHTTP3GatewayFixture(t)
	// A guest on a registered nickname is refused by AIRC.
	identify := rpcRequestParams(core.Request{Version: 1, RequestID: "http3-denied", Operation: core.AccountIdentify})
	identify["author"] = map[string]string{"nick": fixtureAuthor}
	response, body := http3Call(t, f, "", "/v1/rpc", rpcRequestBody(t, "denied", "airc.request", identify))
	if response.StatusCode != http.StatusOK || !bytes.Contains(decodeRPC(t, body).Error, []byte(core.NickRegistered)) {
		t.Fatalf("guest on registered nickname request=%s", body)
	}
	request := core.Request{Version: 1, RequestID: "http3-unknown", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "must not post"}
	params := rpcRequestParams(request)
	params["unknown"] = "must reject"
	response, body = http3Call(t, f, f.token, "/v1/rpc", rpcRequestBody(t, "unknown", "airc.request", params))
	if response.StatusCode != http.StatusOK || len(decodeRPC(t, body).Error) == 0 {
		t.Fatalf("unknown core field accepted: %s", body)
	}
	rpcNoJobs(t, f.config.StateDir)
}
