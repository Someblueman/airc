package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/testtls"
)

func TestSecureTransportDiscoveryDoesNotStartRuntime(t *testing.T) {
	for _, protocol := range []string{"rawquic", "rawquic_datagrams", "dtls", "coaps"} {
		t.Run(protocol, func(t *testing.T) {
			root := t.TempDir()
			state := filepath.Join(root, "state")
			path := filepath.Join(root, "config.json")
			cert := testtls.New(t)
			data := `{"version":1,"realm":"fixture","state_dir":"` + state + `","listeners":[{"id":"quic","protocol":"` + protocol + `","enabled":true,"tls_cert_file":"` + cert.CertPath + `","tls_key_file":"` + cert.KeyPath + `","operations":["send","receipt.get"]}]}`
			if err := os.WriteFile(path, []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			if err := run([]string{"capabilities", "--file", path}, &out); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), `"status":"enabled_`+protocol+`_partial"`) || !strings.Contains(out.String(), `"operations":["send","receipt.get"]`) || strings.Contains(out.String(), root) {
				t.Fatal(out.String())
			}
			if _, err := os.Stat(state); !os.IsNotExist(err) {
				t.Fatal("discovery created state", err)
			}
		})
	}
}
