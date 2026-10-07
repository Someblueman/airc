package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestKafkaCredentialReferences(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	password, ca := filepath.Join(root, "password"), filepath.Join(root, "ca")
	for _, p := range []string{password, ca} {
		if err := os.WriteFile(p, []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	c := Defaults()
	c.StateDir = filepath.Join(root, "state")
	c.Kafka = []Kafka{{Credential: KafkaCredential{PasswordFile: password}, Brokers: []KafkaBroker{{TLSCAFile: ca}, {TLSCAFile: ca}}}}
	if err := c.CheckFiles(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(password, 0644); err != nil {
		t.Fatal(err)
	}
	if err := c.CheckFiles(); err == nil {
		t.Fatal("public password accepted")
	}
	if err := os.Chmod(password, 0600); err != nil {
		t.Fatal(err)
	}
	c.Kafka[0].Brokers[0].TLSCAFile = password
	if err := c.CheckFiles(); err == nil {
		t.Fatal("aliased password/trust reference accepted")
	}
	c.Kafka[0].Brokers[0].TLSCAFile = filepath.Join(root, "missing")
	if err := c.CheckFiles(); err == nil {
		t.Fatal("missing trust reference accepted")
	}
}
