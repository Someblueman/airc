package config

import (
	"errors"
	"runtime"
	"strings"
)

// POSIXQueue sets verified attributes for ingress and all fixed response queues.
// Each message is an existing 1000-byte AGD1 fragment, not a whole RPC.
type POSIXQueue struct {
	Depth int `json:"depth"`
}

func POSIXQueueAvailable() bool {
	return runtime.GOOS == "linux" && (runtime.GOARCH == "arm64" || runtime.GOARCH == "amd64")
}

func posixQueueName(name string) bool {
	const alphanumeric = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	return len(name) >= 2 && len(name) <= 255 && name[0] == '/' && strings.Trim(name[1:2], alphanumeric) == "" && strings.Trim(name[1:], alphanumeric+"_.-") == ""
}

func (c Config) validatePOSIXQueues() error {
	listeners := make(map[string]Listener)
	ingress := make(map[string]bool)
	for _, listener := range c.Listeners {
		if listener.Protocol != "posix_mqueue" {
			if listener.POSIXQueue != nil {
				return errors.New("posix_queue requires posix_mqueue protocol")
			}
			continue
		}
		if !posixQueueName(listener.Address) || listener.POSIXQueue == nil || listener.POSIXQueue.Depth < 1 || listener.POSIXQueue.Depth > 10 {
			return errors.New("POSIX queue requires a bounded /name and depth 1 through 10")
		}
		if listener.TLSCertFile != "" || listener.TLSKeyFile != "" || len(listener.AllowedOrigins) > 0 {
			return errors.New("POSIX queue cannot use TLS or origins")
		}
		listeners[listener.ID] = listener
		ingress[listener.Address] = true
	}
	counts := make(map[string]int)
	for _, route := range c.ReturnRoutes {
		if route.Protocol != "posix_mqueue" {
			continue
		}
		if _, ok := listeners[route.Listener]; !ok || !posixQueueName(route.Destination) || ingress[route.Destination] || !id(route.RecipientKeyID) || !absolute(route.RecipientPublicKeyFile) {
			return errors.New("invalid POSIX queue protected return route")
		}
		counts[route.Listener+"\x00"+route.Principal]++
	}
	for _, listener := range listeners {
		if !listener.Enabled {
			continue
		}
		if !POSIXQueueAvailable() {
			return errors.New("POSIX queue requires Linux ARM64 or AMD64")
		}
		if c.ResponseSigningKey == nil || len(c.Principals) == 0 || len(c.RecipientKeys) == 0 {
			return errors.New("enabled POSIX queue requires ingress recipient keys, response signing key and principals")
		}
		for _, principal := range c.Principals {
			if counts[listener.ID+"\x00"+principal.ID] != 1 {
				return errors.New("POSIX queue requires exactly one protected return route per principal")
			}
		}
	}
	return nil
}
