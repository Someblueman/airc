//go:build darwin || linux

package adapter

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/stompwire"
)

func stompConnect(command, token string) stompwire.Frame {
	return stompwire.Frame{Command: command, Headers: []stompwire.Header{{Key: "accept-version", Value: "1.0,1.2"}, {Key: "host", Value: "fixture"}, {Key: "login", Value: "producer"}, {Key: "passcode", Value: token}, {Key: "heart-beat", Value: "2000,500"}}}
}

func TestSTOMPConnectAuthorityAndHeartbeats(t *testing.T) {
	v, token := amqpVerifierFixture(t)
	routes := []config.STOMPRoute{{Principal: "producer"}}
	for _, command := range []string{"CONNECT", "STOMP"} {
		identity, h, err := authenticateSTOMPFrame(v, stompConnect(command, token), routes, "fixture")
		if err != nil || identity.ID() != "producer" || identity.Binding() != "native" || h.send != time.Second || h.receive != 2*time.Second {
			t.Fatalf("%s: %+v %+v %v", command, identity, h, err)
		}
	}
	for name, mutate := range map[string]func(*stompwire.Frame){
		"command": func(f *stompwire.Frame) { f.Command = "SEND" }, "body": func(f *stompwire.Frame) { f.Body = []byte("x") },
		"version": func(f *stompwire.Frame) { f.Headers[0].Value = "1.1" }, "version-space": func(f *stompwire.Frame) { f.Headers[0].Value = " 1.2" },
		"host": func(f *stompwire.Frame) { f.Headers[1].Value = "other" }, "login": func(f *stompwire.Frame) { f.Headers[2].Value = "other" },
		"bearer":         func(f *stompwire.Frame) { f.Headers[3].Value = strings.Repeat("0", 64) },
		"short-bearer":   func(f *stompwire.Frame) { f.Headers[3].Value = "short" },
		"heartbeat-sign": func(f *stompwire.Frame) { f.Headers[4].Value = "+1,0" }, "heartbeat-space": func(f *stompwire.Frame) { f.Headers[4].Value = "1, 0" },
		"heartbeat-overflow": func(f *stompwire.Frame) { f.Headers[4].Value = "999999999999,0" }, "heartbeat-bound": func(f *stompwire.Frame) { f.Headers[4].Value = "10001,0" },
		"heartbeat-empty": func(f *stompwire.Frame) { f.Headers[4].Value = "0," }, "heartbeat-parts": func(f *stompwire.Frame) { f.Headers[4].Value = "0,0,0" },
	} {
		t.Run(name, func(t *testing.T) {
			f := stompConnect("CONNECT", token)
			mutate(&f)
			if identity, _, err := authenticateSTOMPFrame(v, f, routes, "fixture"); !errors.Is(err, auth.ErrAuthentication) || identity.ID() != "" {
				t.Fatal(identity, err)
			}
		})
	}
	if _, _, err := authenticateSTOMPFrame(v, stompConnect("CONNECT", token), []config.STOMPRoute{{Principal: "other"}}, "fixture"); err == nil {
		t.Fatal("ungranted principal")
	}
	for _, raw := range []string{"0,0", "10000,10000", "0,1000", "1000,0"} {
		f := stompConnect("CONNECT", token)
		f.Headers[4].Value = raw
		if _, _, err := authenticateSTOMPFrame(v, f, routes, "fixture"); err != nil {
			t.Fatal(raw, err)
		}
	}
}
