//go:build darwin || linux

package gateway_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/pion/webrtc/v4"
)

type webRTCAssembly struct {
	total    int
	count    int
	parts    [][]byte
	received int
	deadline time.Time
}

func newWebRTCSession(t *testing.T, f webRTCFixture, mode string) *webRTCSession {
	return newWebRTCSessionWithICE(t, f, mode, webrtc.Configuration{})
}

func newWebRTCSessionWithICE(t *testing.T, f webRTCFixture, mode string, configuration webrtc.Configuration) *webRTCSession {
	t.Helper()
	pc, channel := webRTCDataChannelWithICE(t, f, mode, configuration)
	session := &webRTCSession{pc: pc, channel: channel, responses: make(chan []byte, 128), assemblies: make(map[[16]byte]*webRTCAssembly)}
	channel.OnMessage(func(message webrtc.DataChannelMessage) {
		data := append([]byte(nil), message.Data...)
		select {
		case session.responses <- data:
		default:
			_ = channel.Close()
		}
	})
	return session
}

func webRTCSessionFrame(t *testing.T, f webRTCFixture, session *webRTCSession, request core.Request, encrypted, stream bool) adapter.FrameResponse {
	t.Helper()
	profile := "plain"
	return webRTCSessionFrameToken(t, session, request, profile, f.credentials.token(t, request, encrypted), stream)
}

func webRTCSessionFrameToken(t *testing.T, session *webRTCSession, request core.Request, profile, requestBody string, stream bool) adapter.FrameResponse {
	t.Helper()
	body, err := json.Marshal(frameRequest{Version: 1, CorrelationID: "webrtc-" + request.RequestID, Profile: profile, Request: requestBody, Stream: stream})
	if err != nil {
		t.Fatal(err)
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		t.Fatal(err)
	}
	webRTCSessionSendBody(t, session, body, id)
	return webRTCReceiveFrame(t, session)
}

func webRTCSessionSendBody(t *testing.T, session *webRTCSession, body []byte, id [16]byte) {
	t.Helper()
	for _, fragment := range webTransportDatagrams(body, id) {
		if len(fragment) > 1000 {
			t.Fatalf("WebRTC AGD1 request fragment=%d", len(fragment))
		}
		if err := session.channel.Send(fragment); err != nil {
			t.Fatal(err)
		}
	}
}

func webRTCReceiveFrame(t *testing.T, session *webRTCSession) adapter.FrameResponse {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	for {
		select {
		case data, ok := <-session.responses:
			if !ok {
				t.Fatal("WebRTC DataChannel closed before response")
			}
			id, total, index, count, payload, err := webRTCResponseFragment(data)
			if err != nil {
				t.Fatalf("WebRTC AGD1 response fragment=%d", len(data))
			}
			now := time.Now()
			session.mu.Lock()
			for key, pending := range session.assemblies {
				if !now.Before(pending.deadline) {
					delete(session.assemblies, key)
				}
			}
			pending := session.assemblies[id]
			if pending == nil {
				if len(session.assemblies) >= 4 {
					session.mu.Unlock()
					t.Fatalf("WebRTC AGD1 response assembly bound exceeded")
				}
				pending = &webRTCAssembly{total: total, count: count, parts: make([][]byte, count), deadline: now.Add(5 * time.Second)}
				session.assemblies[id] = pending
			} else if pending.total != total || pending.count != count {
				delete(session.assemblies, id)
				session.mu.Unlock()
				continue
			}
			if prior := pending.parts[index]; prior != nil {
				if string(prior) != string(payload) {
					delete(session.assemblies, id)
				}
				session.mu.Unlock()
				continue
			}
			pending.parts[index] = append([]byte(nil), payload...)
			pending.received++
			if pending.received != pending.count {
				session.mu.Unlock()
				continue
			}
			parts := pending.parts
			delete(session.assemblies, id)
			session.mu.Unlock()
			body := make([]byte, 0, total)
			for _, part := range parts {
				body = append(body, part...)
			}
			if len(body) != total {
				t.Fatalf("WebRTC AGD1 response bytes=%d want=%d", len(body), total)
			}
			var response adapter.FrameResponse
			if err := json.Unmarshal(body, &response); err != nil {
				t.Fatal(err)
			}
			return response
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
}
