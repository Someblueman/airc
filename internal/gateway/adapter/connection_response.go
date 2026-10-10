package adapter

import (
	"crypto/ecdsa"
	"encoding/json"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// ConnectionReturnKey protects a principal's results on a connection without TLS.
// Connection addresses and transport handles confer no operation authority.
type ConnectionReturnKey struct {
	ID, Principal, Binding, RecipientID string
	Recipient                           *ecdsa.PublicKey
}

type ConnectionResponseProtection struct {
	sealer *auth.ResponseSealer
	realm  string
	routes map[string]ConnectionReturnKey
}

func NewConnectionResponseProtection(sealer *auth.ResponseSealer, realm string, routes []ConnectionReturnKey) (*ConnectionResponseProtection, error) {
	if sealer == nil || !validUDPID(realm) || len(routes) == 0 || len(routes) > 1024 {
		return nil, store.ErrInvalid
	}
	p := &ConnectionResponseProtection{sealer: sealer, realm: realm, routes: make(map[string]ConnectionReturnKey)}
	for _, route := range routes {
		if !validUDPID(route.ID) || !validUDPID(route.Principal) || !validUDPID(route.Binding) || !validUDPID(route.RecipientID) || route.Recipient == nil {
			return nil, store.ErrInvalid
		}
		route.Recipient = cloneUDPPublicKey(route.Recipient)
		key := route.Principal + "\x00" + route.Binding
		if route.Recipient == nil || p.routes[key].Recipient != nil {
			return nil, store.ErrInvalid
		}
		p.routes[key] = route
	}
	return p, nil
}

func (p *ConnectionResponseProtection) seal(verified dispatch.VerifiedMessage, response FrameResponse) (string, error) {
	route, ok := p.routes[verified.PrincipalID()+"\x00"+verified.Binding()]
	if !ok {
		return "", store.ErrInvalid
	}
	if response.Result != nil && response.Result.Capabilities != nil {
		response.Result.Capabilities.MaxResponseFrameBytes = auth.MaxResponsePayloadBytes
	}
	payload, err := json.Marshal(response)
	if err != nil || len(payload) > auth.MaxResponsePayloadBytes {
		return "", store.ErrCapacity
	}
	return p.sealer.Seal(auth.ResponseScope{Realm: p.realm, Principal: verified.PrincipalID(), Binding: verified.Binding(), Route: route.ID}, route.RecipientID, route.Recipient, payload, time.Now())
}
