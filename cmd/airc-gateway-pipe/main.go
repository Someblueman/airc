// airc-gateway-pipe carries one bounded gateway exchange over a local Windows pipe.
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
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/gateway/framedbridge"
)

const maxResponse = framedbridge.MaxResponse

type options struct {
	pipe, serverSID, clientSID  string
	profile, correlation, input string
	upstream                    framedbridge.TLS
}

type boundedOutput struct{ bytes.Buffer }

func (b *boundedOutput) Write(p []byte) (int, error) {
	if len(p) > maxResponse-b.Len() {
		return 0, errors.New("pipe child output exceeds bound")
	}
	return b.Buffer.Write(p)
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "airc-gateway-pipe:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if runtime.GOOS != "windows" {
		return errors.New("named-pipe helper requires Windows")
	}
	if len(args) == 0 {
		return errors.New("usage: airc-gateway-pipe serve|request [options]")
	}
	command := strings.TrimPrefix(args[0], "_")
	if command != "serve" && command != "request" {
		return errors.New("expected serve or request")
	}
	var o options
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&o.pipe, "pipe", "", "exact local pipe name")
	fs.StringVar(&o.serverSID, "server-sid", "", "provisioned server account SID")
	fs.StringVar(&o.clientSID, "client-sid", "", "provisioned client account SID")
	if command == "serve" {
		fs.StringVar(&o.upstream.Address, "address", "", "fixed numeric TLS gateway IP:port")
		fs.StringVar(&o.upstream.CA, "ca", "", "trusted gateway CA file")
		fs.StringVar(&o.upstream.ServerName, "server-name", "", "gateway certificate name")
	} else {
		fs.StringVar(&o.profile, "profile", "signed", "signed or encrypted envelope")
		fs.StringVar(&o.correlation, "correlation", "request-1", "request correlation handle")
		fs.StringVar(&o.input, "input", "", "absolute compact envelope file")
	}
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 || o.pipe == "" || o.serverSID == "" || o.clientSID == "" {
		return errors.New("invalid pipe options or missing account SIDs")
	}
	if command == "serve" {
		if err := o.upstream.Validate(); err != nil {
			return err
		}
	} else if (o.profile != "signed" && o.profile != "encrypted") || !regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`).MatchString(o.correlation) || !filepath.IsAbs(o.input) {
		return errors.New("request requires an absolute envelope path, profile and bounded correlation")
	}
	if strings.HasPrefix(args[0], "_") {
		childCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		return runChild(childCtx, command, o)
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	childCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	childArgs := append([]string{"_" + command}, args[1:]...)
	cmd := exec.CommandContext(childCtx, executable, childArgs...)
	cmd.WaitDelay = 2 * time.Second
	var output boundedOutput
	cmd.Stdout = &output
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return errors.New("pipe operation failed, expired or was interrupted; native outcome may be uncertain")
	}
	if command == "serve" {
		if output.Len() != 0 {
			return errors.New("pipe server returned unexpected output")
		}
		return nil
	}
	if err := framedbridge.ValidateResponse(output.Bytes(), o.correlation); err != nil {
		return err
	}
	_, err = fmt.Fprintln(os.Stdout, output.String())
	return err
}
