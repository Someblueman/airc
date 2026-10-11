package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/service"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "airc-gateway:", err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	if len(args) > 0 && args[0] == "scp-inbox" {
		return serveSCPInbox(args[1:])
	}
	if len(args) > 0 && args[0] == "import-bpv7" {
		return importBPv7(args[1:], out)
	}
	if len(args) > 0 && args[0] == "import-bundle" {
		return importOfflineBundle(args[1:], out)
	}
	if len(args) >= 2 && args[0] == "oscore" && args[1] == "init" {
		return initializeOSCORE(args[2:], out)
	}
	command := ""
	if len(args) >= 2 && args[0] == "config" && args[1] == "validate" {
		command = "validate"
		args = args[2:]
	} else if len(args) >= 2 && args[0] == "credential" && args[1] == "init" {
		command = "credential-init"
		args = args[2:]
	} else if len(args) >= 1 && args[0] == "serve" {
		command = "serve"
		args = args[1:]
	} else if len(args) >= 1 && processCommands[args[0]] != nil {
		command = args[0]
		args = args[1:]
	} else if len(args) >= 1 && args[0] == "capabilities" {
		command = "capabilities"
		args = args[1:]
	} else {
		return errors.New("usage: airc-gateway config validate --file PATH | capabilities --file PATH | credential init --file PATH | oscore init --file CONFIG --listener ID --context-file PATH | serve --file PATH | serve-stdio --file PATH | serve-socketpair --file PATH | serve-inherited --file PATH | serve-fifo --file PATH | serve-uart --file PATH | scp-inbox --file CONFIG | mailbox-once --file PATH | import-bundle --file CONFIG --input BUNDLE [--author-nick NICK [--author-token-file PATH]] | import-bpv7 --file CONFIG --input BUNDLE [--author-nick NICK [--author-token-file PATH]]")
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	path := flags.String("file", "", "owner-provisioned JSON configuration")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *path == "" || flags.NArg() != 0 {
		return errors.New("--file PATH is required; positional arguments are not accepted")
	}
	if command == "credential-init" {
		if err := auth.CreateCredential(*path); err != nil {
			return err
		}
		_, err := fmt.Fprintln(out, "credential created; secret stored in owner-only file")
		return err
	}
	c, err := config.Load(*path)
	if err != nil {
		return err
	}
	if serve, ok := processCommands[command]; ok {
		return serve(c)
	}
	if command == "serve" {
		return untilSignal(func(ctx context.Context) error { return service.Run(ctx, c, out) })
	}
	if command == "validate" {
		_, err := fmt.Fprintln(out, "configuration valid; no runtime or listeners started")
		return err
	}
	type instance struct {
		ID         string           `json:"id"`
		Protocol   string           `json:"protocol"`
		Status     string           `json:"status"`
		Operations []core.Operation `json:"operations"`
	}
	instances := make([]instance, 0, len(c.Listeners))
	for _, l := range c.Listeners {
		status, ops := listenerStatus(l)
		instances = append(instances, instance{ID: l.ID, Protocol: l.Protocol, Status: status, Operations: ops})
	}
	return json.NewEncoder(out).Encode(struct {
		Version    int              `json:"version"`
		Runtime    string           `json:"runtime"`
		Operations []core.Operation `json:"implemented_operations"`
		Listeners  []instance       `json:"listeners"`
		Limits     config.Limits    `json:"limits"`
	}{1, "partial_not_running", dispatch.ImplementedOperations(), instances, c.Limits})
}

// processCommands serve the Process carriers, one listener each, under the
// command their descriptor names; scp-inbox and the importers parse their own
// flags.
var processCommands = map[string]func(config.Config) error{
	"serve-stdio": func(c config.Config) error {
		stream, err := adapter.OpenPipeStream(os.Stdin, os.Stdout)
		if err != nil {
			return err
		}
		defer stream.Close()
		return untilSignal(func(ctx context.Context) error { return service.RunStdio(ctx, c, stream) })
	},
	"serve-socketpair": parentStream(adapter.OpenSocketPairStream, service.RunSocketPair),
	"serve-inherited":  parentStream(adapter.OpenInheritedUnixStream, service.RunInheritedUnixStream),
	"serve-fifo":       configured(service.RunFIFO),
	"serve-uart":       configured(service.RunUART),
	"mailbox-once":     configured(service.RunFileMailbox),
}

// parentStream serves the stream the parent passed as descriptor 3.
func parentStream(open func(*os.File) (net.Conn, error), run func(context.Context, config.Config, adapter.FramedStream) error) func(config.Config) error {
	return func(c config.Config) error {
		source := os.NewFile(3, "gateway-parent-stream")
		if source == nil {
			return errors.New("missing inherited descriptor 3")
		}
		defer source.Close()
		stream, err := open(source)
		if err != nil {
			return err
		}
		defer stream.Close()
		return untilSignal(func(ctx context.Context) error { return run(ctx, c, stream) })
	}
}

func configured(run func(context.Context, config.Config) error) func(config.Config) error {
	return func(c config.Config) error {
		return untilSignal(func(ctx context.Context) error { return run(ctx, c) })
	}
}

// untilSignal runs f until SIGINT or SIGTERM cancels its context.
func untilSignal(f func(context.Context) error) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return f(ctx)
}

// listenerStatus is a listener's capabilities report entry: its status and
// the operations it would serve.
func listenerStatus(l config.Listener) (string, []core.Operation) {
	t, ok := config.Lookup(l.Protocol)
	switch {
	case !ok:
		return "disabled_unimplemented", []core.Operation{}
	case l.Enabled:
		return "enabled_" + l.Protocol + "_partial", core.EffectiveOperations(dispatch.ImplementedOperations(), l.Operations, dispatch.ImplementedOperations(), dispatch.ImplementedOperations())
	case !t.Available():
		return "disabled_unavailable", []core.Operation{}
	}
	return "disabled", []core.Operation{}
}
