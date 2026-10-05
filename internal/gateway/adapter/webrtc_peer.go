package adapter

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/jsoninput"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/pion/webrtc/v4"
)

type webrtcPeer struct {
	adapter   *WebRTC
	principal string
	binding   string
	ctx       context.Context
	cancel    context.CancelFunc

	mu       sync.Mutex
	pc       *webrtc.PeerConnection
	closed   bool
	channel  *webrtc.DataChannel
	done     chan struct{}
	once     sync.Once
	work     sync.WaitGroup
	created  atomic.Int64
	activity atomic.Int64
	opened   atomic.Bool
}

func (p *webrtcPeer) answer(parent context.Context, offer webrtcOffer) (webrtcAnswer, error) {
	policy := webrtc.NewICETransportPolicy(p.adapter.options.ICETransportPolicy)
	iceServers := make([]webrtc.ICEServer, 0, len(p.adapter.options.ICEServers))
	for _, configured := range p.adapter.options.ICEServers {
		credential, err := readWebRTCCredential(configured.CredentialFile)
		if err != nil {
			return webrtcAnswer{}, err
		}
		iceServers = append(iceServers, webrtc.ICEServer{URLs: append([]string(nil), configured.URLs...), Username: configured.Username, Credential: credential})
	}
	setting := webrtc.SettingEngine{}
	setting.SetSCTPMaxMessageSize(agd1DatagramBytes)
	setting.SetSCTPMaxReceiveBufferSize(64 << 10)
	// ICE/DTLS/SCTP packets include headers around the 1000-byte application
	// fragment; the network receive buffer must not truncate those packets.
	setting.SetReceiveMTU(2048)
	api := webrtc.NewAPI(webrtc.WithSettingEngine(setting))
	pc, err := api.NewPeerConnection(webrtc.Configuration{ICEServers: iceServers, ICETransportPolicy: policy})
	if err != nil {
		return webrtcAnswer{}, err
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		_ = pc.Close()
		return webrtcAnswer{}, context.Canceled
	}
	p.pc = pc
	p.mu.Unlock()
	pc.OnDataChannel(func(channel *webrtc.DataChannel) { p.onDataChannel(channel, offer.Mode) })
	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		if state == webrtc.PeerConnectionStateFailed || state == webrtc.PeerConnectionStateClosed || state == webrtc.PeerConnectionStateDisconnected {
			p.close()
		}
	})
	remote := webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: offer.SDP}
	if err := pc.SetRemoteDescription(remote); err != nil {
		return webrtcAnswer{}, err
	}
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		return webrtcAnswer{}, err
	}
	if err := pc.SetLocalDescription(answer); err != nil {
		return webrtcAnswer{}, err
	}
	gather := webrtc.GatheringCompletePromise(pc)
	select {
	case <-p.done:
		return webrtcAnswer{}, context.Canceled
	case <-gather:
	case <-time.After(webRTCGatherTimeout):
		return webrtcAnswer{}, context.DeadlineExceeded
	case <-parent.Done():
		return webrtcAnswer{}, parent.Err()
	}
	local := pc.LocalDescription()
	if local == nil || len(local.SDP) > maxWebRTCSDPBytes {
		return webrtcAnswer{}, store.ErrCapacity
	}
	return webrtcAnswer{Version: 1, SDP: local.SDP}, nil
}

func (p *webrtcPeer) start() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	created := time.Now()
	p.created.Store(created.UnixNano())
	p.activity.Store(created.UnixNano())
	p.work.Add(1)
	p.mu.Unlock()
	go func() {
		defer p.work.Done()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-p.done:
				return
			case now := <-ticker.C:
				created := time.Unix(0, p.created.Load())
				if (!p.opened.Load() && now.Sub(created) >= webRTCPeerTimeout) || now.Sub(created) >= webRTCPeerLifetime || now.Sub(time.Unix(0, p.activity.Load())) >= 30*time.Second {
					p.close()
					return
				}
			}
		}
	}()
}

func (p *webrtcPeer) handshakeDone() { p.work.Done() }

func (p *webrtcPeer) wait() { p.work.Wait() }

func (p *webrtcPeer) touch() { p.activity.Store(time.Now().UnixNano()) }

func (p *webrtcPeer) write(parent context.Context, channel *webrtc.DataChannel, response FrameResponse) error {
	body, err := json.Marshal(response)
	if err != nil || len(body) > MaxResponseFrameBytes {
		return store.ErrCapacity
	}
	fragments, err := fragmentAGD1(body)
	if err != nil {
		return store.ErrCapacity
	}
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	for _, fragment := range fragments {
		for channel.BufferedAmount()+uint64(len(fragment)) > 64<<10 {
			select {
			case <-ctx.Done():
				p.close()
				return ctx.Err()
			case <-time.After(10 * time.Millisecond):
			}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := channel.Send(fragment); err != nil {
			return err
		}
	}
	if response.Result != nil || response.Page != nil {
		p.touch()
	}
	return nil
}

func (p *webrtcPeer) onDataChannel(channel *webrtc.DataChannel, mode string) {
	if channel.Label() != WebRTCDataChannelLabel || channel.Protocol() != WebRTCDataChannelProtocol || !validWebRTCChannelMode(mode, channel) {
		_ = channel.Close()
		p.close()
		return
	}
	p.mu.Lock()
	duplicate := p.channel != nil
	if p.closed || duplicate {
		p.mu.Unlock()
		_ = channel.Close()
		if duplicate {
			p.close()
		}
		return
	}
	p.channel = channel
	p.mu.Unlock()
	in := make(chan []byte, 32)
	channel.OnMessage(func(message webrtc.DataChannelMessage) {
		if message.IsString {
			p.close()
			return
		}
		if len(message.Data) > agd1DatagramBytes {
			p.close()
			return
		}
		copyData := append([]byte(nil), message.Data...)
		select {
		case in <- copyData:
		case <-p.done:
		default:
			p.close()
		}
	})
	channel.OnOpen(func() {
		p.opened.Store(true)
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			_ = channel.Close()
			return
		}
		p.work.Add(1)
		p.mu.Unlock()
		go func() {
			defer p.work.Done()
			p.serveChannel(channel, in)
		}()
	})
	channel.OnClose(func() { p.close() })
}

func (p *webrtcPeer) close() {
	p.once.Do(func() {
		p.mu.Lock()
		p.closed = true
		pc := p.pc
		channel := p.channel
		close(p.done)
		cancel := p.cancel
		p.mu.Unlock()
		cancel()
		if channel != nil {
			_ = channel.Close()
		}
		if pc != nil {
			_ = pc.Close()
		}
	})
}

func validWebRTCChannelMode(mode string, channel *webrtc.DataChannel) bool {
	if !validWebRTCMode(mode) {
		return false
	}
	reliable := channel.MaxRetransmits() == nil && channel.MaxPacketLifeTime() == nil
	if mode == "reliable_ordered" {
		return reliable && channel.Ordered()
	}
	if mode == "reliable_unordered" {
		return reliable && !channel.Ordered()
	}
	unreliable := channel.MaxPacketLifeTime() == nil && channel.MaxRetransmits() != nil && *channel.MaxRetransmits() == 0
	if mode == "unreliable_ordered" {
		return unreliable && channel.Ordered()
	}
	return unreliable && !channel.Ordered()
}

func (p *webrtcPeer) serveChannel(channel *webrtc.DataChannel, in <-chan []byte) {
	ctx := p.ctx
	writes := make(chan struct{}, 1)
	active := make(map[string]bool)
	var activeMu sync.Mutex
	var work sync.WaitGroup
	assembler := newDatagramAssembler()
	for {
		select {
		case <-p.done:
			work.Wait()
			return
		case data, ok := <-in:
			if !ok {
				work.Wait()
				return
			}
			body, _ := assembler.add(data, time.Now())
			if body == nil {
				continue
			}
			var request FrameRequest
			if jsoninput.Decode(body, &request, MaxRequestFrameBytes) != nil || request.Version != 1 || !correlationID(request.CorrelationID) || request.Envelope == "" || request.Profile != "signed" && request.Profile != "encrypted" {
				p.close()
				continue
			}
			verified, err := p.adapter.d.AuthenticateDetached(request.Envelope, request.Profile == "encrypted", time.Now())
			if err != nil || verified.PrincipalID() != p.principal || verified.Binding() != p.binding {
				p.close()
				continue
			}
			activeMu.Lock()
			duplicate := active[request.CorrelationID]
			full := len(active) >= 4
			if !duplicate && !full {
				active[request.CorrelationID] = true
			}
			activeMu.Unlock()
			if duplicate || full {
				p.close()
				continue
			}
			select {
			case p.adapter.requests <- struct{}{}:
			default:
				activeMu.Lock()
				delete(active, request.CorrelationID)
				activeMu.Unlock()
				continue
			}
			work.Add(1)
			go func(request FrameRequest, verified dispatch.VerifiedMessage) {
				defer work.Done()
				defer func() {
					<-p.adapter.requests
					activeMu.Lock()
					delete(active, request.CorrelationID)
					activeMu.Unlock()
				}()
				timeout := 4 * time.Second
				if request.Stream {
					timeout = 20 * time.Second
				}
				requestCtx, finish := context.WithTimeout(ctx, timeout)
				defer finish()
				write := func(response FrameResponse) error {
					select {
					case writes <- struct{}{}:
						defer func() { <-writes }()
					case <-requestCtx.Done():
						return requestCtx.Err()
					}
					return p.write(requestCtx, channel, response)
				}
				runCorrelatedVerifiedRequest(requestCtx, write, []string{"long_poll", "webrtc_" + channelMode(channel)}, true, p.adapter.d, request, verified)
			}(request, verified)
		}
	}
}

func channelMode(channel *webrtc.DataChannel) string {
	if channel.MaxRetransmits() != nil && *channel.MaxRetransmits() == 0 {
		if channel.Ordered() {
			return "unreliable_ordered"
		}
		return "unreliable_unordered"
	}
	if channel.Ordered() {
		return "reliable_ordered"
	}
	return "reliable_unordered"
}
