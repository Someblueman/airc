package config

import (
	"errors"
	"runtime"
	"strconv"
)

func systemVQueueKey(value string) (int32, error) {
	key, err := strconv.ParseInt(value, 10, 32)
	if err != nil || key <= 0 || strconv.FormatInt(key, 10) != value {
		return 0, errors.New("system_v_queue key requires canonical decimal 1 through 2147483647")
	}
	return int32(key), nil
}

func systemVQueueAvailable() bool {
	return runtime.GOARCH == "arm64" && (runtime.GOOS == "darwin" || runtime.GOOS == "linux")
}

// validateSystemVQueues is a prerequisite for service activation. Validate does
// not yet accept an enabled System V listener.
func (c Config) validateSystemVQueues() error {
	listeners := make(map[string]Listener)
	ingress := make(map[int32]bool)
	for _, listener := range c.Listeners {
		if listener.Protocol != "system_v_queue" {
			continue
		}
		key, err := systemVQueueKey(listener.Address)
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
	counts := make(map[string]int)
	for _, route := range c.ReturnRoutes {
		if route.Protocol != "system_v_queue" {
			continue
		}
		key, err := systemVQueueKey(route.Destination)
		if err != nil {
			return err
		}
		if _, ok := listeners[route.Listener]; !ok || ingress[key] || !id(route.RecipientKeyID) || !absolute(route.RecipientPublicKeyFile) {
			return errors.New("invalid System V queue protected return route")
		}
		counts[route.Listener+"\x00"+route.Principal]++
	}
	for _, listener := range listeners {
		if !listener.Enabled {
			continue
		}
		if !systemVQueueAvailable() {
			return errors.New("system_v_queue requires Darwin or Linux ARM64")
		}
		if c.ResponseSigningKey == nil || len(c.Principals) == 0 || len(c.RecipientKeys) == 0 {
			return errors.New("enabled System V queue requires ingress recipient keys, response signing key and principals")
		}
		for _, principal := range c.Principals {
			if counts[listener.ID+"\x00"+principal.ID] != 1 {
				return errors.New("system_v_queue requires exactly one protected return route per principal")
			}
		}
	}
	return nil
}
