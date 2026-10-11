//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"github.com/plgd-dev/go-coap/v3/message/pool"
	"github.com/plgd-dev/go-coap/v3/tcp/coder"
)

func TestCoAPTCPHonorsPeerFrameLimitWithoutPartialResponse(t *testing.T) {
	f := newCoAPTCPFixture(t)
	request := core.Request{Version: 1, RequestID: "tcp-small-peer", Operation: core.CapabilitiesGet}
	body, err := json.Marshal(frameRequest{Version: 1, CorrelationID: "tcp-small-peer", Profile: "plain", Request: f.credentials.token(t, request, false)})
	if err != nil {
		t.Fatal(err)
	}
	csm := pool.NewMessage(t.Context())
	csm.SetCode(codes.CSM)
	csm.SetOptionUint32(message.TCPMaxMessageSize, 32)
	wire, err := csm.MarshalWithEncoder(coder.DefaultCoder)
	if err != nil {
		t.Fatal(err)
	}
	conn := dialRawCoAPTCP(t, f, wire, rawCoAPTCPMessage(t, codes.POST, "/v1/coap/rpc", body, true))
	defer conn.Close()
	initial := readRawCoAPTCPMessage(t, conn, time.Now().Add(3*time.Second))
	if initial.Code != codes.CSM {
		t.Fatalf("first server frame=%v", initial.Code)
	}
	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	one := make([]byte, 1)
	n, err := conn.Read(one)
	if n != 0 || err != io.EOF {
		t.Fatalf("oversized application response leaked bytes=%x count=%d error=%v", one[:n], n, err)
	}
	control := coapTCPRPC(t, f, core.Request{Version: 1, RequestID: "tcp-full-peer", Operation: core.CapabilitiesGet}, false)
	if control.Result == nil || control.Result.Capabilities == nil {
		t.Fatalf("ordinary peer control=%+v", control)
	}
	rpcNoJobs(t, f.config.StateDir)
}
