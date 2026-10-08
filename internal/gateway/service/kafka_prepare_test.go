//go:build darwin || linux

package service

import (
	"bytes"
	"crypto/tls"
	"os"
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/internal/testtls"
)

func kafkaServiceConfig(t *testing.T) (config.Config, testtls.Certificate) {
	t.Helper()
	c, cert, _, _ := natsServiceConfig(t)
	c.Listeners = []config.Listener{}
	password := filepath.Join(filepath.Dir(c.StateDir), "kafka-password")
	if err := os.WriteFile(password, []byte("password"), 0600); err != nil {
		t.Fatal(err)
	}
	initial := int64(23)
	c.Kafka = []config.Kafka{{ID: "kafka", Enabled: true, Namespace: "history", ClusterID: "cluster", Brokers: []config.KafkaBroker{{ID: 7, Address: "127.0.0.1:9093", TLSServerName: "localhost", TLSCAFile: cert.CAPath}}, Credential: config.KafkaCredential{Username: "user", PasswordFile: password}, Route: config.KafkaRoute{Principal: c.Principals[0].ID, Topic: "events", Partition: 2, InitialOffset: &initial, ResultTopic: "results", ResultPartition: 3, Target: c.Principals[0].Targets[0], Operations: []core.Operation{core.Send}}}}
	return c, cert
}

func TestKafkaPreparationSnapshotsAndClears(t *testing.T) {
	c, _ := kafkaServiceConfig(t)
	v, err := auth.New(c)
	if err != nil {
		t.Fatal(err)
	}
	b, err := backend.New(c)
	if err != nil {
		t.Fatal(err)
	}
	journal, err := store.Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	owners, err := prepareKafka(t.Context(), c, journal, v, b, nil)
	if err != nil || len(owners) != 1 {
		t.Fatal(err)
	}
	o := owners[0]
	defer o.clear()
	security := o.security[7]
	if !bytes.Equal(o.password, []byte("password")) || security.MinVersion != tls.VersionTLS13 || security.InsecureSkipVerify || security.RootCAs == nil || o.dispatcher == nil || o.sealer == nil {
		t.Fatal("incomplete preparation")
	}
	c.Kafka[0].Brokers[0].Address = "127.0.0.1:1"
	c.Kafka[0].Route.Operations[0] = core.Search
	*c.Kafka[0].Route.InitialOffset = 100
	if o.options.Brokers[0].Address != "127.0.0.1:9093" || o.options.Route.Operations[0] != core.Send || *o.options.Route.InitialOffset != 23 {
		t.Fatal("mutable config alias")
	}
	if err := os.WriteFile(c.Kafka[0].Credential.PasswordFile, []byte("rotated"), 0600); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(o.password, []byte("password")) {
		t.Fatal("credential changed")
	}
	held := o.password
	o.clear()
	if !bytes.Equal(held, make([]byte, len(held))) {
		t.Fatal("credential retained")
	}
	c.Kafka[0].Enabled = false
	owners, err = prepareKafka(t.Context(), c, journal, v, b, nil)
	if err != nil || len(owners) != 0 {
		t.Fatal("disabled preparation", err)
	}
}

func TestKafkaStartupFailureNoReadyAndJournalReleased(t *testing.T) {
	for _, kind := range []string{"empty_password", "nul_password", "utf8_password", "trust", "signer"} {
		t.Run(kind, func(t *testing.T) {
			c, cert := kafkaServiceConfig(t)
			path, content := c.Kafka[0].Credential.PasswordFile, []byte{}
			switch kind {
			case "nul_password":
				content = []byte{'p', 0}
			case "utf8_password":
				content = []byte{255}
			case "trust":
				path, content = cert.CAPath, []byte("invalid CA")
			case "signer":
				path, content = c.ResponseSigningKey.PrivateKeyFile, []byte("invalid signer")
			}
			if err := os.WriteFile(path, content, 0600); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			if err := Run(t.Context(), c, &out); err == nil || out.Len() != 0 {
				t.Fatal("failed startup claimed ready", err)
			}
			journal, err := store.Open(t.Context(), c)
			if err != nil {
				t.Fatal("journal retained", err)
			}
			if err := journal.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
