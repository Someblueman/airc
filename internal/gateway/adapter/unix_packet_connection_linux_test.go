//go:build linux

package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestUnixPacketEncryptedProfileCapacityAndJoinedCancellation(t *testing.T) {
	server, client := unixPacketPair(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	requests := make(chan struct{}, 1)
	requests <- struct{}{}
	done := make(chan error, 1)
	go func() { done <- serveUnixPacketConnection(ctx, server, nil, requests) }()
	joined := false
	t.Cleanup(func() {
		cancel()
		_ = client.Close()
		if !joined {
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Error("record connection cleanup did not join")
			}
		}
	})
	for _, test := range []struct {
		profile string
		code    core.ErrorCode
	}{
		{"signed", core.InvalidRequest},
		{"encrypted", core.CapacityExceeded},
	} {
		body, err := json.Marshal(FrameRequest{Version: 1, CorrelationID: test.profile, Profile: test.profile, Envelope: "unused"})
		if err != nil {
			t.Fatal(err)
		}
		if err := client.SetDeadline(time.Now().Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		if n, _, err := client.WriteMsgUnix(body, nil, nil); err != nil || n != len(body) {
			t.Fatal("send", n, err)
		}
		data := make([]byte, MaxResponseFrameBytes)
		n, _, _, _, err := client.ReadMsgUnix(data, nil)
		if err != nil {
			t.Fatal(err)
		}
		var response FrameResponse
		if err := json.Unmarshal(data[:n], &response); err != nil || response.Error == nil || response.Error.Code != test.code {
			t.Fatal("profile/capacity guard", response, err)
		}
	}
	cancel()
	select {
	case <-done:
		joined = true
	case <-time.After(time.Second):
		t.Fatal("record connection did not join cancellation")
	}
	if len(requests) != 1 {
		t.Fatal("record refusal changed someone else's request slot")
	}
}

func TestUnixPacketMalformedRecordsCannotBeCombined(t *testing.T) {
	valid := []byte(`{"version":1,"correlation_id":"split","profile":"encrypted","envelope":"unused"}`)
	for _, body := range [][]byte{
		valid[:len(valid)/2],
		append([]byte{0, 0, 0, byte(len(valid))}, valid...),
		[]byte(`{"version":1,"version":1}`),
		append(append([]byte(nil), valid...), 0xff),
		append(append([]byte(nil), valid...), bytes.Repeat([]byte(" "), MaxRequestFrameBytes+1-len(valid))...),
	} {
		server, client := unixPacketPair(t)
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- serveUnixPacketConnection(ctx, server, nil, make(chan struct{}, 1)) }()
		joined := false
		t.Cleanup(func() {
			cancel()
			_ = client.Close()
			if !joined {
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Error("malformed record cleanup did not join")
				}
			}
		})
		if err := client.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		if _, _, err := client.WriteMsgUnix(body, nil, nil); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-done:
			joined = true
			if !errors.Is(err, store.ErrInvalid) {
				t.Fatal("wrong record refusal", err)
			}
		case <-time.After(time.Second):
			cancel()
			t.Fatal("malformed record did not close promptly")
		}
		cancel()
	}
}
