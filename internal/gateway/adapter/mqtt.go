package adapter

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/mqttwire"
)

const mqttDeadline = 3 * time.Second

// mqttIdle bounds a connection that disables MQTT keep-alive.
const mqttIdle = 5 * time.Minute

// ServeMQTT owns and joins MQTT 3.1.1 connections, plain or TLS. The caller
// limits accepted sockets; requests share the service's global admission
// capacity. Each PUBLISH payload is a plain contract request naming its
// author, or none after an account.identify on the same connection. A CONNECT
// password is the listener access password, never an identity; user names
// and client IDs carry no authority. A newer connection with the same client
// ID replaces the older one, as MQTT requires.
func ServeMQTT(parent context.Context, listener net.Listener, d *dispatch.Dispatcher, requests chan struct{}, options config.MQTT) error {
	if listener == nil || d == nil || !d.AuthorAccess() || cap(requests) < 1 || len(options.Routes) < 1 || len(options.Routes) > 128 {
		return errors.New("invalid MQTT owner")
	}
	routes := make(map[string]config.MQTTRoute, len(options.Routes))
	for _, route := range options.Routes {
		if _, exists := routes[route.Topic]; exists || !mqttwire.ValidTopic(route.Topic) {
			return errors.New("invalid MQTT route")
		}
		routes[route.Topic] = route
	}
	ctx, cancel := context.WithCancel(parent)
	var connections sync.WaitGroup
	owners := mqttSessions{active: make(map[string]*mqttSession)}
	closed := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = listener.Close(); close(closed) })
	defer func() {
		cancel()
		if !stop() {
			<-closed
		}
		connections.Wait()
	}()
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		connections.Go(func() { serveMQTTConnection(ctx, conn, d, requests, routes, &owners) })
	}
}

func serveMQTTConnection(parent context.Context, conn net.Conn, d *dispatch.Dispatcher, requests chan struct{}, routes map[string]config.MQTTRoute, owners *mqttSessions) {
	ctx, cancel := context.WithCancel(parent)
	session := &mqttSession{conn: conn, cancel: cancel, done: make(chan struct{})}
	closed := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { closeMQTTConnection(conn); close(closed) })
	author := dispatch.NewSession()
	clientID := ""
	defer func() {
		cancel()
		if !stop() {
			<-closed
		}
		closeMQTTConnection(conn)
		author.Close()
		owners.release(clientID, session)
		close(session.done)
	}()
	deadline := time.Now().Add(mqttDeadline)
	if conn.SetDeadline(deadline) != nil {
		return
	}
	if secure, ok := conn.(*tls.Conn); ok {
		hsctx, finish := context.WithDeadline(ctx, deadline)
		err := secure.HandshakeContext(hsctx)
		finish()
		if err != nil {
			return
		}
	}
	packet, err := mqttwire.Read(conn)
	if err != nil {
		if errors.Is(err, mqttwire.ErrVersion) {
			_ = mqttwire.WriteConnack(conn, 1)
		}
		if errors.Is(err, mqttwire.ErrClientID) {
			_ = mqttwire.WriteConnack(conn, 2)
		}
		return
	}
	if packet.Kind != mqttwire.Connect {
		return
	}
	lastControl := time.Now()
	var password core.Password
	if packet.HasPassword && packet.Password != "" {
		if password, err = core.NewPassword(packet.Password); err != nil {
			_ = mqttwire.WriteConnack(conn, 4)
			return
		}
	}
	packet.Password = ""
	if err := d.Connect(author, password); err != nil {
		_ = mqttwire.WriteConnack(conn, 4)
		return
	}
	clientID = packet.ClientID
	if clientID == "" {
		clientID = "airc-" + rand.Text()
	}
	if !owners.claim(ctx, clientID, session) {
		return
	}
	if conn.SetWriteDeadline(time.Now().Add(mqttDeadline)) != nil || mqttwire.WriteConnack(conn, 0) != nil {
		return
	}
	idle := time.Duration(packet.KeepAlive) * time.Second * 3 / 2
	if idle == 0 {
		idle = mqttIdle
	}
	in := dispatch.Ingress{Source: d.Source(conn.RemoteAddr()), Session: author}
	for {
		if ctx.Err() != nil || conn.SetReadDeadline(lastControl.Add(idle)) != nil {
			return
		}
		packet, err = mqttwire.Read(conn)
		if err != nil {
			return
		}
		lastControl = time.Now()
		if conn.SetWriteDeadline(time.Now().Add(mqttDeadline)) != nil {
			return
		}
		switch packet.Kind {
		case mqttwire.Ping:
			if mqttwire.WritePingresp(conn) != nil {
				return
			}
		case mqttwire.Disconnect:
			return
		case mqttwire.Publish:
			// MQTT 3.1.1 has no negative publish acknowledgement: a refused
			// request closes the connection, at either QoS.
			if !admitMQTT(ctx, packet, in, routes, d, requests) {
				return
			}
			if packet.QoS == 1 && (conn.SetWriteDeadline(time.Now().Add(mqttDeadline)) != nil || mqttwire.WritePuback(conn, packet.ID) != nil) {
				return
			}
		default:
			return
		}
	}
}

// Close the socket first: TLS close-notify must not stall cancellation or a
// publisher replacement when the peer has stopped reading.
func closeMQTTConnection(conn net.Conn) {
	if secure, ok := conn.(*tls.Conn); ok {
		_ = secure.NetConn().Close()
	}
	_ = conn.Close()
}

// admitMQTT runs one publication's plain request, published on a route
// topic, as its author. The request names its own target; the listener's
// policy bounds operations and targets. It reports success only after a
// mutation is durably admitted, so QoS 1 PUBACK follows durable intake.
func admitMQTT(ctx context.Context, packet mqttwire.Packet, in dispatch.Ingress, routes map[string]config.MQTTRoute, d *dispatch.Dispatcher, requests chan struct{}) bool {
	if _, ok := routes[packet.Topic]; !ok {
		return false
	}
	sub, err := core.DecodeSubmission([]byte(packet.Payload))
	if err != nil {
		return false
	}
	op := sub.Request.Operation
	if !op.IsMutation() && op != core.AccountIdentify {
		return false
	}
	select {
	case requests <- struct{}{}:
		defer func() { <-requests }()
	default:
		return false
	}
	admission, cancel := context.WithTimeout(ctx, mqttDeadline)
	defer cancel()
	result, err := d.SubmitDecoded(admission, in, sub, time.Now())
	if err != nil {
		return false
	}
	return op == core.AccountIdentify || result.Result != nil && result.Result.Durable
}
