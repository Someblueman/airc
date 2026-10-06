// Package service owns configured ingress, delivery workers and journal lifetime.
package service

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/delivery"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// Run binds only explicitly enabled instances. Any preparation/bind failure
// closes all owned resources and never emits a ready record. No upstream account
// is provisioned and no dial occurs before a journal-authorized lease exists.
func Run(ctx context.Context, c config.Config, out io.Writer) (failure error) {
	if err := c.Validate(); err != nil {
		return err
	}
	if err := c.CheckFiles(); err != nil {
		return err
	}
	for _, l := range c.Listeners {
		if l.Enabled && l.Protocol == "file_mailbox" {
			return errors.New("file mailbox requires mailbox-once")
		}
		if l.Enabled && l.Protocol == "stdio" {
			return errors.New("stdio listener requires serve-stdio")
		}
	}
	v, err := auth.New(c)
	if err != nil {
		return err
	}
	b, err := backend.New(c)
	if err != nil {
		return err
	}
	cursors, err := auth.NewCursorCodec(c.CursorKeyFile)
	if err != nil {
		return err
	}
	certificates := map[string]tls.Certificate{}
	enabled := 0
	for _, l := range c.Listeners {
		if !l.Enabled {
			continue
		}
		enabled++
		if l.TLSCertFile != "" {
			pair, err := auth.ServerCertificate(l.TLSCertFile, l.TLSKeyFile)
			if err != nil {
				return err
			}
			certificates[l.ID] = pair
		}
	}
	if enabled == 0 {
		return errors.New("no enabled listeners")
	}
	s, err := store.Open(ctx, c)
	if err != nil {
		return err
	}
	defer func() { failure = errors.Join(failure, s.Close()) }()
	type instance struct {
		oscore         oscoreOwner
		server         *http.Server
		quic           *quicOwner
		dtls           *dtlsOwner
		packet         *packetOwner
		mcp            *adapter.MCPHTTP
		webrtc         *adapter.WebRTC
		listener       net.Listener
		dispatcher     *dispatch.Dispatcher
		encryptedOnly  bool
		coapTCP        bool
		coapProtection *adapter.CoAPResponseProtection
	}
	instances := make([]instance, 0, enabled)
	defer func() {
		for _, i := range instances {
			if i.oscore != nil {
				_ = i.oscore.Close()
			}
			if i.webrtc != nil {
				_ = i.webrtc.Close()
			}
			if i.packet != nil {
				_ = i.packet.close()
			}
			if i.dtls != nil {
				_ = i.dtls.close()
			}
			if i.quic != nil {
				closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				_ = i.quic.close(closeCtx)
				cancel()
			}
			if i.mcp != nil {
				i.mcp.Close()
			}
			if i.server != nil {
				_ = i.server.Close()
			}
			if i.listener != nil {
				_ = i.listener.Close()
			}
		}
	}()
	state := struct {
		Version   int    `json:"version"`
		State     string `json:"state"`
		Listeners []struct {
			ID      string `json:"id"`
			Address string `json:"address"`
		} `json:"listeners"`
	}{Version: 1, State: "ready"}
	slots := make(chan struct{}, c.Limits.Sessions)
	requests := make(chan struct{}, c.Limits.Sessions)
	var owned handlers
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	for _, l := range c.Listeners {
		if !l.Enabled {
			continue
		}
		d, err := dispatch.New(s, v, b, cursors, l.Operations)
		if err != nil {
			return err
		}
		if l.Protocol == "coap_oscore" {
			owner, err := startOSCORE(workCtx, c, l, s, d, requests)
			if err != nil {
				return err
			}
			instances = append(instances, instance{oscore: owner, dispatcher: d})
			state.Listeners = append(state.Listeners, struct {
				ID      string `json:"id"`
				Address string `json:"address"`
			}{l.ID, owner.Addr().String()})
			continue
		}
		var server *http.Server
		var udp *quicOwner
		var dtlsServer *dtlsOwner
		var packetServer *packetOwner
		var mcpOwner *adapter.MCPHTTP
		var webrtcOwner *adapter.WebRTC
		if l.Protocol == "udp" || l.Protocol == "coap_udp" || l.Protocol == "unixgram" {
			if l.Protocol == "udp" || l.Protocol == "coap_udp" {
				packetServer, err = newUDPOwner(c, l, d, slots, requests)
			} else {
				packetServer, err = newUnixgramOwner(c, l, d, slots, requests)
			}
			if err != nil {
				return err
			}
		} else if l.Protocol == "dtls" || l.Protocol == "coaps" {
			dtlsServer, err = newDTLSOwner(d, slots, requests, certificates[l.ID], l.Address, l.Protocol)
			if err != nil {
				return err
			}
		} else if l.Protocol == "http3" || l.Protocol == "webtransport" || l.Protocol == "rawquic" || l.Protocol == "rawquic_datagrams" {
			pair, ok := certificates[l.ID]
			if !ok {
				return errors.New(l.Protocol + " listener requires TLS certificates")
			}
			if l.Protocol == "rawquic" || l.Protocol == "rawquic_datagrams" {
				udp, err = newRawQUICOwner(workCtx, d, slots, requests, pair, l.Address, l.Protocol == "rawquic_datagrams")
			} else if l.Protocol == "webtransport" {
				udp, err = newWebTransportOwner(workCtx, d, slots, requests, l.AllowedOrigins, pair, l.Address)
			} else {
				udp, err = newHTTP3Owner(workCtx, d, slots, l.AllowedOrigins, pair, l.Address)
			}
			if err != nil {
				return err
			}
			if udp.server != nil {
				udp.server.Handler = owned.wrap(udp.server.Handler)
			}
		} else if l.Protocol == "webrtc" {
			options := config.WebRTC{}
			if l.WebRTC != nil {
				options = *l.WebRTC
			}
			if options.ICETransportPolicy == "" {
				options.ICETransportPolicy = "all"
			}
			server, webrtcOwner, err = adapter.WebRTCServer(d, v, slots, requests, c.Limits.SessionsPerPrincipal, options, l.AllowedOrigins)
			if err != nil {
				return err
			}
		} else if l.Protocol == "http" || l.Protocol == "grpc" || l.Protocol == "websocket" || l.Protocol == "mcp_http" {
			if l.Protocol == "websocket" {
				server, err = adapter.WebSocketServer(d, requests, l.AllowedOrigins)
			} else if l.Protocol == "mcp_http" {
				server, mcpOwner, err = adapter.MCPHTTPServer(d, v, requests, c.Limits.Sessions, c.Limits.SessionsPerPrincipal, l.AllowedOrigins)
			} else if l.Protocol == "grpc" {
				server, err = adapter.GRPCServer(d, requests)
			} else {
				server, err = adapter.HTTPServer(d, c.Limits.Sessions, l.AllowedOrigins)
			}
			if err != nil {
				return err
			}
			server.Handler = owned.wrap(server.Handler)
			server.BaseContext = func(net.Listener) context.Context { return workCtx }
		}
		if webrtcOwner != nil {
			server.Handler = owned.wrap(server.Handler)
			server.BaseContext = func(net.Listener) context.Context { return workCtx }
			if server.TLSConfig == nil {
				server.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS13}
			}
		}
		var listener net.Listener
		if packetServer != nil {
			instances = append(instances, instance{packet: packetServer, dispatcher: d})
			state.Listeners = append(state.Listeners, struct {
				ID      string `json:"id"`
				Address string `json:"address"`
			}{l.ID, packetServer.addr().String()})
			continue
		}
		if dtlsServer != nil {
			instances = append(instances, instance{dtls: dtlsServer, dispatcher: d})
			state.Listeners = append(state.Listeners, struct {
				ID      string `json:"id"`
				Address string `json:"address"`
			}{l.ID, dtlsServer.addr().String()})
			continue
		}
		if udp != nil {
			instances = append(instances, instance{quic: udp, dispatcher: d})
			state.Listeners = append(state.Listeners, struct {
				ID      string `json:"id"`
				Address string `json:"address"`
			}{l.ID, udp.addr().String()})
			continue
		}
		if l.Protocol == "unix" {
			listener, err = adapter.ListenUnix(l.Address)
		} else {
			listener, err = (&net.ListenConfig{}).Listen(ctx, "tcp", l.Address)
		}
		if err != nil {
			if webrtcOwner != nil {
				_ = webrtcOwner.Close()
			}
			if mcpOwner != nil {
				mcpOwner.Close()
			}
			return err
		}
		bounded := limitedListener{Listener: listener, slots: slots}
		var transport net.Listener = bounded
		if pair, ok := certificates[l.ID]; ok {
			tlsConfig := &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{pair}, NextProtos: []string{adapter.FramedALPN}}
			if l.Protocol == "coaps_tcp" {
				tlsConfig.NextProtos = []string{adapter.CoAPALPN}
			}
			if server != nil {
				tlsConfig = server.TLSConfig.Clone()
				tlsConfig.Certificates = []tls.Certificate{pair}
			}
			transport = tls.NewListener(bounded, tlsConfig)
		}
		var coapProtection *adapter.CoAPResponseProtection
		if l.Protocol == "coap_tcp" {
			coapProtection, err = loadCoAPResponseProtection(c, l)
			if err != nil {
				_ = transport.Close()
				return err
			}
		}
		instances = append(instances, instance{server: server, mcp: mcpOwner, webrtc: webrtcOwner, listener: transport, dispatcher: d, encryptedOnly: l.Protocol == "unix" || l.TLSKeyFile == "", coapTCP: l.Protocol == "coaps_tcp" || l.Protocol == "coap_tcp", coapProtection: coapProtection})
		state.Listeners = append(state.Listeners, struct {
			ID      string `json:"id"`
			Address string `json:"address"`
		}{l.ID, listener.Addr().String()})
	}
	runner, err := delivery.New(s, b, c.Limits.Workers)
	if err != nil {
		return err
	}
	results := make(chan error, len(instances)+1)
	var work sync.WaitGroup
	work.Go(func() { results <- runner.Run(workCtx) })
	for _, i := range instances {
		work.Go(func() {
			if i.oscore != nil {
				results <- i.oscore.Wait()
			} else if i.packet != nil {
				results <- i.packet.serve(workCtx)
			} else if i.dtls != nil {
				results <- i.dtls.serve(workCtx)
			} else if i.quic != nil {
				results <- i.quic.serve(workCtx)
			} else if i.server != nil {
				results <- i.server.Serve(i.listener)
			} else if i.coapTCP {
				results <- serveCoAPTCPListener(workCtx, i.listener, i.dispatcher, requests, i.coapProtection)
			} else {
				results <- adapter.ServeFramed(workCtx, i.listener, i.dispatcher, requests, i.encryptedOnly)
			}
		})
	}
	first := json.NewEncoder(out).Encode(state)
	if first == nil {
		select {
		case <-ctx.Done():
		case first = <-results:
		}
	}
	owned.stop()
	cancel()
	shutdown, finish := context.WithTimeout(context.Background(), 5*time.Second)
	defer finish()
	var closing sync.WaitGroup
	for _, i := range instances {
		closing.Go(func() {
			if i.oscore != nil {
				_ = i.oscore.Close()
				return
			}
			if i.webrtc != nil {
				_ = i.webrtc.Close()
			}
			if i.packet != nil {
				_ = i.packet.close()
				return
			}
			if i.dtls != nil {
				_ = i.dtls.close()
				return
			}
			if i.quic != nil {
				_ = i.quic.close(shutdown)
				return
			}
			if i.mcp != nil {
				i.mcp.Close()
			}
			if i.server != nil {
				_ = i.server.Shutdown(shutdown)
				_ = i.server.Close()
			} else {
				_ = i.listener.Close()
			}
		})
	}
	closing.Wait()
	work.Wait()
	owned.work.Wait()
	close(results)
	for err := range results {
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			first = errors.Join(first, err)
		}
	}
	return first
}
