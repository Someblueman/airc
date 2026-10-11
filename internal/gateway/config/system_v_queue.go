package config

import (
	"errors"
	"strconv"
)

// SystemVQueueKey parses the canonical configured queue-key representation.
func SystemVQueueKey(value string) (int32, error) {
	key, err := strconv.ParseInt(value, 10, 32)
	if err != nil || key <= 0 || strconv.FormatInt(key, 10) != value {
		return 0, errors.New("system_v_queue key requires canonical decimal 1 through 2147483647")
	}
	return int32(key), nil
}

// validateSystemVQueues checks queue shape, reply queues and platform support.
func (c Config) validateSystemVQueues() error {
	listeners := make(map[string]Listener)
	ingress := make(map[int32]bool)
	for _, listener := range c.Listeners {
		if listener.Protocol != "system_v_queue" {
			continue
		}
		key, err := SystemVQueueKey(listener.Address)
		if err != nil {
			return err
		}
		if listener.POSIXQueue != nil || listener.TLSCertFile != "" || listener.TLSKeyFile != "" || len(listener.AllowedOrigins) > 0 {
			return errors.New("system_v_queue cannot use POSIX attributes, TLS or origins")
		}
		if ingress[key] {
			return errors.New("duplicate System V ingress key")
		}
		listeners[listener.ID] = listener
		ingress[key] = true
	}
	for _, listener := range listeners {
		if reply, err := SystemVQueueKey(listener.ReplyTo); listener.ReplyTo != "" && err == nil && ingress[reply] {
			return errors.New("system_v_queue reply_to aliases an ingress key")
		}
		if !listener.Enabled {
			continue
		}
		if !Available("system_v_queue") {
			return errors.New("system_v_queue requires Darwin ARM64 or Linux AMD64/ARM64")
		}
	}
	return nil
}
