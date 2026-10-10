package adapter

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"net"
	"time"
	"unicode/utf8"

	"github.com/Someblueman/airc/internal/gateway/amqpwire"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
)

const amqpDeadline = 3 * time.Second

// authenticateAMQP performs only the TLS/SASL phase, leaving the stream at the
// first AMQP frame. The connection owner must close on error and join its whole
// lifetime. Authentication alone does not authorize a link or dispatch request.
func authenticateAMQP(parent context.Context, conn net.Conn, v *auth.Verifier, routes []config.AMQPRoute) (auth.Identity, error) {
	secure, ok := conn.(*tls.Conn)
	if !ok || v == nil || len(routes) == 0 || len(routes) > 128 {
		return auth.Identity{}, auth.ErrAuthentication
	}
	ctx, cancel := context.WithTimeout(parent, amqpDeadline)
	closed := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { closeMQTTConnection(conn); close(closed) })
	defer func() {
		if !stop() {
			<-closed
		}
		cancel()
	}()
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return auth.Identity{}, err
	}
	if err := secure.HandshakeContext(ctx); err != nil {
		return auth.Identity{}, err
	}
	if secure.ConnectionState().Version != tls.VersionTLS13 {
		return auth.Identity{}, auth.ErrAuthentication
	}
	if err := amqpwire.ExchangeProtocolHeader(conn, conn, amqpwire.SASLProtocol); err != nil {
		return auth.Identity{}, err
	}
	mechanisms := amqpwire.SASLPerformative{Kind: 0x40, Fields: []amqpwire.Value{{Code: 0xe0, Children: []amqpwire.Value{{Code: 0xa3, Data: []byte("PLAIN")}}}}}
	if err := writeAMQPSASL(conn, mechanisms); err != nil {
		return auth.Identity{}, err
	}
	init, err := readAMQPSASL(conn)
	if err != nil || init.Kind != 0x41 {
		return auth.Identity{}, auth.ErrAuthentication
	}
	// The decoded initial response and its enclosing frame own distinct copies.
	for _, field := range init.Fields {
		defer clear(field.Data)
	}
	refuse := func() (auth.Identity, error) {
		_ = writeAMQPSASL(conn, amqpwire.SASLPerformative{Kind: 0x44, Fields: []amqpwire.Value{{Code: 0x50, Number: 1}}})
		return auth.Identity{}, auth.ErrAuthentication
	}
	if !bytes.Equal(init.Fields[0].Data, []byte("PLAIN")) {
		return refuse()
	}
	var response []byte
	if len(init.Fields) > 1 && init.Fields[1].Code != 0x40 {
		response = init.Fields[1].Data
	} else {
		if err := writeAMQPSASL(conn, amqpwire.SASLPerformative{Kind: 0x42, Fields: []amqpwire.Value{{Code: 0xa0}}}); err != nil {
			return auth.Identity{}, err
		}
		reply, err := readAMQPSASL(conn)
		if err != nil || reply.Kind != 0x43 {
			return auth.Identity{}, auth.ErrAuthentication
		}
		response = reply.Fields[0].Data
		defer clear(response)
	}
	identity, err := authenticateAMQPPlain(v, response)
	if err != nil {
		return refuse()
	}
	allowed := false
	for _, route := range routes {
		allowed = allowed || route.Principal == identity.ID()
	}
	if !allowed {
		return refuse()
	}
	if err := writeAMQPSASL(conn, amqpwire.SASLPerformative{Kind: 0x44, Fields: []amqpwire.Value{{Code: 0x50}}}); err != nil {
		return auth.Identity{}, err
	}
	if err := amqpwire.ExchangeProtocolHeader(conn, conn, amqpwire.AMQPProtocol); err != nil {
		return auth.Identity{}, err
	}
	if err := ctx.Err(); err != nil {
		return auth.Identity{}, err
	}
	return identity, nil
}

func authenticateAMQPPlain(v *auth.Verifier, response []byte) (auth.Identity, error) {
	authz, rest, ok := bytes.Cut(response, []byte{0})
	if !ok {
		return auth.Identity{}, auth.ErrAuthentication
	}
	username, password, ok := bytes.Cut(rest, []byte{0})
	if !ok || len(authz) > 255 || len(username) == 0 || len(username) > 255 || len(password) != 64 || bytes.ContainsRune(password, 0) || !utf8.Valid(authz) || !utf8.Valid(username) || !utf8.Valid(password) {
		return auth.Identity{}, auth.ErrAuthentication
	}
	identity, err := v.Authenticate(string(password))
	if err != nil || string(username) != identity.ID() || len(authz) != 0 && string(authz) != identity.ID() {
		return auth.Identity{}, auth.ErrAuthentication
	}
	return identity, nil
}

func readAMQPSASL(conn net.Conn) (amqpwire.SASLPerformative, error) {
	frame, err := amqpwire.ReadFrame(conn, amqpwire.InitialFrameLimit)
	if err != nil {
		return amqpwire.SASLPerformative{}, err
	}
	defer clear(frame.Body)
	return amqpwire.DecodeSASL(frame)
}

func writeAMQPSASL(conn net.Conn, p amqpwire.SASLPerformative) error {
	frame, err := amqpwire.EncodeSASL(p)
	if err != nil {
		return err
	}
	if err := amqpwire.WriteFrame(conn, frame, amqpwire.InitialFrameLimit); err != nil {
		return errors.Join(auth.ErrAuthentication, err)
	}
	return nil
}
