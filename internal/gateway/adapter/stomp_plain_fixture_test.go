//go:build darwin || linux

package adapter

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/stompwire"
)

// stompRequestFrame carries a plain contract request, exactly as an HTTP
// client would send it, naming the fixture's guest author, to the fixture
// route's request destination.
func stompRequestFrame(t *testing.T, r core.Request) stompwire.Frame {
	t.Helper()
	body, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatal(err)
	}
	fields["author"] = map[string]string{"nick": stompTestAuthor}
	if body, err = json.Marshal(fields); err != nil {
		t.Fatal(err)
	}
	return stompBodyFrame(body)
}

func stompBodyFrame(body []byte) stompwire.Frame {
	return stompwire.Frame{Command: "SEND", Headers: []stompwire.Header{{Key: "destination", Value: "request"}, {Key: "content-type", Value: "application/json"}, {Key: "content-length", Value: strconv.Itoa(len(body))}, {Key: "receipt", Value: "wire-receipt"}}, Body: body}
}
