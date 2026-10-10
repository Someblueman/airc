//go:build darwin || linux

package adapter

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func redisAdmissionRoute(f amqpAdmissionFixture) config.RedisRoute {
	r := f.route()
	return config.RedisRoute{Principal: r.Principal, RequestChannel: "airc request.*é", ResponseChannel: "airc response", Target: r.Target, Operations: r.Operations}
}

func redisSignedRequest(t *testing.T, f amqpAdmissionFixture, r core.Request, shift int64) []byte {
	t.Helper()
	// Reuse the existing protocol-independent compact JWS fixture.
	return stompAdmissionFrame(t, f, r, shift).Body
}
