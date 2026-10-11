package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/testtls"
)

func TestUARTCapabilitiesAndCommandRejectsNonDevice(t *testing.T) {
	if !config.Available("unix") {
		t.Skip("UART requires Darwin or Linux")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cert := testtls.New(t)
	device := filepath.Join(root, "regular-file")
	if err := os.WriteFile(device, []byte("preserve this file"), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "config.json")
	data := fmt.Sprintf(`{"version":1,"realm":"fixture","state_dir":%q,"listeners":[{"id":"uart","protocol":"uart","address":%q,"enabled":true,"tls_cert_file":%q,"tls_key_file":%q,"operations":["send","receipt.get"]}]}`, filepath.Join(root, "state"), device, cert.CertPath, cert.KeyPath)
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run([]string{"capabilities", "--file", path}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"status":"enabled_uart_partial"`) || strings.Contains(out.String(), root) {
		t.Fatal("incorrect or path-leaking UART capabilities", out.String())
	}
	if err := run([]string{"serve-uart", "--file", path}, &out); err == nil {
		t.Fatal("UART command accepted a regular file")
	}
	if got, err := os.ReadFile(device); err != nil || string(got) != "preserve this file" {
		t.Fatal("UART command altered its rejected path", err)
	}
}
