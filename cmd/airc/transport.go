package main

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/Someblueman/airc/internal/admin"
	"github.com/Someblueman/airc/pkg/irc"
)

func addTransportOptions(fs *flag.FlagSet, opt *options) {
	value, _ := strconv.ParseBool(os.Getenv("AIRC_TLS"))
	fs.BoolVar(&opt.tls, "tls", value, "use verified TLS (env AIRC_TLS)")
	fs.StringVar(&opt.tlsCA, "tls-ca", os.Getenv("AIRC_TLS_CA"), "CA certificate PEM; implies TLS")
	fs.StringVar(&opt.tlsServerName, "tls-server-name", os.Getenv("AIRC_TLS_SERVER_NAME"), "certificate hostname; implies TLS")
	fs.StringVar(&opt.accessTokenFile, "access-token-file", os.Getenv("AIRC_ACCESS_TOKEN_FILE"), "owner-only connection credential file")
}

func usesTLS(opt options) bool { return opt.tls || opt.tlsCA != "" || opt.tlsServerName != "" }

// Keep legacy local keys unchanged; a TLS hostname override identifies a
// different peer even when two SSH tunnels use the same socket address.
func connectionKey(opt options, separator string) string {
	cfg := clientConfig(opt)
	network := cfg.Network
	if usesTLS(opt) {
		network = "tls"
	}
	key := network + separator + cfg.Addr
	if usesTLS(opt) && opt.tlsServerName != "" {
		key += "#" + strings.ToLower(opt.tlsServerName)
	}
	return key
}

func transportConfig(opt options) (irc.Config, error) {
	cfg := clientConfig(opt)
	if value := os.Getenv("AIRC_TLS"); value != "" {
		if _, err := strconv.ParseBool(value); err != nil {
			return cfg, errors.New("AIRC_TLS must be true or false")
		}
	}
	if usesTLS(opt) {
		if opt.unix != "" {
			return cfg, errors.New("TLS cannot be used with a Unix socket")
		}
		cfg.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS13, ServerName: opt.tlsServerName}
		if opt.tlsCA != "" {
			data, err := os.ReadFile(opt.tlsCA)
			if err != nil {
				return cfg, fmt.Errorf("read TLS CA: %w", err)
			}
			roots := x509.NewCertPool()
			if !roots.AppendCertsFromPEM(data) {
				return cfg, errors.New("TLS CA file contains no certificates")
			}
			cfg.TLSConfig.RootCAs = roots
		}
	}
	if opt.accessTokenFile != "" {
		token, err := admin.ReadToken(opt.accessTokenFile)
		if err != nil {
			return cfg, fmt.Errorf("read connection credential: %w", err)
		}
		cfg.AccessToken = token
	}
	return cfg, nil
}

func transportArgs(opt options) []string {
	var args []string
	if opt.tls {
		args = append(args, "--tls")
	}
	for _, pair := range [][2]string{{"--tls-ca", opt.tlsCA}, {"--tls-server-name", opt.tlsServerName}, {"--access-token-file", opt.accessTokenFile}} {
		if pair[1] != "" {
			args = append(args, pair[0], pair[1])
		}
	}
	return args
}
