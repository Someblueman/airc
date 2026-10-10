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
		if l.Enabled && l.Protocol == "scp_inbox" {
			return errors.New("SCP inbox requires scp-inbox")
		}
		if l.Enabled && l.Protocol == "bpv7" {
			return errors.New("BPv7 requires import-bpv7")
		}
		if l.Enabled && l.Protocol == "offline_bundle" {
			return errors.New("offline bundle requires import-bundle")
		}
		if l.Enabled && l.Protocol == "file_mailbox" {
			return errors.New("file mailbox requires mailbox-once")
		}
		if l.Enabled && (l.Protocol == "stdio" || l.Protocol == "socketpair" || l.Protocol == "inherited" || l.Protocol == "fifo" || l.Protocol == "uart") {
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
	for _, owner := range c.SQS {
		if owner.Enabled {
			enabled++
		}
	}
	for _, owner := range c.IRCBridges {
		if owner.Enabled {
			enabled++
		}
	}
	for _, owner := range c.XMPPBridges {
		if owner.Enabled {
			enabled++
		}
	}
	for _, owner := range c.MatrixBridges {
		if owner.Enabled {
			enabled++
		}
	}
	for _, owner := range c.SFTP {
		if owner.Enabled {
			enabled++
		}
	}
	for _, owner := range c.S3 {
		if owner.Enabled {
			enabled++
		}
	}
	for _, owner := range c.SQLiteMailboxes {
		if owner.Enabled {
			enabled++
		}
	}
	for _, owner := range c.CouchDBChanges {
		if owner.Enabled {
			enabled++
		}
	}
	for _, owner := range c.PostgresNotify {
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
	irc, err := prepareIRC(c, s, v, b, cursors)
	if err != nil {
		return err
	}
	defer func() {
		for _, o := range irc {
			o.server.Close()
		}
	}()
	xmpp, err := prepareXMPP(c, s, v, b, cursors)
	if err != nil {
		return err
	}
	defer func() {
		for _, o := range xmpp {
			o.server.Close()
		}
	}()
	matrix, err := prepareMatrix(c, s, v, b, cursors)
	if err != nil {
		return err
	}
	defer func() {
		for _, o := range matrix {
			o.server.Close()
		}
	}()
	sqs, err := prepareSQS(c, s, v, b, cursors)
	if err != nil {
		return err
	}
	defer func() {
		for _, o := range sqs {
			o.close()
		}
	}()
	sftp, err := prepareSFTP(c, s, v, b, cursors)
	if err != nil {
		return err
	}
	defer func() {
		for _, o := range sftp {
			o.close()
		}
	}()
	s3, err := prepareS3(c, s, v, b, cursors)
	if err != nil {
		return err
	}
	defer func() {
		for _, o := range s3 {
			o.close()
		}
	}()
	sqliteMailboxes, err := prepareSQLiteMailboxes(ctx, c, s, v, b, cursors)
	if err != nil {
		return err
	}
	defer func() {
		for _, o := range sqliteMailboxes {
			failure = errors.Join(failure, o.close())
		}
	}()
	couchDBChanges, err := prepareCouchDBChanges(ctx, c, s, v, b, cursors)
	if err != nil {
		return err
	}
	defer func() {
		for _, o := range couchDBChanges {
			o.close()
		}
	}()
	postgresNotify, err := preparePostgresNotify(c, s, v, b, cursors)
	if err != nil {
		return err
	}
	defer func() {
		for _, o := range postgresNotify {
			o.close()
		}
	}()
	type instance struct {
		mach               *machIPCOwner
		mailbox            mailboxOwner
		bleGATT            *bleGATTOwner
		oscore             oscoreOwner
		server             *http.Server
		quic               *quicOwner
		dtls               *dtlsOwner
		packet             *packetOwner
		dccp               *dccpOwner
		mcp                *adapter.MCPHTTP
		webrtc             *adapter.WebRTC
		listener           net.Listener
		dispatcher         *dispatch.Dispatcher
		encryptedOnly      bool
		zeroMQ             bool
		nng                bool
		unixPacket         bool
		coapTCP            bool
		sctp               bool
		sctpMode           adapter.SCTPMode
		responseProtection *adapter.ConnectionResponseProtection
		mqtt               *config.MQTT
		amqp               *config.AMQP
		stomp              *config.STOMP
		nats               *config.NATS
		natsTLS            *tls.Config
		syslog             *auth.SyslogAuthenticator
		signedSyslog       *auth.SignedSyslogAuthenticator
		signedResponse     *auth.ResponseSealer
		snmp               net.PacketConn
		snmpAuth           *auth.SNMPAuthenticator
		mqttSN             net.PacketConn
		mqttSNOptions      *config.MQTTSN
	}
	instances := make([]instance, 0, enabled)
	defer func() {
		for _, i := range instances {
			if i.bleGATT != nil {
				_ = i.bleGATT.Close()
			}
			if i.mach != nil {
				_ = i.mach.close()
			}
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
	for _, o := range irc {
		state.Brokers = append(state.Brokers, struct {
			ID       string `json:"id"`
			Protocol string `json:"protocol"`
		}{o.id, "irc"})
	}
	for _, o := range xmpp {
		state.Brokers = append(state.Brokers, struct {
			ID       string `json:"id"`
			Protocol string `json:"protocol"`
		}{o.id, "xmpp"})
	}
	for _, o := range matrix {
		state.Brokers = append(state.Brokers, struct {
			ID       string `json:"id"`
			Protocol string `json:"protocol"`
		}{o.id, "matrix"})
	}
	for _, o := range sqs {
		protocol := "aws/sqs"
		if o.options.EventBridge != nil {
			protocol = "aws/eventbridge/sqs"
		}
		state.Brokers = append(state.Brokers, struct {
			ID       string `json:"id"`
			Protocol string `json:"protocol"`
		}{o.options.ID, protocol})
	}
	for _, o := range sftp {
		state.Brokers = append(state.Brokers, struct {
			ID       string `json:"id"`
			Protocol string `json:"protocol"`
		}{o.options.ID, "sftp"})
	}
	for _, o := range s3 {
		state.Brokers = append(state.Brokers, struct {
			ID       string `json:"id"`
			Protocol string `json:"protocol"`
		}{o.options.ID, "aws/s3"})
	}
	for _, o := range sqliteMailboxes {
		state.Brokers = append(state.Brokers, struct {
			ID       string `json:"id"`
			Protocol string `json:"protocol"`
		}{o.options.ID, "sqlite/mailbox"})
	}
	for _, o := range couchDBChanges {
		state.Brokers = append(state.Brokers, struct {
			ID       string `json:"id"`
			Protocol string `json:"protocol"`
		}{o.options.ID, "couchdb/changes"})
	}
	for _, o := range postgresNotify {
		state.Brokers = append(state.Brokers, struct {
			ID       string `json:"id"`
			Protocol string `json:"protocol"`
		}{o.options.ID, "postgres/listen-notify"})
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
		if l.Protocol == "mach_ipc" {
			owner, err := newMachIPCOwner(ctx, c, l, d, requests)
			if err != nil {
				return err
			}
			instances = append(instances, instance{mach: owner})
			state.Listeners = append(state.Listeners, struct {
				ID      string `json:"id"`
				Address string `json:"address"`
			}{l.ID, l.Address})
			continue
		}
		if l.Protocol == "can_isotp" || l.Protocol == "canfd_isotp" {
			owner, err := newCANISOTPOwner(c, l, d, slots, requests)
			if err != nil {
				return err
			}
			instances = append(instances, instance{packet: owner})
			state.Listeners = append(state.Listeners, struct {
				ID      string `json:"id"`
				Address string `json:"address"`
			}{l.ID, owner.addr().String()})
			continue
		}
		if l.Protocol == "dccp" {
			owner, err := newDCCPOwner(c, l, d, slots, requests)
			if err != nil {
				return err
			}
			instances = append(instances, instance{dccp: owner, listener: owner.listener})
			state.Listeners = append(state.Listeners, struct {
				ID      string `json:"id"`
				Address string `json:"address"`
			}{l.ID, owner.listener.Addr().String()})
			continue
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
		if l.Protocol == "ble_gatt" {
			owner, err := openBLEGATT(workCtx, c, l, d, slots, requests)
			if err != nil {
				return err
			}
			instances = append(instances, instance{bleGATT: owner, dispatcher: d})
			state.Listeners = append(state.Listeners, struct {
				ID      string `json:"id"`
				Address string `json:"address"`
			}{l.ID, l.Address})
			continue
		}
		if l.Protocol == "directory_mailbox" || l.Protocol == "append_log" || l.Protocol == "ftps_inbox" {
			var owner mailboxOwner
			var err error
			if l.Protocol == "ftps_inbox" {
				owner, err = openFTPSInbox(c, l, d, requests)
			} else if l.Protocol == "append_log" {
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
		if l.Protocol == "raw_ethernet" || l.Protocol == "raw_ip" || l.Protocol == "icmp_v4" || l.Protocol == "icmp_v6" || l.Protocol == "udp" || l.Protocol == "ipv4_multicast" || l.Protocol == "ipv4_broadcast" || l.Protocol == "ipv6_multicast" || l.Protocol == "udp_lite" || l.Protocol == "coap_udp" || l.Protocol == "unixgram" || l.Protocol == "shared_memory" || l.Protocol == "mapped_file" || l.Protocol == "posix_mqueue" || l.Protocol == "system_v_queue" {
			if l.Protocol == "icmp_v6" {
				packetServer, err = newICMPv6Owner(c, l, d, slots, requests)
			} else if l.Protocol == "icmp_v4" {
				packetServer, err = newICMPv4Owner(c, l, d, slots, requests)
			} else if l.Protocol == "raw_ip" {
				packetServer, err = newRawIPOwner(c, l, d, slots, requests)
			} else if l.Protocol == "raw_ethernet" {
				packetServer, err = newRawEthernetOwner(c, l, d, slots, requests)
			} else if l.Protocol == "udp" || l.Protocol == "ipv4_multicast" || l.Protocol == "ipv4_broadcast" || l.Protocol == "ipv6_multicast" || l.Protocol == "udp_lite" || l.Protocol == "coap_udp" {
				packetServer, err = newUDPOwner(c, l, d, slots, requests)
			} else if l.Protocol == "system_v_queue" {
				packetServer, err = newSystemVQueueOwner(workCtx, c, l, s, d, slots, requests)
			} else if l.Protocol == "posix_mqueue" {
				packetServer, err = newPOSIXQueueOwner(workCtx, c, l, s, d, slots, requests)
			} else if l.Protocol == "shared_memory" {
				packetServer, err = newSharedMemoryOwner(workCtx, c, l, d, slots, requests)
			} else if l.Protocol == "mapped_file" {
				packetServer, err = newMappedFileOwner(workCtx, c, l, d, slots, requests)
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
		} else if l.Protocol == "http" || l.Protocol == "grpc" || l.Protocol == "websocket" || l.Protocol == "mcp_http" || l.Protocol == "github_webhook" || l.Protocol == "chirpstack" || l.Protocol == "twilio_sms" || l.Protocol == "postmark_email" {
			if l.Protocol == "github_webhook" {
				verifier, verifyErr := auth.NewGitHubWebhook(v, c, *l.GitHubWebhook)
				if verifyErr != nil {
					return verifyErr
				}
				defer verifier.Close()
				server, err = adapter.GitHubWebhookServer(d, verifier, requests)
			} else if l.Protocol == "chirpstack" {
				verifier, verifyErr := auth.NewChirpStack(v, c, *l.ChirpStack)
				if verifyErr != nil {
					return verifyErr
				}
				defer verifier.Close()
				server, err = adapter.ChirpStackServer(d, verifier, requests)
			} else if l.Protocol == "twilio_sms" {
				verifier, verifyErr := auth.NewTwilioSMS(v, c, *l.TwilioSMS)
				if verifyErr != nil {
					return verifyErr
				}
				defer verifier.Close()
				server, err = adapter.TwilioSMSServer(d, verifier, requests)
			} else if l.Protocol == "postmark_email" {
				verifier, verifyErr := auth.NewPostmarkEmail(v, c, *l.PostmarkEmail)
				if verifyErr != nil {
					return verifyErr
				}
				defer verifier.Close()
				server, err = adapter.PostmarkEmailServer(d, verifier, requests)
			} else if l.Protocol == "websocket" {
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
		sctpMode := adapter.SCTPOrdered
		if l.Protocol == "sctp_unordered" {
			sctpMode = adapter.SCTPUnordered
		} else if l.Protocol == "sctp_pr_ttl" {
			sctpMode = adapter.SCTPOrderedTTL
		}
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
		if l.Protocol == "sctp" || l.Protocol == "sctp_unordered" || l.Protocol == "sctp_pr_ttl" {
			listener, err = adapter.ListenSCTP(l.Address, sctpMode)
		} else if l.Protocol == "virtio_vsock" {
			listener, err = adapter.ListenVSock(l.Address, l.VSock.AllowedPeerCIDs)
		} else if l.Protocol == "bluetooth_rfcomm" {
			listener, err = adapter.ListenRFCOMM(l.Address, l.RFCOMM.AllowedPeerAddresses)
		} else if l.Protocol == "bluetooth_l2cap" {
			listener, err = adapter.ListenL2CAP(l.Address, l.L2CAP.AllowedPeerAddresses)
		} else if l.Protocol == "unix" {
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
		if l.Protocol == "nats" {
			signedResponse, err = loadSignedResponseSigner(c)
			if err != nil {
				_ = transport.Close()
				return err
			}
		}
		var responseProtection *adapter.ConnectionResponseProtection
		if l.Protocol == "coap_tcp" || l.Protocol == "sctp" || l.Protocol == "sctp_unordered" || l.Protocol == "sctp_pr_ttl" {
			responseProtection, err = loadConnectionResponseProtection(c, l)
			if err != nil {
				_ = transport.Close()
				return err
			}
		}
		instances = append(instances, instance{server: server, mcp: mcpOwner, webrtc: webrtcOwner, listener: transport, dispatcher: d, encryptedOnly: l.Protocol == "unix" || l.TLSKeyFile == "", zeroMQ: l.Protocol == "zeromq", nng: l.Protocol == "nng", unixPacket: l.Protocol == "unixpacket", coapTCP: l.Protocol == "coaps_tcp" || l.Protocol == "coap_tcp", sctp: l.Protocol == "sctp" || l.Protocol == "sctp_unordered" || l.Protocol == "sctp_pr_ttl", sctpMode: sctpMode, responseProtection: responseProtection, mqtt: l.MQTT, amqp: l.AMQP, stomp: l.STOMP, nats: l.NATS, natsTLS: natsTLS, syslog: syslog, signedSyslog: signedSyslog, signedResponse: signedResponse})
		state.Listeners = append(state.Listeners, struct {
			ID      string `json:"id"`
			Address string `json:"address"`
		}{l.ID, listener.Addr().String()})
	}
	runner, err := delivery.New(s, b, c.Limits.Workers)
	if err != nil {
		return err
	}
	results := make(chan error, len(instances)+len(brokers)+len(streams)+len(irc)+len(xmpp)+len(matrix)+len(sqs)+len(sftp)+len(s3)+len(sqliteMailboxes)+len(couchDBChanges)+len(postgresNotify)+1)
	var work sync.WaitGroup
	work.Go(func() { results <- runner.Run(workCtx) })
	for _, o := range brokers {
		work.Go(func() {
			results <- adapter.ServeRedisPubSub(workCtx, o.options, o.security, o.subscriber, o.publisher, o.dispatcher, slots, requests)
		})
	}
	for _, o := range streams {
		work.Go(func() {
			results <- adapter.ServeRedisStreams(workCtx, o.options, o.security, o.password, o.dispatcher, slots, requests)
		})
	}
	for _, o := range irc {
		work.Go(func() { results <- adapter.ServeIRC(workCtx, o.server, o.dispatcher, slots, requests) })
	}
	for _, o := range xmpp {
		work.Go(func() { results <- adapter.ServeXMPP(workCtx, o.server, o.dispatcher, slots, requests) })
	}
	for _, o := range matrix {
		work.Go(func() { results <- adapter.ServeMatrix(workCtx, o.server, o.dispatcher, s, slots, requests) })
	}
	for _, o := range sqs {
		work.Go(func() {
			results <- adapter.ServeSQS(workCtx, o.options, o.client, c.Realm, o.dispatcher, o.sealer, o.recipient, requests)
		})
	}
	for _, o := range sftp {
		work.Go(func() {
			results <- adapter.ServeSFTP(workCtx, o.options, &o.credentials, c.Realm, o.binding, o.dispatcher, o.sealer, o.recipient, slots, requests)
		})
	}
	for _, o := range s3 {
		work.Go(func() {
			results <- adapter.ServeS3(workCtx, o.options, o.client, c.Realm, o.binding, o.dispatcher, o.sealer, o.recipient, requests)
		})
	}
	for _, o := range sqliteMailboxes {
		work.Go(func() {
			results <- adapter.ServeSQLiteMailbox(workCtx, o.options, o.mailbox, c.Realm, o.binding, o.dispatcher, o.sealer, o.recipient, requests)
		})
	}
	for _, o := range couchDBChanges {
		work.Go(func() {
			results <- adapter.ServeCouchDBChanges(workCtx, o.options, o.client, c.Realm, o.binding, o.dispatcher, s, requests)
		})
	}
	for _, o := range postgresNotify {
		work.Go(func() {
			results <- adapter.ServePostgresNotify(workCtx, o.options, o.security, o.binding, o.dispatcher, slots, requests)
		})
	}
	for _, i := range instances {
		work.Go(func() {
			if i.mach != nil {
				results <- i.mach.serve(workCtx)
			} else if i.bleGATT != nil {
				results <- i.bleGATT.serve(workCtx)
			} else if i.dccp != nil {
				results <- i.dccp.serve(workCtx)
			} else if i.snmp != nil {
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
				results <- serveCoAPTCPListener(workCtx, i.listener, i.dispatcher, requests, i.responseProtection)
			} else if i.sctp {
				results <- adapter.ServeSCTP(workCtx, i.listener, i.dispatcher, requests, i.responseProtection, i.sctpMode)
			} else if i.syslog != nil {
				results <- adapter.ServeSyslog(workCtx, i.listener, i.syslog, i.signedSyslog, slots, c.Limits.SessionsPerPrincipal, c.Realm, i.dispatcher, requests)
			} else if i.nats != nil {
				results <- adapter.ServeNATS(workCtx, i.listener, i.natsTLS, v, slots, c.Limits.SessionsPerPrincipal, *i.nats, c.Realm, i.dispatcher, i.signedResponse, requests)
			} else if i.stomp != nil {
				results <- adapter.ServeSTOMP(workCtx, i.listener, v, slots, c.Limits.SessionsPerPrincipal, *i.stomp, c.Realm, i.dispatcher, requests)
			} else if i.amqp != nil {
				results <- adapter.ServeAMQP(workCtx, i.listener, v, slots, c.Limits.SessionsPerPrincipal, *i.amqp, i.dispatcher, requests)
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
			if i.mach != nil {
				_ = i.mach.close()
				return
			}
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
