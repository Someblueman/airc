// Package service owns configured ingress, delivery workers and journal lifetime.
package service

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
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
// is provisioned and native AIRC dials require a journal-authorized lease or read
// authority. Explicit outbound brokers dial only after preparation completes.
func Run(ctx context.Context, c config.Config, out io.Writer) (failure error) {
	if err := c.Validate(); err != nil {
		return err
	}
	if err := c.CheckFiles(); err != nil {
		return err
	}
	for _, l := range c.Listeners {
		if l.Enabled && l.Protocol == "bpv7" {
			return errors.New("BPv7 requires import-bpv7")
		}
		if l.Enabled && l.Protocol == "offline_bundle" {
			return errors.New("offline bundle requires import-bundle")
		}
		if l.Enabled && l.Protocol == "file_mailbox" {
			return errors.New("file mailbox requires mailbox-once")
		}
		if l.Enabled && (l.Protocol == "stdio" || l.Protocol == "socketpair" || l.Protocol == "inherited" || l.Protocol == "fifo") {
			return fmt.Errorf("%s listener requires serve-%s", l.Protocol, l.Protocol)
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
	for _, b := range c.RedisPubSub {
		if b.Enabled {
			enabled++
		}
	}
	for _, b := range c.RedisStreams {
		if b.Enabled {
			enabled++
		}
	}
	for _, owner := range c.Kafka {
		if owner.Enabled {
			enabled++
		}
	}
	for _, owner := range c.IRCBridges {
		if owner.Enabled {
			enabled++
		}
	}
	if enabled == 0 {
		return errors.New("no enabled listeners or brokers")
	}
	s, err := store.Open(ctx, c)
	if err != nil {
		return err
	}
	defer func() { failure = errors.Join(failure, s.Close()) }()
	brokers, err := prepareRedis(c, s, v, b, cursors)
	if err != nil {
		return err
	}
	defer func() {
		for _, o := range brokers {
			o.clear()
		}
	}()
	streams, err := prepareRedisStreams(c, s, v, b, cursors)
	if err != nil {
		return err
	}
	defer func() {
		for _, o := range streams {
			o.clear()
		}
	}()
	kafka, err := prepareKafka(ctx, c, s, v, b, cursors)
	if err != nil {
		return err
	}
	defer func() {
		for _, o := range kafka {
			o.clear()
		}
	}()
	irc, err := prepareIRC(c, s, v)
	if err != nil {
		return err
	}
	defer func() {
		for _, o := range irc {
			o.server.Close()
		}
	}()
	type instance struct {
		mailbox        mailboxOwner
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
		zeroMQ         bool
		nng            bool
		unixPacket     bool
		coapTCP        bool
		coapProtection *adapter.CoAPResponseProtection
		mqtt           *config.MQTT
		amqp           *config.AMQP
		stomp          *config.STOMP
		nats           *config.NATS
		natsTLS        *tls.Config
		syslog         *auth.SyslogAuthenticator
		signedSyslog   *auth.SignedSyslogAuthenticator
		signedResponse *auth.ResponseSealer
		snmp           net.PacketConn
		snmpAuth       *auth.SNMPAuthenticator
		mqttSN         net.PacketConn
		mqttSNOptions  *config.MQTTSN
	}
	instances := make([]instance, 0, enabled)
	defer func() {
		for _, i := range instances {
			if i.snmp != nil {
				_ = i.snmp.Close()
			}
			if i.mqttSN != nil {
				_ = i.mqttSN.Close()
			}
			if i.mailbox != nil {
				_ = i.mailbox.Close()
			}
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
		Brokers []struct {
			ID       string `json:"id"`
			Protocol string `json:"protocol"`
		} `json:"brokers,omitempty"`
	}{Version: 1, State: "ready"}
	for _, o := range brokers {
		state.Brokers = append(state.Brokers, struct {
			ID       string `json:"id"`
			Protocol string `json:"protocol"`
		}{o.options.ID, "redis/pubsub"})
	}
	for _, o := range streams {
		state.Brokers = append(state.Brokers, struct {
			ID       string `json:"id"`
			Protocol string `json:"protocol"`
		}{o.options.ID, "redis/streams"})
	}
	for _, o := range kafka {
		state.Brokers = append(state.Brokers, struct {
			ID       string `json:"id"`
			Protocol string `json:"protocol"`
		}{o.options.ID, "kafka"})
	}
	for _, o := range irc {
		state.Brokers = append(state.Brokers, struct {
			ID       string `json:"id"`
			Protocol string `json:"protocol"`
		}{o.id, "irc"})
	}
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
		if l.Protocol == "snmp_v3" {
			a, err := auth.NewSNMPAuthenticator(v, *l.SNMP)
			if err != nil {
				return err
			}
			conn, err := net.ListenPacket("udp", l.Address)
			if err != nil {
				return err
			}
			instances = append(instances, instance{snmp: conn, snmpAuth: a, dispatcher: d})
			state.Listeners = append(state.Listeners, struct {
				ID      string `json:"id"`
				Address string `json:"address"`
			}{l.ID, conn.LocalAddr().String()})
			continue
		}
		if l.Protocol == "mqtt_sn" {
			conn, err := net.ListenPacket("udp", l.Address)
			if err != nil {
				return err
			}
			instances = append(instances, instance{mqttSN: conn, mqttSNOptions: l.MQTTSN, dispatcher: d})
			state.Listeners = append(state.Listeners, struct {
				ID      string `json:"id"`
				Address string `json:"address"`
			}{l.ID, conn.LocalAddr().String()})
			continue
		}
		if l.Protocol == "directory_mailbox" || l.Protocol == "append_log" {
			var owner mailboxOwner
			var err error
			if l.Protocol == "append_log" {
				owner, err = openAppendLog(c, l, d, requests)
			} else {
				owner, err = openDirectoryMailbox(c, l, d, requests)
			}
			if err != nil {
				return err
			}
			instances = append(instances, instance{mailbox: owner, dispatcher: d})
			state.Listeners = append(state.Listeners, struct {
				ID      string `json:"id"`
				Address string `json:"address"`
			}{l.ID, l.Address})
			continue
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
		if l.Protocol == "udp" || l.Protocol == "ipv4_multicast" || l.Protocol == "udp_lite" || l.Protocol == "coap_udp" || l.Protocol == "unixgram" || l.Protocol == "posix_mqueue" || l.Protocol == "system_v_queue" {
			if l.Protocol == "udp" || l.Protocol == "ipv4_multicast" || l.Protocol == "udp_lite" || l.Protocol == "coap_udp" {
				packetServer, err = newUDPOwner(c, l, d, slots, requests)
			} else if l.Protocol == "system_v_queue" {
				packetServer, err = newSystemVQueueOwner(workCtx, c, l, s, d, slots, requests)
			} else if l.Protocol == "posix_mqueue" {
				packetServer, err = newPOSIXQueueOwner(workCtx, c, l, s, d, slots, requests)
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
		} else if l.Protocol == "unixpacket" {
			listener, err = adapter.ListenUnixPacket(l.Address)
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
		var syslog *auth.SyslogAuthenticator
		if l.Syslog != nil {
			syslog, err = auth.NewSyslogAuthenticator(v, *l.Syslog)
			if err != nil {
				_ = listener.Close()
				return err
			}
		}
		bounded := limitedListener{Listener: listener, slots: slots}
		var transport net.Listener = bounded
		var signedSyslog *auth.SignedSyslogAuthenticator
		if l.SignedSyslog != nil {
			signedSyslog, err = auth.NewSignedSyslogAuthenticator(v, *l.SignedSyslog)
			if err != nil {
				listener.Close()
				return err
			}
		}
		if l.Protocol == "nats" || l.Protocol == "amqp" || l.Protocol == "stomp" || l.Protocol == "syslog_tls" || l.Protocol == "syslog_signed" {
			transport = listener
		}
		var natsTLS *tls.Config
		if pair, ok := certificates[l.ID]; ok {
			tlsConfig := &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{pair}, NextProtos: []string{adapter.FramedALPN}}
			if l.Protocol == "nats" || l.Protocol == "mqtt" || l.Protocol == "amqp" || l.Protocol == "stomp" {
				tlsConfig.NextProtos = nil
			}
			if l.Protocol == "coaps_tcp" {
				tlsConfig.NextProtos = []string{adapter.CoAPALPN}
			}
			if server != nil {
				tlsConfig = server.TLSConfig.Clone()
				tlsConfig.Certificates = []tls.Certificate{pair}
			}
			if syslog != nil {
				tlsConfig, err = syslog.TLSConfig(tlsConfig)
				if err != nil {
					_ = transport.Close()
					return err
				}
			}
			if l.Protocol == "nats" {
				natsTLS = tlsConfig
			} else {
				transport = tls.NewListener(transport, tlsConfig)
			}
		}
		var signedResponse *auth.ResponseSealer
		if l.Protocol == "nats" || l.Protocol == "amqp" || l.Protocol == "stomp" {
			signedResponse, err = loadSignedResponseSigner(c)
			if err != nil {
				_ = transport.Close()
				return err
			}
		}
		var coapProtection *adapter.CoAPResponseProtection
		if l.Protocol == "coap_tcp" {
			coapProtection, err = loadCoAPResponseProtection(c, l)
			if err != nil {
				_ = transport.Close()
				return err
			}
		}
		instances = append(instances, instance{server: server, mcp: mcpOwner, webrtc: webrtcOwner, listener: transport, dispatcher: d, encryptedOnly: l.Protocol == "unix" || l.TLSKeyFile == "", zeroMQ: l.Protocol == "zeromq", nng: l.Protocol == "nng", unixPacket: l.Protocol == "unixpacket", coapTCP: l.Protocol == "coaps_tcp" || l.Protocol == "coap_tcp", coapProtection: coapProtection, mqtt: l.MQTT, amqp: l.AMQP, stomp: l.STOMP, nats: l.NATS, natsTLS: natsTLS, syslog: syslog, signedSyslog: signedSyslog, signedResponse: signedResponse})
		state.Listeners = append(state.Listeners, struct {
			ID      string `json:"id"`
			Address string `json:"address"`
		}{l.ID, listener.Addr().String()})
	}
	runner, err := delivery.New(s, b, c.Limits.Workers)
	if err != nil {
		return err
	}
	results := make(chan error, len(instances)+len(brokers)+len(streams)+len(kafka)+len(irc)+1)
	var work sync.WaitGroup
	work.Go(func() { results <- runner.Run(workCtx) })
	for _, o := range brokers {
		work.Go(func() {
			results <- adapter.ServeRedisPubSub(workCtx, o.options, o.security, o.subscriber, o.publisher, c.Realm, o.dispatcher, o.sealer, slots, requests)
		})
	}
	for _, o := range streams {
		work.Go(func() {
			results <- adapter.ServeRedisStreams(workCtx, o.options, o.security, o.password, c.Realm, o.dispatcher, o.sealer, slots, requests)
		})
	}
	for _, o := range kafka {
		work.Go(func() {
			results <- adapter.ServeKafka(workCtx, o.options, o.security, o.password, c.Realm, o.dispatcher, s, o.sealer, slots, requests)
		})
	}
	for _, o := range irc {
		work.Go(func() { results <- adapter.ServeIRC(workCtx, o.server, o.dispatcher, slots, requests) })
	}
	for _, i := range instances {
		work.Go(func() {
			if i.snmp != nil {
				results <- adapter.ServeSNMP(workCtx, i.snmp, i.snmpAuth, i.dispatcher, requests)
			} else if i.mqttSN != nil {
				results <- adapter.ServeMQTTSN(workCtx, i.mqttSN, i.dispatcher, requests, *i.mqttSNOptions)
			} else if i.mailbox != nil {
				results <- i.mailbox.serve(workCtx)
			} else if i.oscore != nil {
				results <- i.oscore.Wait()
			} else if i.packet != nil {
				results <- i.packet.serve(workCtx)
			} else if i.dtls != nil {
				results <- i.dtls.serve(workCtx)
			} else if i.quic != nil {
				results <- i.quic.serve(workCtx)
			} else if i.server != nil {
				results <- i.server.Serve(i.listener)
			} else if i.nng {
				results <- adapter.ServeNNG(workCtx, i.listener, i.dispatcher, requests)
			} else if i.zeroMQ {
				results <- adapter.ServeZeroMQ(workCtx, i.listener, i.dispatcher, requests)
			} else if i.unixPacket {
				results <- adapter.ServeUnixPacket(workCtx, i.listener, i.dispatcher, requests)
			} else if i.coapTCP {
				results <- serveCoAPTCPListener(workCtx, i.listener, i.dispatcher, requests, i.coapProtection)
			} else if i.syslog != nil {
				results <- adapter.ServeSyslog(workCtx, i.listener, i.syslog, i.signedSyslog, slots, c.Limits.SessionsPerPrincipal, c.Realm, i.dispatcher, requests)
			} else if i.nats != nil {
				results <- adapter.ServeNATS(workCtx, i.listener, i.natsTLS, v, slots, c.Limits.SessionsPerPrincipal, *i.nats, c.Realm, i.dispatcher, i.signedResponse, requests)
			} else if i.stomp != nil {
				results <- adapter.ServeSTOMP(workCtx, i.listener, v, slots, c.Limits.SessionsPerPrincipal, *i.stomp, c.Realm, i.dispatcher, i.signedResponse, requests)
			} else if i.amqp != nil {
				results <- adapter.ServeAMQP(workCtx, i.listener, v, slots, c.Limits.SessionsPerPrincipal, *i.amqp, i.dispatcher, i.signedResponse, c.Realm, requests)
			} else if i.mqtt != nil {
				results <- adapter.ServeMQTT(workCtx, i.listener, i.dispatcher, v, requests, *i.mqtt)
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
			if i.snmp != nil {
				_ = i.snmp.Close()
				return
			}
			if i.mqttSN != nil {
				_ = i.mqttSN.Close()
				return
			}
			// Pollers observe cancellation and must join before their files close.
			if i.mailbox != nil {
				return
			}
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
