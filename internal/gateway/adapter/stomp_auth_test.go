//go:build darwin || linux

package adapter

import (
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/stompwire"
)

func stompConnect(command, password string) stompwire.Frame {
	return stompwire.Frame{Command: command, Headers: []stompwire.Header{{Key: "accept-version", Value: "1.0,1.2"}, {Key: "host", Value: "fixture"}, {Key: "login", Value: "producer"}, {Key: "passcode", Value: password}, {Key: "heart-beat", Value: "2000,500"}}}
}

func TestSTOMPConnectPasscodeAndHeartbeats(t *testing.T) {
	for _, command := range []string{"CONNECT", "STOMP"} {
		password, h, err := parseSTOMPConnect(stompConnect(command, "letmein"))
		if err != nil || password.Empty() || h.send != time.Second || h.receive != 2*time.Second {
			t.Fatalf("%s: %+v %v", command, h, err)
		}
	}
	// Login and host carry no authority; any host is accepted.
	f := stompConnect("CONNECT", "")
	f.Headers[1].Value, f.Headers[2].Value = "any-host.example", "someone"
	if password, _, err := parseSTOMPConnect(f); err != nil || !password.Empty() {
		t.Fatal("host or login refused", err)
	}
	for name, mutate := range map[string]func(*stompwire.Frame){
		"command": func(f *stompwire.Frame) { f.Command = "SEND" }, "body": func(f *stompwire.Frame) { f.Body = []byte("x") },
		"version": func(f *stompwire.Frame) { f.Headers[0].Value = "1.1" }, "version-space": func(f *stompwire.Frame) { f.Headers[0].Value = " 1.2" },
		"passcode":       func(f *stompwire.Frame) { f.Headers[3].Value = "bad\x01code" },
		"heartbeat-sign": func(f *stompwire.Frame) { f.Headers[4].Value = "+1,0" }, "heartbeat-space": func(f *stompwire.Frame) { f.Headers[4].Value = "1, 0" },
		"heartbeat-overflow": func(f *stompwire.Frame) { f.Headers[4].Value = "999999999999,0" },
		"heartbeat-empty":    func(f *stompwire.Frame) { f.Headers[4].Value = "0," }, "heartbeat-parts": func(f *stompwire.Frame) { f.Headers[4].Value = "0,0,0" },
	} {
		t.Run(name, func(t *testing.T) {
			f := stompConnect("CONNECT", "letmein")
			mutate(&f)
			_, _, err := parseSTOMPConnect(f)
			if message, ok := stompRefusalMessage(err); !ok || message == "" {
				t.Fatal("refusal without an ERROR message", err)
			}
		})
	}
	for raw, want := range map[string]time.Duration{"0,0": 0, "10000,10000": 10 * time.Second, "0,1000": time.Second, "1000,0": 0, "120000,0": 0} {
		f := stompConnect("CONNECT", "letmein")
		f.Headers[4].Value = raw
		if _, h, err := parseSTOMPConnect(f); err != nil || h.send != want {
			t.Fatal(raw, h, err)
		}
	}
}
