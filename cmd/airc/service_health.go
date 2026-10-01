package main

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/Someblueman/airc/internal/service"
	"github.com/Someblueman/airc/pkg/irc"
)

// Confirm the IRC STATUS PID belongs to the managed process, not some unrelated
// daemon that already occupies the endpoint. Probe credentials stay in memory.
func serviceReady(c service.Config) error {
	opt := options{nick: defaultQueryNick()}
	for i := 0; i+1 < len(c.Args); i += 2 {
		switch c.Args[i] {
		case "--listen":
			opt.addr = c.Args[i+1]
		case "--unix":
			opt.unix = c.Args[i+1]
		case "--tls-cert":
			opt.tlsCA = c.Args[i+1]
		case "--access-token-file":
			opt.accessTokenFile = c.Args[i+1]
		}
	}
	if opt.unix == "" {
		addr, err := net.ResolveTCPAddr("tcp", opt.addr)
		if err != nil {
			return err
		}
		if addr.IP.IsUnspecified() {
			host := "127.0.0.1"
			if addr.IP.To4() == nil {
				host = "::1"
			}
			opt.addr = net.JoinHostPort(host, fmt.Sprint(addr.Port))
		}
	}
	if opt.tlsCA != "" {
		data, err := os.ReadFile(opt.tlsCA)
		if err != nil {
			return err
		}
		block, _ := pem.Decode(data)
		if block == nil {
			return errors.New("invalid service certificate")
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return err
		}
		if len(cert.DNSNames) > 0 {
			opt.tlsServerName = cert.DNSNames[0]
		} else if len(cert.IPAddresses) > 0 {
			opt.tlsServerName = cert.IPAddresses[0].String()
		}
	}
	cfg, err := transportConfig(opt)
	if err != nil {
		return err
	}
	cfg.Ephemeral = true
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var last error
	for {
		status, err := service.Inspect(c)
		if err != nil {
			return err
		}
		if status.Running {
			attempt, stop := context.WithTimeout(ctx, time.Second)
			last = probeService(attempt, cfg, status.PID)
			stop()
			if last == nil {
				return nil
			}
		} else {
			last = errors.New("managed daemon is not running")
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("service readiness failed: %w; inspect %s/service.log and startup.log", last, c.StateDir)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func probeService(ctx context.Context, cfg irc.Config, pid int) error {
	client, err := irc.DialContext(ctx, cfg)
	if err != nil {
		return err
	}
	defer client.Close()
	stop := context.AfterFunc(ctx, func() { _ = client.Close() })
	defer stop()
	status, err := fetchStatus(ctx, client)
	if err != nil {
		return err
	}
	if status.PID != pid {
		return fmt.Errorf("endpoint is served by PID %d, expected managed PID %d", status.PID, pid)
	}
	if status.PersistenceError != "" {
		return errors.New(status.PersistenceError)
	}
	return nil
}
