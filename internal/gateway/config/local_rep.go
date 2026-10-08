package config

import (
	"fmt"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func validateLocalREP(l Listener, recipients int) error {
	if l.Protocol != "zeromq" && l.Protocol != "nng" {
		return nil
	}
	name := "ZeroMQ"
	if l.Protocol == "nng" {
		name = "NNG"
	}
	if l.TLSKeyFile != "" || l.TLSCertFile != "" {
		return fmt.Errorf("%s local profile cannot use TLS", name)
	}
	if err := endpoint(l.Address, false); err != nil {
		return err
	}
	if l.Enabled && recipients == 0 {
		return fmt.Errorf("%s requires encrypted ingress recipient keys", name)
	}
	for _, op := range l.Operations {
		switch op {
		case core.Send, core.ContextRead, core.HistoryRead, core.Search:
		default:
			return fmt.Errorf("%s supports send, context, history and search only", name)
		}
	}
	return nil
}
