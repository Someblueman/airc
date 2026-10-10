package config

import (
	"errors"
	"runtime"
	"strings"

	"github.com/Someblueman/airc/internal/gateway/core"
)

// CANISOTPAvailable reports whether the Linux SocketCAN ISO-TP adapter exists.
func CANISOTPAvailable() bool { return runtime.GOOS == "linux" }

// ParseCANISOTPEndpoint parses interface/RX-ID/TX-ID using lowercase,
// three-digit Classical CAN identifiers.
func ParseCANISOTPEndpoint(endpoint string) (string, uint16, uint16, error) {
	if strings.Count(endpoint, "/") != 2 {
		return "", 0, 0, errors.New("invalid CAN ISO-TP endpoint")
	}
	parts := strings.SplitN(endpoint, "/", 3)
	ifname := parts[0]
	if len(ifname) == 0 || len(ifname) > 15 || ifname == "." || ifname == ".." {
		return "", 0, 0, errors.New("invalid CAN ISO-TP interface name")
	}
	for _, b := range []byte(ifname) {
		if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_' || b == '-' || b == '.') {
			return "", 0, 0, errors.New("invalid CAN ISO-TP interface name")
		}
	}
	rx, okRX := canID(parts[1])
	tx, okTX := canID(parts[2])
	if !okRX || !okTX || rx == tx {
		return "", 0, 0, errors.New("CAN ISO-TP endpoint requires distinct canonical 11-bit identifiers")
	}
	return ifname, rx, tx, nil
}

func canID(value string) (uint16, bool) {
	if len(value) != 3 {
		return 0, false
	}
	var id uint16
	for _, b := range []byte(value) {
		id <<= 4
		switch {
		case b >= '0' && b <= '9':
			id |= uint16(b - '0')
		case b >= 'a' && b <= 'f':
			id |= uint16(b-'a') + 10
		default:
			return 0, false
		}
	}
	return id, id <= 0x7ff
}

func (c Config) validateCANISOTP() error {
	listeners := make(map[string]Listener)
	for _, listener := range c.Listeners {
		if listener.Protocol != "can_isotp" {
			continue
		}
		if _, _, _, err := ParseCANISOTPEndpoint(listener.Address); err != nil {
			return err
		}
		if listener.TLSCertFile != "" || listener.TLSKeyFile != "" || len(listener.AllowedOrigins) != 0 || listener.AnonymousPrincipal != "" {
			return errors.New("CAN ISO-TP cannot use TLS, origins or anonymous access")
		}
		if len(listener.Operations) == 0 || operations(listener.Operations) != nil {
			return errors.New("CAN ISO-TP requires explicit supported operations")
		}
		for _, op := range listener.Operations {
			if op != core.Send && op != core.ContextRead && op != core.HistoryRead && op != core.Search {
				return errors.New("CAN ISO-TP supports only send, context.read, history.read and search")
			}
		}
		listeners[listener.ID] = listener
	}

	// ISO-TP owns both receive and transmit flow-control identifiers on an
	// interface, so no enabled listener may overlap either identifier.
	for id, listener := range listeners {
		if !listener.Enabled {
			continue
		}
		iface, rx, tx, _ := ParseCANISOTPEndpoint(listener.Address)
		for otherID, other := range listeners {
			if otherID <= id || !other.Enabled {
				continue
			}
			otherIface, otherRX, otherTX, _ := ParseCANISOTPEndpoint(other.Address)
			if iface == otherIface && (rx == otherRX || rx == otherTX || tx == otherRX || tx == otherTX) {
				return errors.New("enabled CAN ISO-TP listeners cannot overlap identifiers on an interface")
			}
		}
	}

	counts := make(map[string]int)
	for _, route := range c.ReturnRoutes {
		listener, exists := listeners[route.Listener]
		if route.Protocol != "can_isotp" && !exists {
			continue
		}
		if !exists || route.Protocol != "can_isotp" || listener.Protocol != "can_isotp" || !id(route.RecipientKeyID) || !absolute(route.RecipientPublicKeyFile) {
			return errors.New("invalid protected CAN ISO-TP return route")
		}
		iface, rx, tx, err := ParseCANISOTPEndpoint(listener.Address)
		if err != nil {
			return errors.New("invalid CAN ISO-TP listener endpoint")
		}
		routeIface, routeRX, routeTX, err := ParseCANISOTPEndpoint(route.Destination)
		if err != nil || routeIface != iface || routeRX != tx || routeTX != rx {
			return errors.New("CAN ISO-TP return route must use the reverse listener endpoint")
		}
		counts[route.Listener+"\x00"+route.Principal]++
	}

	for _, listener := range listeners {
		if !listener.Enabled {
			continue
		}
		if !CANISOTPAvailable() {
			return errors.New("enabled CAN ISO-TP listener requires Linux")
		}
		if c.ResponseSigningKey == nil || len(c.RecipientKeys) == 0 || len(c.Principals) == 0 {
			return errors.New("CAN ISO-TP requires ingress recipient keys, response signing key and principals")
		}
		for _, principal := range c.Principals {
			if len(principal.SigningKeys) == 0 || counts[listener.ID+"\x00"+principal.ID] != 1 {
				return errors.New("CAN ISO-TP requires a signing principal with exactly one protected return route")
			}
		}
	}
	return nil
}
