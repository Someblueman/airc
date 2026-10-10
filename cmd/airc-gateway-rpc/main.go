// airc-gateway-rpc supervises one authenticated local Windows RPC exchange.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"time"

	"github.com/Someblueman/airc/internal/gateway/framedbridge"
	"github.com/Someblueman/airc/internal/gateway/winworker"
)

type options struct {
	worker, digest, serverSID, clientSID string
	profile, correlation, input          string
	upstream                             framedbridge.TLS
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "airc-gateway-rpc:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if runtime.GOOS != "windows" {
		return errors.New("RPC helper requires Windows")
	}
	if len(args) == 0 || (args[0] != "serve" && args[0] != "request") {
		return errors.New("usage: airc-gateway-rpc serve|request [options]")
	}
	var o options
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&o.worker, "worker", "", "absolute native worker executable")
	fs.StringVar(&o.digest, "sha256", "", "trusted native executable digest")
	fs.StringVar(&o.serverSID, "server-sid", "", "server account SID")
	fs.StringVar(&o.clientSID, "client-sid", "", "client account SID")
	if args[0] == "serve" {
		fs.StringVar(&o.upstream.Address, "address", "", "fixed numeric TLS gateway IP:port")
		fs.StringVar(&o.upstream.CA, "ca", "", "absolute trusted CA path")
		fs.StringVar(&o.upstream.ServerName, "server-name", "", "gateway certificate name")
	} else {
		fs.StringVar(&o.profile, "profile", "signed", "signed or encrypted envelope")
		fs.StringVar(&o.correlation, "correlation", "request-1", "request correlation")
		fs.StringVar(&o.input, "input", "", "absolute compact envelope path")
	}
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 || o.serverSID == "" || o.clientSID == "" {
		return errors.New("invalid RPC options or missing account SIDs")
	}
	var request []byte
	var err error
	if args[0] == "serve" {
		err = o.upstream.Validate()
	} else {
		request, err = framedbridge.Request(o.profile, o.correlation, o.input)
	}
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	result, err := exchange(ctx, args[0], o, request)
	if err != nil {
		return err
	}
	if args[0] == "request" {
		_, err = fmt.Fprintln(os.Stdout, string(result))
	}
	return err
}

func exchange(ctx context.Context, command string, o options, request []byte) ([]byte, error) {
	worker, err := winworker.Start(ctx, o.worker, o.digest, o.serverSID, o.clientSID)
	if err != nil {
		return nil, err
	}
	defer worker.Close()
	var response []byte
	if command == "serve" {
		ready, readyErr := framedbridge.ReadFrame(worker.Output, 1)
		if readyErr != nil || len(ready) != 1 || ready[0] != 1 {
			return nil, errors.New("RPC worker did not become ready")
		}
		fmt.Fprintln(os.Stderr, "airc-gateway-rpc: ready for one local RPC request")
		request, err = framedbridge.ReadFrame(worker.Output, framedbridge.MaxRequest)
		if err == nil {
			response, err = framedbridge.Forward(ctx, o.upstream, request)
		}
		if err == nil {
			err = framedbridge.WriteFrame(worker.Input, response, framedbridge.MaxResponse)
		}
	} else {
		err = framedbridge.WriteFrame(worker.Input, request, framedbridge.MaxRequest)
		if err == nil {
			response, err = framedbridge.ReadFrame(worker.Output, framedbridge.MaxResponse)
		}
		if err == nil {
			err = framedbridge.ValidateResponse(response, o.correlation)
		}
	}
	if err != nil {
		return nil, errors.New("RPC exchange failed; native outcome may be uncertain")
	}
	if err := worker.Finish(); err != nil {
		return nil, err
	}
	return response, nil
}
