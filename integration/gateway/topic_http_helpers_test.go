//go:build darwin || linux

package gateway_test

import (
	"io"
	"net/http"
	"testing"
)

func topicHeaderGET(t *testing.T, f *reactionTransportFixture, path string) (int, []byte) {
	return topicHeaderGETAuth(t, f, path, f.authorSpec)
}

func topicHeaderGETAuth(t *testing.T, f *reactionTransportFixture, path, token string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, f.httpURL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	setAuthorHeaders(req, token)
	resp, err := f.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	wantProto := 1
	if f.http.Transport.(*http.Transport).ForceAttemptHTTP2 {
		wantProto = 2
	}
	if resp.ProtoMajor != wantProto {
		t.Fatalf("topic HTTP protocol=%s want major %d", resp.Proto, wantProto)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, body
}
