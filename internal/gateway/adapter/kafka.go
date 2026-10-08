package adapter

import (
	"context"
	"crypto/tls"
	"slices"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// ServeKafka owns one configured request partition and its fixed result route.
// Reconnect owns credential snapshots and joins every socket before returning.
func ServeKafka(ctx context.Context, b config.Kafka, security map[int32]*tls.Config, password []byte, realm string, d *dispatch.Dispatcher, journal *store.Store, sealer *auth.ResponseSealer, slots, requests chan struct{}) error {
	if d == nil || journal == nil || sealer == nil || realm == "" || cap(requests) < 1 || cap(requests) > 256 {
		return errKafkaConnection
	}
	b.Brokers = slices.Clone(b.Brokers)
	b.Route.Operations = slices.Clone(b.Route.Operations)
	if b.Route.InitialOffset != nil {
		initial := *b.Route.InitialOffset
		b.Route.InitialOffset = &initial
	}
	return runKafkaConnections(ctx, b, security, password, slots, func(p *kafkaPair) bool {
		return runKafkaSession(ctx, p, realm, b, d, journal, sealer, requests)
	})
}
