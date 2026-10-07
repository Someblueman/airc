package service

import (
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func loadSignedResponseSigner(c config.Config) (*auth.ResponseSealer, error) {
	if c.ResponseSigningKey == nil {
		return nil, store.ErrInvalid
	}
	private, err := auth.ReadResponseSigningKey(c.ResponseSigningKey.PrivateKeyFile)
	if err != nil {
		return nil, err
	}
	defer clear(private)
	return auth.NewResponseSealer(c.ResponseSigningKey.ID, private)
}
