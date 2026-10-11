package auth

import (
	"encoding/hex"
	"io"
	"net"
	"slices"
	"strconv"
	"strings"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/snmpwire"
)

type snmpGrant struct {
	author                 RouteAuthor
	target                 core.Target
	contextEngine, context string
	key                    *snmpwire.Verifier
	notifications          map[string]config.SNMPNotification
}

// SNMPAuthenticator snapshots explicit engine/user grants and localized keys.
// Source addresses confer no authority. MAC verification confers no timeliness
// or durable admission; callers must perform those checks atomically later.
type SNMPAuthenticator struct{ grants map[[2]string]snmpGrant }

func NewSNMPAuthenticator(options config.SNMP) (*SNMPAuthenticator, error) {
	if config.ValidateSNMPGrants(options) != nil {
		return nil, ErrCredential
	}
	a := &SNMPAuthenticator{grants: make(map[[2]string]snmpGrant, len(options.Routes))}
	for _, r := range options.Routes {
		author, err := LoadBoundAuthor(r.BoundAuthor)
		if err != nil {
			return nil, err
		}
		material, err := readSNMPKey(r.AuthenticationKeyFile, r.Authentication)
		if err != nil {
			return nil, err
		}
		key, err := snmpwire.NewVerifier(r.Authentication, material)
		clear(material)
		if err != nil {
			return nil, ErrCredential
		}
		g := snmpGrant{author: author, target: r.Target, contextEngine: r.ContextEngineID, context: r.ContextName, key: key, notifications: make(map[string]config.SNMPNotification, len(r.Notifications))}
		for _, n := range r.Notifications {
			n.Fields = slices.Clone(n.Fields)
			g.notifications[n.OID] = n
		}
		a.grants[[2]string{r.EngineID, r.User}] = g
	}
	return a, nil
}

func readSNMPKey(path, protocol string) ([]byte, error) {
	size := 32
	if protocol == snmpwire.HMACSHA196 {
		size = 20
	}
	f, err := openPrivate(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	encoded, err := io.ReadAll(io.LimitReader(f, int64(size*2+2)))
	if err != nil {
		return nil, ErrCredential
	}
	defer clear(encoded)
	if len(encoded) == size*2+1 && encoded[len(encoded)-1] == '\n' {
		encoded = encoded[:len(encoded)-1]
	}
	if len(encoded) != size*2 {
		return nil, ErrCredential
	}
	for _, b := range encoded {
		if !(b >= '0' && b <= '9' || b >= 'a' && b <= 'f') {
			return nil, ErrCredential
		}
	}
	material := make([]byte, size)
	if _, err := hex.Decode(material, encoded); err != nil {
		clear(material)
		return nil, ErrCredential
	}
	return material, nil
}

// VerifiedSNMPNotification is opaque: neither the grant's author nor the
// authenticated packet/body can be changed through exported aliases. A zero
// value is invalid.
type VerifiedSNMPNotification struct {
	author RouteAuthor
	target core.Target
	packet snmpwire.Packet
	body   string
}

func (n VerifiedSNMPNotification) Valid() bool         { return n.author.Valid() }
func (n VerifiedSNMPNotification) Author() RouteAuthor { return n.author }
func (n VerifiedSNMPNotification) Target() core.Target { return n.target }
func (n VerifiedSNMPNotification) Body() string        { return n.body }
func (n VerifiedSNMPNotification) Packet() snmpwire.Packet {
	p, _ := snmpwire.Parse(n.packet.Raw)
	return p
}

func (a *SNMPAuthenticator) Verify(raw []byte) (VerifiedSNMPNotification, error) {
	if a == nil {
		return VerifiedSNMPNotification{}, ErrAuthentication
	}
	p, err := snmpwire.Parse(raw)
	if err != nil {
		return VerifiedSNMPNotification{}, ErrAuthentication
	}
	g, ok := a.grants[[2]string{hex.EncodeToString(p.EngineID), p.User}]
	if !ok {
		return VerifiedSNMPNotification{}, ErrAuthentication
	}
	p, err = g.key.Verify(p.Raw)
	if err != nil || hex.EncodeToString(p.ContextEngineID) != g.contextEngine || p.Context != g.context {
		return VerifiedSNMPNotification{}, ErrAuthentication
	}
	n, ok := g.notifications[p.Notification]
	if !ok {
		return VerifiedSNMPNotification{}, ErrForbidden
	}
	body, err := renderSNMP(n, p.Variables)
	if err != nil {
		return VerifiedSNMPNotification{}, err
	}
	target := g.target
	r := core.Request{Version: 1, RequestID: "snmp-validation", Operation: core.Send, Target: &target, Message: body}
	if r.Normalize() != nil || r.Message != body || r.Target == nil || *r.Target != g.target {
		return VerifiedSNMPNotification{}, ErrForbidden
	}
	return VerifiedSNMPNotification{g.author, g.target, p, body}, nil
}

func renderSNMP(n config.SNMPNotification, variables []snmpwire.Variable) (string, error) {
	allowed := make(map[string]config.SNMPField, len(n.Fields))
	for _, f := range n.Fields {
		allowed[f.OID] = f
	}
	values := make(map[string]string, len(variables))
	for _, v := range variables {
		f, ok := allowed[v.OID]
		if !ok {
			return "", ErrForbidden
		}
		value, ok := snmpValue(f.Type, v)
		if !ok {
			return "", ErrForbidden
		}
		values[v.OID] = value
	}
	var body strings.Builder
	body.WriteString("SNMP ")
	body.WriteString(n.Name)
	for _, f := range n.Fields {
		value, ok := values[f.OID]
		if !ok {
			if f.Required {
				return "", ErrForbidden
			}
			continue
		}
		body.WriteByte('\n')
		body.WriteString(f.Name)
		body.WriteString(": ")
		body.WriteString(value)
		if body.Len() > 4096 {
			return "", ErrForbidden
		}
	}
	return body.String(), nil
}
func snmpValue(kind string, v snmpwire.Variable) (string, bool) {
	switch kind {
	case "text":
		if v.Type == 4 {
			return string(v.Bytes), true
		}
	case "hex":
		if v.Type == 4 {
			return hex.EncodeToString(v.Bytes), true
		}
	case "integer":
		if v.Type == 2 {
			return strconv.FormatInt(v.Integer, 10), true
		}
	case "unsigned":
		if v.Type == 65 || v.Type == 66 || v.Type == 67 || v.Type == 70 {
			return strconv.FormatUint(v.Unsigned, 10), true
		}
	case "oid":
		if v.Type == 6 {
			return v.ObjectID, true
		}
	case "ip":
		if v.Type == 64 {
			return net.IP(v.Bytes).String(), true
		}
	}
	return "", false
}
