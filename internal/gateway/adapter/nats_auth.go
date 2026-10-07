package adapter

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/jsoninput"
	"github.com/Someblueman/airc/internal/gateway/natswire"
)

const natsDeadline = 3 * time.Second

type natsInfo struct {
	ServerID     string `json:"server_id"`
	ServerName   string `json:"server_name"`
	Version      string `json:"version"`
	Go           string `json:"go"`
	Host         string `json:"host"`
	Port         int    `json:"port"`
	Headers      bool   `json:"headers"`
	MaxPayload   int    `json:"max_payload"`
	Proto        int    `json:"proto"`
	AuthRequired bool   `json:"auth_required"`
	TLSRequired  bool   `json:"tls_required"`
}

type natsConnect struct {
	Verbose      *bool   `json:"verbose"`
	Pedantic     bool    `json:"pedantic"`
	TLSRequired  bool    `json:"tls_required"`
	AuthToken    string  `json:"auth_token"`
	User         *string `json:"user"`
	Pass         *string `json:"pass"`
	JWT          *string `json:"jwt"`
	NKey         *string `json:"nkey"`
	Signature    *string `json:"sig"`
	Name         string  `json:"name"`
	Lang         string  `json:"lang"`
	Version      string  `json:"version"`
	Protocol     int     `json:"protocol"`
	Echo         *bool   `json:"echo"`
	Headers      bool    `json:"headers"`
	NoResponders bool    `json:"no_responders"`
}

type natsAuthenticated struct {
	conn     *tls.Conn
	reader   *bufio.Reader
	identity auth.Identity
	verbose  bool
}

func natsMetadata(s string) bool {
	if len(s) > 255 || !utf8.ValidString(s) {
		return false
	}
	for _, c := range s {
		if unicode.IsControl(c) {
			return false
		}
	}
	return true
}

// authenticateNATS owns only INFO, TLS and CONNECT negotiation. The caller owns
// the socket before/after this call, must close on error and retain reader on
// success. Principal admission and CONNECT +OK belong to the connection owner.
func authenticateNATS(parent context.Context, conn net.Conn, security *tls.Config, v *auth.Verifier, routes []config.NATSRoute, info natsInfo) (natsAuthenticated, error) {
	if conn == nil || security == nil || v == nil || len(routes) < 1 || len(routes) > 128 || info.ServerID == "" || info.ServerName == "" || !natsMetadata(info.ServerID) || !natsMetadata(info.ServerName) || !natsMetadata(info.Host) || !natsMetadata(info.Version) || !natsMetadata(info.Go) || info.Port < 0 || info.Port > 65535 || info.Headers || info.MaxPayload != natswire.MaxPayload || info.Proto != 1 || !info.AuthRequired || !info.TLSRequired {
		return natsAuthenticated{}, auth.ErrAuthentication
	}
	if _, alreadySecure := conn.(*tls.Conn); alreadySecure {
		return natsAuthenticated{}, auth.ErrAuthentication
	}
	ctx, cancel := context.WithTimeout(parent, natsDeadline)
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
		return natsAuthenticated{}, err
	}
	data, err := json.Marshal(info)
	if err != nil {
		return natsAuthenticated{}, auth.ErrAuthentication
	}
	wire, err := natswire.Encode(natswire.Frame{Command: "INFO", JSON: data})
	if err != nil {
		return natsAuthenticated{}, auth.ErrAuthentication
	}
	if _, err := io.Copy(conn, bytes.NewReader(wire)); err != nil {
		return natsAuthenticated{}, err
	}
	tlsConfig := security.Clone()
	tlsConfig.MinVersion = max(tlsConfig.MinVersion, tls.VersionTLS13)
	secure := tls.Server(conn, tlsConfig)
	if err := secure.HandshakeContext(ctx); err != nil {
		return natsAuthenticated{}, err
	}
	if secure.ConnectionState().Version != tls.VersionTLS13 {
		return natsAuthenticated{}, auth.ErrAuthentication
	}
	reader := bufio.NewReader(secure)
	f, err := natswire.Read(reader)
	if err != nil {
		return natsAuthenticated{}, err
	}
	defer clear(f.JSON)
	identity, verbose, err := authenticateNATSFrame(v, f, routes)
	if err != nil {
		return natsAuthenticated{}, err
	}
	if err := ctx.Err(); err != nil {
		return natsAuthenticated{}, err
	}
	return natsAuthenticated{secure, reader, identity, verbose}, nil
}

func authenticateNATSFrame(v *auth.Verifier, f natswire.Frame, routes []config.NATSRoute) (auth.Identity, bool, error) {
	refuse := func() (auth.Identity, bool, error) { return auth.Identity{}, false, auth.ErrAuthentication }
	if v == nil || f.Command != "CONNECT" || len(routes) < 1 || len(routes) > 128 {
		return refuse()
	}
	var c natsConnect
	if jsoninput.Decode(f.JSON, &c, natswire.MaxLine) != nil || len(c.AuthToken) != 64 || c.User != nil || c.Pass != nil || c.JWT != nil || c.NKey != nil || c.Signature != nil || c.Protocol < 0 || c.Protocol > 1 || c.Headers || c.NoResponders || !natsMetadata(c.Name) || !natsMetadata(c.Lang) || !natsMetadata(c.Version) {
		return refuse()
	}
	identity, err := v.Authenticate(c.AuthToken)
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
	verbose := true
	if c.Verbose != nil {
		verbose = *c.Verbose
	}
	return identity, verbose, nil
}
