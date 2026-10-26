//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
)

type webRTCSession struct {
	pc         *webrtc.PeerConnection
	channel    *webrtc.DataChannel
	responses  chan []byte
	mu         sync.Mutex
	assemblies map[[16]byte]*webRTCAssembly
}

type webRTCSignal struct {
	Version int    `json:"version"`
	SDP     string `json:"sdp"`
	Mode    string `json:"mode"`
}

func webRTCDataChannel(t *testing.T, f webRTCFixture, mode string) (*webrtc.PeerConnection, *webrtc.DataChannel) {
	return webRTCDataChannelWithICE(t, f, mode, webrtc.Configuration{})
}

func webRTCDataChannelWithICE(t *testing.T, f webRTCFixture, mode string, configuration webrtc.Configuration) (*webrtc.PeerConnection, *webrtc.DataChannel) {
	pc, channel, err := webRTCDataChannelWithICEAttempt(t, f, mode, configuration, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	return pc, channel
}

func webRTCDataChannelWithICEAttempt(t *testing.T, f webRTCFixture, mode string, configuration webrtc.Configuration, timeout time.Duration) (*webrtc.PeerConnection, *webrtc.DataChannel, error) {
	t.Helper()
	var setting webrtc.SettingEngine
	setting.SetSCTPMaxReceiveBufferSize(64 << 10)
	api := webrtc.NewAPI(webrtc.WithSettingEngine(setting))
	pc, err := api.NewPeerConnection(configuration)
	if err != nil {
		return nil, nil, err
	}
	ordered := mode == "reliable_ordered" || mode == "unreliable_ordered"
	var init webrtc.DataChannelInit
	init.Protocol = func() *string { v := "agd1"; return &v }()
	init.Ordered = &ordered
	if mode == "unreliable_ordered" || mode == "unreliable_unordered" {
		max := uint16(0)
		init.MaxRetransmits = &max
	}
	channel, err := pc.CreateDataChannel("airc-gateway-v1", &init)
	if err != nil {
		_ = pc.Close()
		return nil, nil, err
	}
	opened := make(chan struct{})
	channel.OnOpen(func() { close(opened) })
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		_ = pc.Close()
		return nil, nil, err
	}
	if err := pc.SetLocalDescription(offer); err != nil {
		_ = pc.Close()
		return nil, nil, err
	}
	select {
	case <-webrtc.GatheringCompletePromise(pc):
	case <-time.After(timeout):
		_ = pc.Close()
		return nil, nil, fmt.Errorf("WebRTC ICE gathering timed out")
	}
	local := pc.LocalDescription()
	if local == nil {
		_ = pc.Close()
		return nil, nil, fmt.Errorf("missing WebRTC local description")
	}
	body, err := json.Marshal(webRTCSignal{Version: 1, SDP: local.SDP, Mode: mode})
	if err != nil {
		_ = pc.Close()
		return nil, nil, err
	}
	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+f.address+"/v1/webrtc/offer", bytes.NewReader(body))
	if err != nil {
		_ = pc.Close()
		return nil, nil, err
	}
	if f.token != "" {
		// Without a authorSpec token the offer opens an author peer.
		setAuthorHeaders(request, f.token)
	}
	request.Header.Set("Content-Type", "application/json")
	transport := &http.Transport{TLSClientConfig: f.cert.Client.Clone()}
	client := &http.Client{Transport: transport}
	defer transport.CloseIdleConnections()
	response, err := client.Do(request)
	if err != nil {
		_ = pc.Close()
		return nil, nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(response.Body, 4097))
		_ = pc.Close()
		return nil, nil, fmt.Errorf("WebRTC offer status=%d body=%q", response.StatusCode, data)
	}
	var answer webRTCSignal
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&answer); err != nil || answer.Version != 1 || answer.SDP == "" {
		_ = pc.Close()
		return nil, nil, fmt.Errorf("invalid WebRTC answer=%+v err=%v", answer, err)
	}
	if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: answer.SDP}); err != nil {
		_ = pc.Close()
		return nil, nil, err
	}
	select {
	case <-opened:
	case <-time.After(timeout):
		_ = pc.Close()
		return nil, nil, fmt.Errorf("WebRTC DataChannel did not open")
	}
	return pc, channel, nil
}
