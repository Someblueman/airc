package service

import (
	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func loadConnectionResponseProtection(c config.Config, l config.Listener) (*adapter.ConnectionResponseProtection, error) {
	if c.ResponseSigningKey == nil {
		return nil, store.ErrInvalid
	}
	private, err := auth.ReadResponseSigningKey(c.ResponseSigningKey.PrivateKeyFile)
	if err != nil {
		return nil, err
	}
	defer clear(private)
	sealer, err := auth.NewResponseSealer(c.ResponseSigningKey.ID, private)
	if err != nil {
		return nil, err
	}
	bindings := make(map[string]string)
	for _, p := range c.Principals {
		bindings[p.ID] = p.Binding
	}
	var routes []adapter.ConnectionReturnKey
	for _, route := range c.ReturnRoutes {
		if route.Protocol != l.Protocol || route.Listener != l.ID {
			continue
		}
		recipient, err := auth.ReadResponseRecipientKey(route.RecipientPublicKeyFile)
		if err != nil {
			return nil, err
		}
		routes = append(routes, adapter.ConnectionReturnKey{ID: route.ID, Principal: route.Principal, Binding: bindings[route.Principal], RecipientID: route.RecipientKeyID, Recipient: recipient})
	}
	return adapter.NewConnectionResponseProtection(sealer, c.Realm, routes)
}
