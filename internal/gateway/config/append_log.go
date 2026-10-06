package config

import (
	"errors"
)

// AppendLog fixes the authenticated owner of one provisioned log.
type AppendLog struct {
	Principal string `json:"principal"`
}

func (c Config) validateAppendLog(l Listener) error {
	if l.Protocol != "append_log" {
		if l.AppendLog != nil {
			return errors.New("append log options require an append_log listener")
		}
		return nil
	}
	if l.AppendLog == nil || !id(l.AppendLog.Principal) {
		return errors.New("append log requires an explicit owner principal")
	}
	for _, p := range c.Principals {
		if p.ID == l.AppendLog.Principal {
			if p.Revoked || len(p.SigningKeys) == 0 {
				return errors.New("append log owner must be active with a signing key")
			}
			return nil
		}
	}
	return errors.New("append log owner is unknown")
}
