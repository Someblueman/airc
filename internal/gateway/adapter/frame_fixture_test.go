//go:build darwin || linux

package adapter

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
)

// frameFixture is a local listener's dispatcher and one plain correlated
// frame naming the fixture's guest author, shared by the file-backed and
// Unix-socket local carriers that take the same frame.
func frameFixture(t *testing.T) (plainFixture, []byte) {
	t.Helper()
	f := newPlainFixture(t, config.Listener{ID: "frames", Protocol: "unixgram", Operations: []core.Operation{core.Send}})
	request := core.Request{Version: 1, RequestID: "frame-message", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#fixture"}, Message: "first\r\nsecond"}
	body := []byte(`{"version":1,"correlation_id":"native-rpc","profile":"plain","request":` + f.body(t, request) + `}`)
	return f, body
}
