package adapter

import (
	"bufio"
	"context"
	"crypto/tls"
	"net"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/stompwire"
)

const stompDeadline = 3 * time.Second

type stompHeartbeat struct{ send, receive time.Duration }

// authenticateSTOMP leaves CONNECTED and connection admission to the owner.
// The owner retains reader, including any bytes read ahead past CONNECT.
func authenticateSTOMP(parent context.Context, conn net.Conn, reader *bufio.Reader, v *auth.Verifier, routes []config.STOMPRoute, realm string) (auth.Identity, stompHeartbeat, error) {
	secure, ok := conn.(*tls.Conn)
	if !ok || reader == nil || v == nil || realm == "" || len(routes) == 0 || len(routes) > 128 {
		return auth.Identity{}, stompHeartbeat{}, auth.ErrAuthentication
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
		return auth.Identity{}, stompHeartbeat{}, err
	}
	if err := secure.HandshakeContext(ctx); err != nil {
		return auth.Identity{}, stompHeartbeat{}, err
	}
	if secure.ConnectionState().Version != tls.VersionTLS13 {
		return auth.Identity{}, stompHeartbeat{}, auth.ErrAuthentication
	}
	for {
		f, err := stompwire.Read(reader)
		if err != nil {
			return auth.Identity{}, stompHeartbeat{}, err
		}
		if f.Command == "" {
			continue
		}
		identity, heartbeat, err := authenticateSTOMPFrame(v, f, routes, realm)
		if err != nil {
			return auth.Identity{}, stompHeartbeat{}, err
		}
		if err := ctx.Err(); err != nil {
			return auth.Identity{}, stompHeartbeat{}, err
		}
		return identity, heartbeat, nil
	}
}

func authenticateSTOMPFrame(v *auth.Verifier, f stompwire.Frame, routes []config.STOMPRoute, realm string) (auth.Identity, stompHeartbeat, error) {
	refuse := func() (auth.Identity, stompHeartbeat, error) {
		return auth.Identity{}, stompHeartbeat{}, auth.ErrAuthentication
	}
	if v == nil || realm == "" || len(routes) == 0 || len(routes) > 128 || (f.Command != "CONNECT" && f.Command != "STOMP") || len(f.Body) != 0 {
		return refuse()
	}
	versions, _ := f.Header("accept-version")
	if len(versions) > 32 {
		return refuse()
	}
	found := false
	for _, version := range strings.Split(versions, ",") {
		found = found || version == "1.2"
	}
	host, _ := f.Header("host")
	login, _ := f.Header("login")
	passcode, _ := f.Header("passcode")
	if !found || host != realm || login == "" || len(login) > 255 || len(passcode) != 64 {
		return refuse()
	}
	identity, err := v.Authenticate(passcode)
	if err != nil || identity.ID() != login {
		return refuse()
	}
	allowed := false
	for _, route := range routes {
		allowed = allowed || route.Principal == identity.ID()
	}
	if !allowed {
		return refuse()
	}
	var heartbeat stompHeartbeat
	if raw, ok := f.Header("heart-beat"); ok {
		parts := strings.Split(raw, ",")
		if len(parts) != 2 {
			return refuse()
		}
		var values [2]time.Duration
		for i, part := range parts {
			if len(part) == 0 || len(part) > 5 {
				return refuse()
			}
			var n int
			for _, digit := range part {
				if digit < '0' || digit > '9' {
					return refuse()
				}
				n = n*10 + int(digit-'0')
			}
			if n > 10000 {
				return refuse()
			}
			if n != 0 {
				values[i] = time.Duration(max(n, 1000)) * time.Millisecond
			}
		}
		// Server offers 1000,1000. Peer receive controls our send interval;
		// peer send controls our receive expectation. Zero disables that side.
		heartbeat = stompHeartbeat{send: values[1], receive: values[0]}
	}
	return identity, heartbeat, nil
}
