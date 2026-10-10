// airc-gateway-pipe carries one bounded gateway exchange over a local Windows pipe.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const maxRequest = 16384
const maxResponse = 65536

type options struct {
	pipe, serverSID, clientSID, address, ca, serverName string
	profile, correlation, input                         string
}

type frameRequest struct {
	Version       int    `json:"version"`
	CorrelationID string `json:"correlation_id"`
	Profile       string `json:"profile"`
	Envelope      string `json:"envelope"`
	Stream        bool   `json:"stream"`
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
		fs.StringVar(&o.address, "address", "", "fixed numeric TLS gateway IP:port")
		fs.StringVar(&o.ca, "ca", "", "trusted gateway CA file")
		fs.StringVar(&o.serverName, "server-name", "", "gateway certificate name")
	} else {
		fs.StringVar(&o.profile, "profile", "signed", "signed or encrypted envelope")
		fs.StringVar(&o.correlation, "correlation", "request-1", "request correlation handle")
		fs.StringVar(&o.input, "input", "", "absolute compact envelope file")
	}
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 || o.pipe == "" || o.serverSID == "" || o.clientSID == "" {
		return errors.New("invalid pipe options or missing account SIDs")
	}
	if command == "serve" {
		host, port, err := net.SplitHostPort(o.address)
		ip := net.ParseIP(host)
		portNumber, portErr := strconv.ParseUint(port, 10, 16)
		if err != nil || ip == nil || ip.IsUnspecified() || ip.IsMulticast() || portErr != nil || portNumber == 0 || o.serverName == "" || !filepath.IsAbs(o.ca) {
			return errors.New("serve requires fixed numeric TLS address, certificate name and absolute CA path")
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
	if err := validateResponse(output.Bytes(), o.correlation); err != nil {
		return err
	}
	_, err = fmt.Fprintln(os.Stdout, output.String())
	return err
}

func requestBytes(o options) ([]byte, error) {
	f, err := os.Open(o.input)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > maxRequest {
		return nil, errors.New("envelope must be a bounded regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxRequest+1))
	if err != nil || len(data) > maxRequest {
		return nil, errors.New("envelope read failed or exceeds bound")
	}
	data = bytes.TrimSuffix(data, []byte("\n"))
	data = bytes.TrimSuffix(data, []byte("\r"))
	if len(data) == 0 || bytes.IndexAny(data, " \r\n\t") >= 0 {
		return nil, errors.New("expected a compact signed or encrypted envelope")
	}
	request, err := json.Marshal(frameRequest{Version: 1, CorrelationID: o.correlation, Profile: o.profile, Envelope: string(data)})
	if err != nil || len(request) > maxRequest {
		return nil, errors.New("complete request exceeds frame bound")
	}
	return request, nil
}

func validateResponse(data []byte, correlation string) error {
	var response struct {
		Version       int    `json:"version"`
		CorrelationID string `json:"correlation_id"`
		Event         string `json:"event"`
	}
	if len(data) == 0 || len(data) > maxResponse || json.Unmarshal(data, &response) != nil || response.Version != 1 || response.CorrelationID != correlation || (response.Event != "result" && response.Event != "error") {
		return errors.New("invalid or uncorrelated gateway result")
	}
	return nil
}
