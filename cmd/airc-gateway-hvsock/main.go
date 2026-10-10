// airc-gateway-hvsock carries a guest TLS exchange through a fixed Hyper-V host relay.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"github.com/Someblueman/airc/internal/gateway/framedbridge"
	"github.com/Someblueman/airc/internal/gateway/hvsock"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "airc-gateway-hvsock:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if runtime.GOOS != "windows" {
		return errors.New("Hyper-V helper requires Windows")
	}
	if len(args) == 0 || (args[0] != "serve" && args[0] != "request") {
		return errors.New("usage: airc-gateway-hvsock serve|request [options]")
	}
	var service, guest, address, ca, name, input, correlation, profile string
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&service, "service", "", "provisioned Hyper-V service GUID")
	if args[0] == "serve" {
		fs.StringVar(&guest, "guest-vm", "", "exact permitted guest VM GUID")
		fs.StringVar(&address, "address", "", "fixed numeric upstream TLS IP:port")
	} else {
		fs.StringVar(&ca, "ca", "", "absolute trusted gateway CA path")
		fs.StringVar(&name, "server-name", "", "gateway certificate name")
		fs.StringVar(&input, "input", "", "absolute original compact envelope path")
		fs.StringVar(&profile, "profile", "signed", "signed or encrypted envelope")
		fs.StringVar(&correlation, "correlation", "request-1", "request correlation")
	}
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 {
		return errors.New("invalid Hyper-V options")
	}
	if err := hvsock.ValidateService(service); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	if args[0] == "serve" {
		if err := hvsock.ValidateGuest(guest); err != nil {
			return err
		}
		host, port, err := net.SplitHostPort(address)
		ip := net.ParseIP(host)
		portNumber, portErr := strconv.ParseUint(port, 10, 16)
		if err != nil || ip == nil || ip.IsUnspecified() || ip.IsMulticast() || portErr != nil || portNumber == 0 {
			return errors.New("relay requires a fixed numeric gateway IP:port")
		}
		listener, err := hvsock.Listen(ctx, guest, service)
		if err != nil {
			return err
		}
		defer listener.Close()
		fmt.Fprintln(os.Stderr, "airc-gateway-hvsock: waiting for one connection from the configured guest")
		conn, err := listener.Accept()
		if err != nil {
			return err
		}
		defer conn.Close()
		return relay(ctx, conn, address)
	}
	if !filepath.IsAbs(ca) || name == "" {
		return errors.New("guest requires absolute CA path and gateway certificate name")
	}
	request, err := framedbridge.Request(profile, correlation, input)
	if err != nil {
		return err
	}
	conn, err := hvsock.Dial(ctx, service)
	if err != nil {
		return err
	}
	response, err := framedbridge.ForwardConn(ctx, conn, ca, name, request)
	if err != nil {
		return fmt.Errorf("guest exchange failed; recover the original request if its outcome is uncertain: %w", err)
	}
	_, err = fmt.Fprintln(os.Stdout, string(response))
	return err
}
