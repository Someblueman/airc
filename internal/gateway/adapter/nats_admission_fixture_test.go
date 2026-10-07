//go:build darwin || linux

package adapter

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/natswire"
)

func natsAdmissionRoute(f amqpAdmissionFixture) config.NATSRoute {
	r := f.route()
	return config.NATSRoute{Principal: r.Principal, RequestSubject: "airc.request", ResponseSubject: "airc.response", Target: r.Target, Operations: r.Operations}
}

func natsAdmissionFrame(t *testing.T, f amqpAdmissionFixture, r core.Request, shift int64) natswire.Frame {
	t.Helper()
	// This existing helper produces protocol-independent signed JWS intent.
	body := stompAdmissionFrame(t, f, r, shift).Body
	return natswire.Frame{Command: "PUB", Subject: "airc.request", Reply: "airc.response", Payload: body}
}
