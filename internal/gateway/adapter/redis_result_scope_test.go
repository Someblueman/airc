package adapter

import (
	"encoding/json"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/rediswire"
)

func redisResultRoute() config.RedisRoute {
	return config.RedisRoute{RequestChannel: "airc.request", ResponseChannel: "airc.response", Target: core.Target{Kind: "room", Name: "#fixture"}}
}

// TestRedisResultLiteralChannels: channel names are echoed byte for byte and
// an unpublishable or aliased channel publishes nothing. TestCarrierResults
// covers the result envelope itself.
func TestRedisResultLiteralChannels(t *testing.T) {
	route := redisResultRoute()
	route.RequestChannel, route.ResponseChannel = "literal.* request 世界", "literal response é"
	wire, err := redisResultMessage(route, "request", nil, &core.Failure{Code: core.CapacityExceeded, Transient: true})
	if err != nil {
		t.Fatal(err)
	}
	var got RedisResult
	if err := json.Unmarshal(wire, &got); err != nil {
		t.Fatal(err)
	}
	if got.RequestChannel != route.RequestChannel || got.ResponseChannel != route.ResponseChannel {
		t.Fatal("literal channel bytes changed")
	}
	for _, bad := range []string{"", "nul\x00channel", "line\nchannel", "\xff", route.ResponseChannel} {
		other := route
		other.RequestChannel = bad
		if wire, err := redisResultMessage(other, "request", nil, &core.Failure{}); err != rediswire.ErrFrame || wire != nil {
			t.Fatalf("invalid literal channel %q published", bad)
		}
	}
}
