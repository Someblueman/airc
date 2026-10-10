package config

import (
	"errors"
	"net"
	"strconv"
	"strings"

	"github.com/Someblueman/airc/internal/gateway/xmppwire"
)

func (c Config) validateXMPPBridges() error {
	if len(c.XMPPBridges) > 32 {
		return errors.New("XMPP bridge count limit exceeded")
	}
	ids, endpoints := make(map[string]bool), make(map[string]bool)
	for _, bridge := range c.XMPPBridges {
		if ids[bridge.ID] {
			return errors.New("duplicate XMPP bridge ID")
		}
		if err := c.ValidateXMPPBridge(bridge); err != nil {
			return err
		}
		ids[bridge.ID] = true
		host, portText, _ := net.SplitHostPort(bridge.Address)
		ip := net.ParseIP(host)
		port, _ := strconv.Atoi(portText)
		endpointKey := net.JoinHostPort(ip.String(), strconv.Itoa(port))
		if endpoints[endpointKey] {
			return errors.New("duplicate XMPP bridge endpoint")
		}
		endpoints[endpointKey] = true

		bare, _, _ := xmppwire.FullJID(bridge.Account + "/" + bridge.Resource)
		_, domain, _ := strings.Cut(bare, "@")
		if bridge.TLSServerName != domain {
			return errors.New("XMPP TLS server name must match the bridge account domain")
		}
		if c.ResponseSigningKey == nil {
			return errors.New("XMPP bridges require a response signing key")
		}
		for _, route := range bridge.Routes {
			if !id(route.ResponseRecipientKeyID) || !absolute(route.ResponseRecipientPublicKeyFile) {
				return errors.New("every XMPP route requires an encrypted response recipient key")
			}
			principalFound := false
			for _, principal := range c.Principals {
				if principal.ID == route.Principal && !principal.Revoked && len(principal.SigningKeys) > 0 {
					principalFound = true
					break
				}
			}
			if !principalFound {
				return errors.New("XMPP routes require active mapped signing principals")
			}
		}
	}
	return nil
}
