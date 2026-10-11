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
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
)

const amqpDeadline = 3 * time.Second

// openAMQPSecurity performs the optional TLS handshake and the optional SASL
// layer, leaving the stream at the first AMQP frame. SASL is not an identity:
// ANONYMOUS, or PLAIN whose password is the listener access password (the
// user name is ignored); a client may also skip SASL when the listener has
// no password. The connection's author comes from its requests, or from an
// account.identify on the connection. The owner closes on error.
func openAMQPSecurity(parent context.Context, conn net.Conn, d *dispatch.Dispatcher, session *dispatch.Session) error {
	if d == nil || session == nil {
		return auth.ErrAuthentication
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
		return err
	}
	if secure, ok := conn.(*tls.Conn); ok {
		if err := secure.HandshakeContext(ctx); err != nil {
			return err
		}
		if secure.ConnectionState().Version != tls.VersionTLS13 {
			return auth.ErrAuthentication
		}
	}
	protocol, err := amqpwire.ReadProtocolHeader(conn)
	if err != nil {
		_ = amqpwire.WriteProtocolHeader(conn, amqpwire.AMQPProtocol)
		return err
	}
	if protocol == amqpwire.AMQPProtocol {
		// A client without SASL is admitted only where no password is needed;
		// otherwise answer with the SASL header and close (AMQP 1.0 5.3.1).
		if err := d.Connect(session, core.Password{}); err != nil {
			_ = amqpwire.WriteProtocolHeader(conn, amqpwire.SASLProtocol)
			return err
		}
		if err := amqpwire.WriteProtocolHeader(conn, amqpwire.AMQPProtocol); err != nil {
			return err
		}
		return ctx.Err()
	}
	if err := amqpwire.WriteProtocolHeader(conn, amqpwire.SASLProtocol); err != nil {
		return err
	}
	offered := []amqpwire.Value{{Code: 0xa3, Data: []byte("PLAIN")}}
	if !d.PasswordRequired() {
		offered = append([]amqpwire.Value{{Code: 0xa3, Data: []byte("ANONYMOUS")}}, offered...)
	}
	if err := writeAMQPSASL(conn, amqpwire.SASLPerformative{Kind: 0x40, Fields: []amqpwire.Value{{Code: 0xe0, Children: offered}}}); err != nil {
		return err
	}
	init, err := readAMQPSASL(conn)
	if err != nil || init.Kind != 0x41 {
		return auth.ErrAuthentication
	}
	// The decoded initial response and its enclosing frame own distinct copies.
	for _, field := range init.Fields {
		defer clear(field.Data)
	}
	refuse := func() error {
		_ = writeAMQPSASL(conn, amqpwire.SASLPerformative{Kind: 0x44, Fields: []amqpwire.Value{{Code: 0x50, Number: 1}}})
		return auth.ErrAuthentication
	}
	var password core.Password
	switch string(init.Fields[0].Data) {
	case "ANONYMOUS":
	case "PLAIN":
		var response []byte
		if len(init.Fields) > 1 && init.Fields[1].Code != 0x40 {
			response = init.Fields[1].Data
		} else {
			if err := writeAMQPSASL(conn, amqpwire.SASLPerformative{Kind: 0x42, Fields: []amqpwire.Value{{Code: 0xa0}}}); err != nil {
				return err
			}
			reply, err := readAMQPSASL(conn)
			if err != nil || reply.Kind != 0x43 {
				return auth.ErrAuthentication
			}
			response = reply.Fields[0].Data
			defer clear(response)
		}
		if password, err = amqpPlainPassword(response); err != nil {
			return refuse()
		}
	default:
		return refuse()
	}
	if err := d.Connect(session, password); err != nil {
		return refuse()
	}
	if err := writeAMQPSASL(conn, amqpwire.SASLPerformative{Kind: 0x44, Fields: []amqpwire.Value{{Code: 0x50}}}); err != nil {
		return err
	}
	// The client speaks first after SASL too; the server answers its header.
	if protocol, err := amqpwire.ReadProtocolHeader(conn); err != nil || protocol != amqpwire.AMQPProtocol {
		_ = amqpwire.WriteProtocolHeader(conn, amqpwire.AMQPProtocol)
		return errors.Join(amqpwire.ErrHeader, err)
	}
	if err := amqpwire.WriteProtocolHeader(conn, amqpwire.AMQPProtocol); err != nil {
		return err
	}
	return ctx.Err()
}

// amqpPlainPassword takes the password of a SASL PLAIN response
// (authzid NUL authcid NUL passwd); an empty password is none.
func amqpPlainPassword(response []byte) (core.Password, error) {
	authz, rest, ok := bytes.Cut(response, []byte{0})
	if !ok {
		return core.Password{}, auth.ErrAuthentication
	}
	username, password, ok := bytes.Cut(rest, []byte{0})
	if !ok || len(authz) > 255 || len(username) > 255 || bytes.ContainsRune(password, 0) || !utf8.Valid(authz) || !utf8.Valid(username) || !utf8.Valid(password) {
		return core.Password{}, auth.ErrAuthentication
	}
	if len(password) == 0 {
		return core.Password{}, nil
	}
	return core.NewPassword(string(password))
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
