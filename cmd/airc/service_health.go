package main

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
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
	defer closeOnCancel(ctx, client)()
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

// gatewayReady waits until the bundled gateway job runs and every carrier
// accepts connections. The gateway reaches aircd per request, so this checks
// its listeners, not delivery.
func gatewayReady(c service.Config) error {
	if c.Gateway == nil {
		return nil
	}
	deadline := time.Now().Add(10 * time.Second)
	var last error
	for {
		status, err := service.Inspect(c)
		if err != nil {
			return err
		}
		if status.Gateway == nil || !status.Gateway.Running {
			last = errors.New("gateway is not running")
		} else {
			last = nil
			for _, carrier := range c.Gateway.Carriers {
				if last = dialCarrier(carrier); last != nil {
					break
				}
			}
			if last == nil {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("gateway readiness failed: %w; inspect %s and the gateway's service log", last, c.GatewayLogPath())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func dialCarrier(carrier service.Carrier) error {
	network, address := "tcp", carrier.Address
	if filepath.IsAbs(address) {
		network = "unix"
	} else if host, port, err := net.SplitHostPort(address); err == nil {
		if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
			host = "127.0.0.1"
			if ip.To4() == nil {
				host = "::1"
			}
			address = net.JoinHostPort(host, port)
		}
	}
	if network == "tcp" && service.CarrierNetwork(carrier.Name) == "udp" {
		return probeUDP(carrier.Name, address)
	}
	conn, err := net.DialTimeout(network, address, time.Second)
	if err != nil {
		return fmt.Errorf("carrier %s: %w", carrier.Name, err)
	}
	return conn.Close()
}

// probeUDP sends a UDP carrier one empty datagram. Nothing answers it, but a
// closed port answers with an ICMP error, which a connected UDP socket reads
// as "connection refused".
func probeUDP(name, address string) error {
	conn, err := net.DialTimeout("udp", address, time.Second)
	if err != nil {
		return fmt.Errorf("carrier %s: %w", name, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(200 * time.Millisecond))
	if _, err := conn.Write([]byte{0}); err != nil {
		return fmt.Errorf("carrier %s: %w", name, err)
	}
	if _, err := conn.Read(make([]byte, 1)); err != nil && !errors.Is(err, os.ErrDeadlineExceeded) {
		return fmt.Errorf("carrier %s: %w", name, err)
	}
	return nil
}
