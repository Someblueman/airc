//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"crypto/tls"
	"io"
	"net/http"
	"testing"

	"github.com/Someblueman/airc/internal/testtls"
	"github.com/quic-go/quic-go/http3"
)

func http3BoundaryCall(t *testing.T, client *http.Client, address, path, token string, origins ...string) (int, []byte, error) {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://"+address+path, bytes.NewReader(rpcRequestBody(t, "http3-boundary", "airc.capabilities", map[string]any{})))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		setAuthorHeaders(request, token)
	}
	if len(origins) > 0 {
		for _, origin := range origins {
			request.Header.Add("Origin", origin)
		}
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	return response.StatusCode, body, readErr
}

func TestHTTP3RejectsAuthorOriginAndQueryBoundaries(t *testing.T) {
	f := newHTTP3GatewayFixture(t)
	for _, test := range []struct {
		name    string
		path    string
		token   string
		origins []string
		want    int
	}{
		// Without author headers the request is answered under the
		// listener's policy; a malformed nickname is refused.
		{name: "missing author", want: http.StatusOK},
		{name: "invalid nickname", token: "bad nick", want: http.StatusBadRequest},
		{name: "unconfigured origin", token: f.token, origins: []string{"https://unconfigured.example"}, want: http.StatusForbidden},
		{name: "query", path: "/v1/rpc?unexpected=1", token: f.token, want: http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := test.path
			if path == "" {
				path = "/v1/rpc"
			}
			status, body, err := http3BoundaryCall(t, f.client, f.address, path, test.token, test.origins...)
			if err != nil {
				t.Fatal(err)
			}
			if status != test.want {
				t.Fatalf("status=%d want=%d body=%s", status, test.want, body)
			}
		})
	}

	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://"+f.address+"/v1/rpc", bytes.NewReader(rpcRequestBody(t, "http3-duplicate", "airc.capabilities", map[string]any{})))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Add("AIRC-Nick", fixtureAuthor)
	request.Header.Add("AIRC-Nick", fixtureAuthor)
	request.Header.Set("Content-Type", "application/json")
	response, err := f.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		data, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		t.Fatalf("duplicate author header status=%d body=%s", response.StatusCode, data)
	}
}

func TestHTTP3RejectsWrongTLSAuthority(t *testing.T) {
	f := newHTTP3GatewayFixture(t)
	wrongCA := testtls.New(t)
	tests := []struct {
		name string
		cfg  *tls.Config
	}{
		{name: "untrusted CA", cfg: wrongCA.Client.Clone()},
		{name: "wrong server name", cfg: func() *tls.Config {
			cfg := f.cert.Client.Clone()
			cfg.ServerName = "wrong.example"
			return cfg
		}()},
		{name: "TLS 1.2", cfg: func() *tls.Config {
			cfg := f.cert.Client.Clone()
			cfg.MinVersion, cfg.MaxVersion = tls.VersionTLS12, tls.VersionTLS12
			return cfg
		}()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transport := &http3.Transport{TLSClientConfig: test.cfg}
			defer transport.Close()
			client := &http.Client{Transport: transport}
			_, _, err := http3BoundaryCall(t, client, f.address, "/v1/rpc", f.token)
			if err == nil {
				t.Fatal("TLS boundary unexpectedly succeeded")
			}
		})
	}
}
