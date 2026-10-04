package main

import (
	"crypto/tls"
	"errors"
	"fmt"
	"os"

	"github.com/Someblueman/airc/internal/admin"
	"github.com/Someblueman/airc/internal/pathcheck"
)

func serverTransport(certPath, keyPath, accessPath, unix string) (*tls.Config, string, error) {
	var cfg *tls.Config
	if certPath != "" || keyPath != "" {
		if certPath == "" || keyPath == "" || unix != "" {
			return nil, "", errors.New("TLS requires both --tls-cert and --tls-key, and a TCP listener")
		}
		info, err := os.Stat(keyPath)
		if err != nil {
			return nil, "", err
		}
		if !pathcheck.OwnerOnly(info) {
			return nil, "", errors.New("TLS key must be owner-only (chmod 600)")
		}
		cert, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			return nil, "", fmt.Errorf("load TLS certificate: %w", err)
		}
		cfg = &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}}
	}
	var token string
	if accessPath != "" {
		var err error
		token, err = admin.ReadToken(accessPath)
		if err != nil {
			return nil, "", fmt.Errorf("read connection credential: %w", err)
		}
	}
	return cfg, token, nil
}
