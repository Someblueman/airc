package config

import (
	"errors"
	"net"
	"strconv"
)

func (c Config) validateMatrixBridges() error {
	if len(c.MatrixBridges) > 32 {
		return errors.New("matrix bridge count limit exceeded")
	}
	ids, endpoints := make(map[string]bool), make(map[string]bool)
	for _, bridge := range c.MatrixBridges {
		if ids[bridge.ID] {
			return errors.New("duplicate matrix bridge ID")
		}
		if err := c.ValidateMatrixBridge(bridge); err != nil {
			return err
		}
		ids[bridge.ID] = true
		host, portText, _ := net.SplitHostPort(bridge.Address)
		ip := net.ParseIP(host)
		port, _ := strconv.Atoi(portText)
		endpoint := net.JoinHostPort(ip.String(), strconv.Itoa(port))
		if endpoints[endpoint] {
			return errors.New("duplicate matrix bridge endpoint")
		}
		endpoints[endpoint] = true
	}
	return nil
}
