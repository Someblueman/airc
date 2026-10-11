package adapter

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"net"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/stompwire"
)

const stompDeadline = 3 * time.Second

type stompHeartbeat struct{ send, receive time.Duration }

// stompRefusal is a CONNECT the server answers with an ERROR frame.
type stompRefusal struct{ message string }

func (r stompRefusal) Error() string { return r.message }

// readSTOMPConnect performs the optional TLS handshake and reads CONNECT (or
// STOMP), leaving CONNECTED and access admission to the owner. The owner
// retains reader, including any bytes read ahead past CONNECT. The passcode
// is the listener access password, never an identity; login and host carry
// no authority, so any host is accepted.
func readSTOMPConnect(parent context.Context, conn net.Conn, reader *bufio.Reader) (core.Password, stompHeartbeat, error) {
	if reader == nil {
		return core.Password{}, stompHeartbeat{}, auth.ErrAuthentication
	}
	ctx, cancel := context.WithTimeout(parent, stompDeadline)
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
		return core.Password{}, stompHeartbeat{}, err
	}
	if secure, ok := conn.(*tls.Conn); ok {
		if err := secure.HandshakeContext(ctx); err != nil {
			return core.Password{}, stompHeartbeat{}, err
		}
		if secure.ConnectionState().Version != tls.VersionTLS13 {
			return core.Password{}, stompHeartbeat{}, auth.ErrAuthentication
		}
	}
	for {
		f, err := stompwire.Read(reader)
		if err != nil {
			return core.Password{}, stompHeartbeat{}, err
		}
		if f.Command == "" {
			continue
		}
		password, heartbeat, err := parseSTOMPConnect(f)
		if err != nil {
			return core.Password{}, stompHeartbeat{}, err
		}
		if err := ctx.Err(); err != nil {
			return core.Password{}, stompHeartbeat{}, err
		}
		return password, heartbeat, nil
	}
}

func parseSTOMPConnect(f stompwire.Frame) (core.Password, stompHeartbeat, error) {
	refuse := func(message string) (core.Password, stompHeartbeat, error) {
		return core.Password{}, stompHeartbeat{}, stompRefusal{message}
	}
	if (f.Command != "CONNECT" && f.Command != "STOMP") || len(f.Body) != 0 {
		return refuse("expected CONNECT")
	}
	versions, _ := f.Header("accept-version")
	if len(versions) > 32 {
		return refuse("unsupported STOMP version")
	}
	found := false
	for _, version := range strings.Split(versions, ",") {
		found = found || version == "1.2"
	}
	if !found {
		return refuse("supported STOMP version: 1.2")
	}
	var password core.Password
	if passcode, _ := f.Header("passcode"); passcode != "" {
		var err error
		if password, err = core.NewPassword(passcode); err != nil {
			return refuse("invalid passcode")
		}
	}
	var heartbeat stompHeartbeat
	if raw, ok := f.Header("heart-beat"); ok {
		parts := strings.Split(raw, ",")
		if len(parts) != 2 {
			return refuse("invalid heart-beat")
		}
		var values [2]time.Duration
		for i, part := range parts {
			if len(part) == 0 || len(part) > 9 {
				return refuse("invalid heart-beat")
			}
			var n int
			for _, digit := range part {
				if digit < '0' || digit > '9' {
					return refuse("invalid heart-beat")
				}
				n = n*10 + int(digit-'0')
			}
			if n != 0 {
				values[i] = time.Duration(min(max(n, 1000), 60000)) * time.Millisecond
			}
		}
		// Server offers 1000,1000. Peer receive controls our send interval;
		// peer send controls our receive expectation. Zero disables that side.
		heartbeat = stompHeartbeat{send: values[1], receive: values[0]}
	}
	return password, heartbeat, nil
}

// stompRefusalMessage reports the ERROR message for a refused CONNECT.
func stompRefusalMessage(err error) (string, bool) {
	var refusal stompRefusal
	if errors.As(err, &refusal) {
		return refusal.message, true
	}
	return "", false
}
