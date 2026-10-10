package config

import (
	"errors"
	"net"
	"net/url"
	"strings"

	"github.com/pion/stun/v4"
)

type WebRTC struct {
	ICEServers         []WebRTCICE `json:"ice_servers"`
	ICETransportPolicy string      `json:"ice_transport_policy,omitempty"`
}

type WebRTCICE struct {
	URLs           []string `json:"urls"`
	Username       string   `json:"username,omitempty"`
	CredentialFile string   `json:"credential_file,omitempty"`
}

// Validate checks the same ICE options used by configured listeners.
func (options WebRTC) Validate() error { return validateWebRTC(&options) }

func validateWebRTC(options *WebRTC) error {
	if options == nil {
		return nil
	}
	if options.ICETransportPolicy != "" && options.ICETransportPolicy != "all" && options.ICETransportPolicy != "relay" {
		return errors.New("invalid WebRTC ICE transport policy")
	}
	if len(options.ICEServers) > 4 {
		return errors.New("WebRTC ICE server limit exceeded")
	}
	totalURLs := 0
	hasTURN := false
	for _, server := range options.ICEServers {
		if len(server.URLs) == 0 || len(server.URLs) > 8 {
			return errors.New("invalid WebRTC ICE server URLs")
		}
		if server.CredentialFile != "" && !absolute(server.CredentialFile) {
			return errors.New("invalid WebRTC ICE credential path")
		}
		serverHasTURN := false
		for _, raw := range server.URLs {
			scheme, err := validateWebRTCURL(raw)
			if err != nil {
				return err
			}
			totalURLs++
			turn := scheme == "turn" || scheme == "turns"
			if turn {
				hasTURN = true
				serverHasTURN = true
			} else if server.Username != "" || server.CredentialFile != "" {
				return errors.New("STUN ICE servers cannot use credentials")
			}
		}
		if serverHasTURN {
			if err := validWebRTCUsername(server.Username); err != nil {
				return err
			}
			if server.CredentialFile == "" {
				return errors.New("TURN ICE servers require username and credential file")
			}
		} else if server.Username != "" || server.CredentialFile != "" {
			return errors.New("TURN ICE servers require username and credential file")
		}
	}
	if totalURLs > 8 {
		return errors.New("WebRTC ICE URL limit exceeded")
	}
	if options.ICETransportPolicy == "relay" && !hasTURN {
		return errors.New("relay ICE policy requires a TURN server")
	}
	return nil
}

func validWebRTCUsername(username string) error {
	if username == "" || len(username) > 256 || strings.ContainsAny(username, "\r\n\x00") {
		return errors.New("invalid TURN username")
	}
	return nil
}

func validateWebRTCURL(raw string) (string, error) {
	if raw == "" || len(raw) > 2048 {
		return "", errors.New("invalid WebRTC ICE URL")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "stun" && u.Scheme != "stuns" && u.Scheme != "turn" && u.Scheme != "turns") || u.User != nil || u.Fragment != "" {
		return "", errors.New("invalid WebRTC ICE URL")
	}
	if u.RawQuery != "" {
		values, err := url.ParseQuery(u.RawQuery)
		if err != nil || len(values) != 1 || len(values["transport"]) != 1 || (values.Get("transport") != "udp" && values.Get("transport") != "tcp") {
			return "", errors.New("invalid WebRTC ICE URL transport")
		}
	}
	// Match the selected ICE implementation's RFC 7064/7065 URI grammar.
	// Hierarchical URLs and path components are not ICE server URIs.
	parsed, err := stun.ParseURI(raw)
	if err != nil || u.Opaque == "" || u.Host != "" || u.Path != "" || parsed.Port < 1 || parsed.Port > 65535 {
		return "", errors.New("invalid WebRTC ICE URL")
	}
	host := parsed.Host
	if host == "" || strings.ContainsAny(host, " \t\r\n\x00") {
		return "", errors.New("invalid WebRTC ICE URL host")
	}
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip == nil {
		if len(host) > 253 || strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") {
			return "", errors.New("invalid WebRTC ICE URL host")
		}
		for _, label := range strings.Split(host, ".") {
			if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
				return "", errors.New("invalid WebRTC ICE URL host")
			}
			for _, r := range label {
				if !(r == '-' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
					return "", errors.New("invalid WebRTC ICE URL host")
				}
			}
		}
	}
	return u.Scheme, nil
}
