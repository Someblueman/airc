package config

import (
	"errors"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func (c Config) validateIRCBridges() error {
	if len(c.IRCBridges) > 32 {
		return errors.New("IRC bridge count limit exceeded")
	}
	ids, addresses := map[string]bool{}, map[string]bool{}
	for _, b := range c.IRCBridges {
		if ids[b.ID] || addresses[b.Address] {
			return errors.New("duplicate IRC bridge identity or endpoint")
		}
		ids[b.ID], addresses[b.Address] = true, true
		if err := c.ValidateIRCBridge(b); err != nil {
			return err
		}
		if b.Enabled {
			for _, route := range b.Routes {
				for _, op := range route.Operations {
					if op != core.Send {
						return errors.New("IRC read replies are not implemented")
					}
				}
			}
		}
	}
	return nil
}
