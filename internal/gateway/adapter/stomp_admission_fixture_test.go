//go:build darwin || linux

package adapter

import (
	"github.com/Someblueman/airc/internal/gateway/config"
)

func stompAdmissionRoute(f amqpAdmissionFixture) config.STOMPRoute {
	r := f.route()
	return config.STOMPRoute{Principal: r.Principal, RequestDestination: r.RequestAddress, ResponseDestination: r.ResponseAddress, Target: r.Target, Operations: r.Operations}
}
