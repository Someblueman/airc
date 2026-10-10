package adapter

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/mqttwire"
)

const mqttDeadline = 3 * time.Second

// ServeMQTT owns and joins TLS connections. The caller limits accepted sockets;
// requests share the service's global admission capacity. Client IDs equal the
// authenticated principal, giving each publisher one clean session per listener.
func ServeMQTT(parent context.Context, listener net.Listener, d *dispatch.Dispatcher, v *auth.Verifier, requests chan struct{}, options config.MQTT) error {
	if listener == nil || d == nil || v == nil || cap(requests) < 1 || len(options.Routes) < 1 || len(options.Routes) > 128 {
		return errors.New("invalid MQTT owner")
	}
	routes := make(map[string]config.MQTTRoute, len(options.Routes))
	for _, route := range options.Routes {
		if _, exists := routes[route.Topic]; exists || !mqttwire.ValidTopic(route.Topic) || route.Principal == "" {
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
		connections.Go(func() { serveMQTTConnection(ctx, conn, d, v, requests, routes, &owners) })
	}
}

func serveMQTTConnection(parent context.Context, conn net.Conn, d *dispatch.Dispatcher, v *auth.Verifier, requests chan struct{}, routes map[string]config.MQTTRoute, owners *mqttSessions) {
	ctx, cancel := context.WithCancel(parent)
	session := &mqttSession{conn: conn, cancel: cancel, done: make(chan struct{})}
	closed := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { closeMQTTConnection(conn); close(closed) })
	principal := ""
	defer func() {
		cancel()
		if !stop() {
			<-closed
		}
		closeMQTTConnection(conn)
		owners.release(principal, session)
		close(session.done)
	}()
	secure, ok := conn.(*tls.Conn)
	if !ok {
		return
	}
	deadline := time.Now().Add(mqttDeadline)
	if conn.SetDeadline(deadline) != nil {
		return
	}
	hsctx, finish := context.WithDeadline(ctx, deadline)
	err := secure.HandshakeContext(hsctx)
	finish()
	if err != nil {
		return
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
	identity, err := v.Authenticate(packet.Password)
	packet.Password = ""
	if err != nil || packet.Username != identity.ID() {
		_ = mqttwire.WriteConnack(conn, 4)
		return
	}
	if packet.ClientID != identity.ID() {
		_ = mqttwire.WriteConnack(conn, 2)
		return
	}
	allowed := false
	for _, route := range routes {
		allowed = allowed || route.Principal == identity.ID()
	}
	if !allowed {
		_ = mqttwire.WriteConnack(conn, 5)
		return
	}
	principal = identity.ID()
	if !owners.claim(ctx, principal, session) {
		return
	}
	if conn.SetWriteDeadline(time.Now().Add(mqttDeadline)) != nil || mqttwire.WriteConnack(conn, 0) != nil {
		return
	}
	idle := time.Duration(packet.KeepAlive) * time.Second * 3 / 2
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
			if !admitMQTT(ctx, packet, identity, routes, d, requests) {
				return
			}
			if conn.SetWriteDeadline(time.Now().Add(mqttDeadline)) != nil || mqttwire.WritePuback(conn, packet.ID) != nil {
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

func admitMQTT(ctx context.Context, packet mqttwire.Packet, identity auth.Identity, routes map[string]config.MQTTRoute, d *dispatch.Dispatcher, requests chan struct{}) bool {
	route, ok := routes[packet.Topic]
	if !ok || route.Principal != identity.ID() {
		return false
	}
	select {
	case requests <- struct{}{}:
		defer func() { <-requests }()
	default:
		return false
	}
	now := time.Now()
	// The payload is a plain contract request; the authenticated connection,
	// already bound to this topic's route, is its only authority.
	verified, err := d.AuthenticateRoute(identity.ID(), []byte(packet.Payload), now)
	if err != nil || verified.PrincipalID() != identity.ID() || verified.Binding() != identity.Binding() || verified.Operation() != core.Send {
		return false
	}
	target, ok := verified.Target()
	if !ok || target != route.Target {
		return false
	}
	admission, cancel := context.WithTimeout(ctx, mqttDeadline)
	defer cancel()
	_, err = d.DetachedVerified(admission, verified, time.Now())
	return err == nil
}
