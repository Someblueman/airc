//go:build darwin || linux

package adapter

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func newKafkaAdmissionFixture(t *testing.T) amqpAdmissionFixture {
	t.Helper()
	f := newAMQPAdmissionFixture(t)
	if err := f.s.Close(); err != nil {
		t.Fatal(err)
	}
	secret := func(name string) string {
		path := filepath.Join(filepath.Dir(f.c.StateDir), name)
		if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	// These references satisfy store configuration; this fixture signs requests
	// with the existing real JWS key but does not load a response signer or broker.
	f.c.ResponseSigningKey = &config.ResponseSigningKey{ID: "response", PrivateKeyFile: secret("kafka-response")}
	position := int64(23)
	route := f.route()
	f.c.Kafka = []config.Kafka{{ID: "owner", Namespace: "history", ClusterID: "cluster", Brokers: []config.KafkaBroker{{ID: 7, Address: "127.0.0.1:9093", TLSServerName: "localhost"}}, Credential: config.KafkaCredential{Username: "user", PasswordFile: secret("kafka-password")}, Route: config.KafkaRoute{Principal: route.Principal, Topic: "events", Partition: 2, InitialOffset: &position, ResultTopic: "results", ResultPartition: 3, Target: route.Target, Operations: route.Operations}}}
	var err error
	f.s, err = store.Open(t.Context(), f.c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.s.Close() })
	f.d, err = dispatch.New(f.s, f.v, f.reader, f.cursors, route.Operations)
	if err != nil {
		t.Fatal(err)
	}
	return f
}
