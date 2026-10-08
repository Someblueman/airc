package config

import (
	"errors"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func validateZeroMQ(l Listener, recipients int) error {
	if l.Protocol != "zeromq" {
		return nil
	}
	if l.TLSKeyFile != "" || l.TLSCertFile != "" {
		return errors.New("ZeroMQ local profile cannot use TLS")
	}
	if err := endpoint(l.Address, false); err != nil {
		return err
	}
	if l.Enabled && recipients == 0 {
		return errors.New("ZeroMQ requires encrypted ingress recipient keys")
	}
	for _, op := range l.Operations {
		switch op {
		case core.Send, core.ContextRead, core.HistoryRead, core.Search:
		default:
			return errors.New("ZeroMQ supports send, context, history and search only")
		}
	}
	return nil
}
