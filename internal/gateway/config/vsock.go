package config

import (
	"errors"
	"runtime"
	"strconv"
	"strings"
)

const maxVSockID = uint32(0xfffffffe)

// VSock bounds the remote peers accepted by a virtio-vsock listener.
type VSock struct {
	AllowedPeerCIDs []uint32 `json:"allowed_peer_cids"`
}

// VSockAvailable reports whether a virtio-vsock implementation exists.
func VSockAvailable() bool { return runtime.GOOS == "linux" }

// ParseVSockEndpoint parses canonical decimal CID:port endpoints.
func ParseVSockEndpoint(endpoint string) (uint32, uint32, error) {
	if strings.Count(endpoint, ":") != 1 {
		return 0, 0, errors.New("invalid virtio-vsock endpoint")
	}
	parts := strings.SplitN(endpoint, ":", 2)
	cidValue, cidErr := strconv.ParseUint(parts[0], 10, 32)
	portValue, portErr := strconv.ParseUint(parts[1], 10, 32)
	if cidErr != nil || portErr != nil || cidValue < 2 || cidValue > uint64(maxVSockID) || portValue < 1024 || portValue > uint64(maxVSockID) || strconv.FormatUint(cidValue, 10) != parts[0] || strconv.FormatUint(portValue, 10) != parts[1] {
		return 0, 0, errors.New("virtio-vsock endpoint requires a canonical CID and port")
	}
	return uint32(cidValue), uint32(portValue), nil
}

func (options *VSock) Validate(endpoint string) error {
	if options == nil {
		return errors.New("missing virtio-vsock options")
	}
	localCID, _, err := ParseVSockEndpoint(endpoint)
	if err != nil {
		return err
	}
	if len(options.AllowedPeerCIDs) < 1 || len(options.AllowedPeerCIDs) > 64 {
		return errors.New("virtio-vsock peer CID count is outside limits")
	}
	seen := make(map[uint32]bool, len(options.AllowedPeerCIDs))
	for _, cid := range options.AllowedPeerCIDs {
		if cid < 2 || cid > maxVSockID || cid == localCID || seen[cid] {
			return errors.New("invalid, local or duplicate virtio-vsock peer CID")
		}
		seen[cid] = true
	}
	return nil
}

func (c Config) validateVSock() error {
	listeners := make(map[string]Listener)
	for _, listener := range c.Listeners {
		if listener.Protocol != "virtio_vsock" {
			if listener.VSock != nil {
				return errors.New("virtio-vsock options require a virtio_vsock listener")
			}
			continue
		}
		if err := listener.VSock.Validate(listener.Address); err != nil {
			return err
		}
		if len(listener.AllowedOrigins) != 0 || listener.AnonymousPrincipal != "" {
			return errors.New("virtio_vsock cannot use origins or anonymous access")
		}
		listeners[listener.ID] = listener
	}

	for _, route := range c.ReturnRoutes {
		if route.Protocol == "virtio_vsock" {
			return errors.New("virtio_vsock uses the connection for responses, not return routes")
		}
		if _, exists := listeners[route.Listener]; exists {
			return errors.New("virtio_vsock listener cannot have a return route")
		}
	}

	for _, listener := range listeners {
		if !listener.Enabled {
			continue
		}
		if !VSockAvailable() {
			return errors.New("virtio_vsock listener requires Linux")
		}
		if listener.TLSCertFile == "" || listener.TLSKeyFile == "" {
			return errors.New("enabled virtio_vsock listener requires TLS certificate and key")
		}
		if len(c.RecipientKeys) == 0 || len(c.Principals) == 0 {
			return errors.New("virtio_vsock requires ingress recipient keys and signed principals")
		}
		for _, principal := range c.Principals {
			if len(principal.SigningKeys) == 0 {
				return errors.New("virtio_vsock requires signed principals")
			}
		}
	}
	return nil
}
