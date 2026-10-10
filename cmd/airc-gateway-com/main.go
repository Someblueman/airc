// airc-gateway-com supervises one authenticated local Windows COM exchange.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/gateway/framedbridge"
)

type options struct {
	worker, digest, serverSID, clientSID string
	pipe                                 string
	profile, correlation, input          string
	upstream                             framedbridge.TLS
}

type boundedOutput struct{ bytes.Buffer }

func (b *boundedOutput) Write(p []byte) (int, error) {
	if len(p) > framedbridge.MaxResponse+1-b.Len() {
		return 0, errors.New("COM child output exceeds bound")
	}
	return b.Buffer.Write(p)
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "airc-gateway-com:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if runtime.GOOS != "windows" {
		return errors.New("COM helper requires Windows")
	}
	if len(args) == 0 {
		return errors.New("usage: airc-gateway-com serve|request [options]")
	}
	command := strings.TrimPrefix(args[0], "_")
	if command != "serve" && command != "request" {
		return errors.New("expected serve or request")
	}
	var o options
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&o.pipe, "pipe", "", "exact local bootstrap pipe name")
	fs.StringVar(&o.worker, "worker", "", "absolute native worker executable")
	fs.StringVar(&o.digest, "sha256", "", "trusted native executable digest")
	fs.StringVar(&o.serverSID, "server-sid", "", "server account SID")
	fs.StringVar(&o.clientSID, "client-sid", "", "client account SID")
	if command == "serve" {
		fs.StringVar(&o.upstream.Address, "address", "", "fixed numeric TLS gateway IP:port")
		fs.StringVar(&o.upstream.CA, "ca", "", "absolute trusted CA path")
		fs.StringVar(&o.upstream.ServerName, "server-name", "", "gateway certificate name")
	} else {
		fs.StringVar(&o.profile, "profile", "signed", "signed or encrypted envelope")
		fs.StringVar(&o.correlation, "correlation", "request-1", "request correlation")
		fs.StringVar(&o.input, "input", "", "absolute compact envelope path")
	}
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 || o.serverSID == "" || o.clientSID == "" || o.pipe == "" {
		return errors.New("invalid COM options or missing account SIDs")
	}
	if !strings.HasPrefix(args[0], "_") {
		return supervise(ctx, command, args[1:], o.correlation)
	}
	var request []byte
	var err error
	if command == "serve" {
		err = o.upstream.Validate()
	} else {
		request, err = framedbridge.Request(o.profile, o.correlation, o.input)
	}
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	result, err := exchange(ctx, command, o, request)
	if err != nil {
		return err
	}
	if command == "request" {
		_, err = fmt.Fprintln(os.Stdout, string(result))
	}
	return err
}

func supervise(ctx context.Context, command string, args []string, correlation string) error {
	path, err := os.Executable()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, append([]string{"_" + command}, args...)...)
	cmd.WaitDelay = 2 * time.Second
	cmd.Stderr = os.Stderr
	var output boundedOutput
	cmd.Stdout = &output
	if err := cmd.Run(); err != nil {
		return errors.New("COM helper failed, expired or was interrupted; native outcome may be uncertain")
	}
	if command == "serve" {
		if output.Len() != 0 {
			return errors.New("unexpected COM server output")
		}
		return nil
	}
	response := bytes.TrimSuffix(output.Bytes(), []byte("\n"))
	if err := framedbridge.ValidateResponse(response, correlation); err != nil {
		return err
	}
	_, err = fmt.Fprintln(os.Stdout, string(response))
	return err
}
