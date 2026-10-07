//go:build darwin || linux

package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func kafkaStoreFixture(t *testing.T) (config.Config, auth.Identity) {
	t.Helper()
	c, identity, _ := fixture(t)
	file := func(name string) string {
		path := filepath.Join(filepath.Dir(c.StateDir), name)
		if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	for i := range c.Principals {
		c.Principals[i].SigningKeys = []config.SigningKey{{ID: "signer-" + c.Principals[i].ID, PublicKeyFile: file("public-" + c.Principals[i].ID)}}
	}
	c.ResponseSigningKey = &config.ResponseSigningKey{ID: "response", PrivateKeyFile: file("response-key")}
	position := int64(23)
	c.Kafka = []config.Kafka{{ID: "owner", Namespace: "history", ClusterID: "cluster", Brokers: []config.KafkaBroker{{ID: 7, Address: "127.0.0.1:9093", TLSServerName: "localhost"}}, Credential: config.KafkaCredential{Username: "user", PasswordFile: file("kafka-password")}, Route: config.KafkaRoute{Principal: "device", Topic: "events", Partition: 2, InitialOffset: &position, ResultTopic: "results", ResultPartition: 3, Target: c.Principals[0].Targets[0], Operations: []core.Operation{core.Send}}}}
	return c, identity
}

func kafkaReadStoreFixture(t *testing.T) (config.Config, auth.Identity) {
	t.Helper()
	c, _ := kafkaStoreFixture(t)
	c.Principals[0].Operations = append(c.Principals[0].Operations, core.ContextRead)
	c.Kafka[0].Route.Operations = append(c.Kafka[0].Route.Operations, core.ContextRead)
	second := cloneKafkaOwners(c.Kafka)[0]
	second.ID = "second"
	second.Route.Partition++
	second.Route.ResultPartition++
	second.Credential.PasswordFile = filepath.Join(filepath.Dir(c.StateDir), "second-password")
	if err := os.WriteFile(second.Credential.PasswordFile, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	c.Kafka = append(c.Kafka, second)
	// Resolve a permission snapshot through the bearer fixture. The store APIs
	// consume trusted codec output; signature/dispatcher checks are separate.
	identityConfig := c
	identityConfig.Kafka = nil
	identityConfig.ResponseSigningKey = nil
	identityConfig.Principals = append(c.Principals[:0:0], c.Principals...)
	for i := range identityConfig.Principals {
		identityConfig.Principals[i].SigningKeys = nil
	}
	verifier, err := auth.New(identityConfig)
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.ReadToken(c.Principals[0].BearerTokenFile)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := verifier.Authenticate(token)
	if err != nil {
		t.Fatal(err)
	}
	return c, identity
}
