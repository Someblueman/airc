package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

type options struct {
	session                               *agentConnection
	nick, addr, unix                      string
	json                                  bool
	identityFile                          string
	tls                                   bool
	tlsCA, tlsServerName, accessTokenFile string
	// carriers is the explicit fallback carrier list (--carriers).
	carriers string
	hedge    bool
}

func main() {
	err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	if err == nil || errors.Is(err, flag.ErrHelp) {
		return // on ErrHelp the flag package already printed the command's usage
	}
	writeFailure(os.Stderr, err)
	os.Exit(1)
}

// writeFailure prints err unless the command already reported it: as a JSON
// error object, or as the flag package's error line and usage, after which only
// the hints are new.
func writeFailure(w io.Writer, err error) {
	if parse, ok := errors.AsType[*flagParseError](err); ok {
		for _, hint := range hints(parse.err) {
			fmt.Fprintln(w, hint)
		}
		return
	}
	if result, ok := errors.AsType[*commandFailure](err); ok && result.reported {
		return
	}
	fmt.Fprintln(w, explain(err))
}

// flagParseError marks a parse failure the flag package has already printed.
type flagParseError struct{ err error }

func (e *flagParseError) Error() string { return e.err.Error() }

func (e *flagParseError) Unwrap() error { return e.err }

// run dispatches one command. With --json, every subcommand reports failure as
// the same error object on stderr that send and check use.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	machine := wantsJSON(args)
	diagnostics := &flagErrorWriter{w: stderr, drop: machine}
	err := dispatch(args, stdin, stdout, diagnostics)
	if err != nil && diagnostics.seen && !machine && !errors.Is(err, flag.ErrHelp) {
		err = &flagParseError{err: err}
	}
	reportFailure(&err, machine, stderr)
	return err
}

// flagErrorWriter notices the flag package's error line. With drop set it also
// discards that line and the usage dump after it, which a JSON caller would
// otherwise have to skip to find the error object.
type flagErrorWriter struct {
	w    io.Writer
	drop bool
	seen bool
}

func (f *flagErrorWriter) Write(p []byte) (int, error) {
	for _, prefix := range []string{"flag provided but not defined", "flag needs an argument", "invalid value ", "invalid boolean "} {
		if strings.HasPrefix(string(p), prefix) {
			f.seen = true // parsing failed; the command returns right after
		}
	}
	if f.drop && f.seen {
		return len(p), nil
	}
	return f.w.Write(p)
}

func wantsJSON(args []string) bool {
	for _, arg := range args {
		switch arg {
		case "--":
			return false
		case "--json", "-json", "--json=true", "-json=true":
			return true
		}
	}
	return false
}

func dispatch(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h" || args[0] == "-help") {
		printUsage(stdout)
		return nil
	}
	leading, rest := leadingOptions(args)
	if len(rest) == 0 {
		return runInteractive(args, stdin, stdout, stderr)
	}
	if rest[0] == "help" {
		printUsage(stdout)
		return nil
	}
	c, ok := commandsByName[rest[0]]
	if !ok {
		if len(leading) > 0 {
			return runInteractive(args, stdin, stdout, stderr) // rejects the stray word
		}
		return fmt.Errorf("unknown command %q (see airc help)", rest[0])
	}
	return runCommand(c, withLeadingOptions(rest[1:], leading), stdin, stdout, stderr)
}

// leadingOptions splits the options every command accepts (addOptions: the
// connection flags, --nick, --identity and --json) off the front of args, so
// airc --addr HOST:PORT channels --json runs airc channels --addr HOST:PORT
// --json; rest starts at the command word. The command parses the leading
// flags again as its own. A leading flag only a command knows (airc --channel
// x send) leaves both empty, and the interactive parser reports it.
func leadingOptions(args []string) (leading, rest []string) {
	fs := flag.NewFlagSet("airc", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	addOptions(fs)
	if err := fs.Parse(args); err != nil {
		return nil, nil
	}
	rest = fs.Args()
	leading = args[:len(args)-len(rest)]
	if slices.Contains(leading, "--") {
		return nil, nil // a terminator ends the flags; what follows is not a command
	}
	return leading, rest
}

// withLeadingOptions moves the flags that preceded the command after its own
// arguments, where every command accepts them: a target or an ID comes first
// (airc history '#room' --json), so the flags go after the last argument, or
// before a -- terminator when there is one.
func withLeadingOptions(args, leading []string) []string {
	if len(leading) == 0 {
		return args
	}
	end := slices.Index(args, "--")
	if end < 0 {
		end = len(args)
	}
	return slices.Concat(args[:end], leading, args[end:])
}

func addOptions(fs *flag.FlagSet) *options {
	opt := &options{}
	fs.StringVar(&opt.nick, "nick", "", "agent nickname (env AIRC_NICK for send, check and interactive)")
	fs.StringVar(&opt.addr, "addr", envOr("AIRC_ADDR", "127.0.0.1:6667"), "TCP server address (env AIRC_ADDR)")
	fs.StringVar(&opt.unix, "unix", os.Getenv("AIRC_UNIX"), "Unix socket path (env AIRC_UNIX)")
	fs.BoolVar(&opt.json, "json", false, "emit machine-readable JSON")
	fs.StringVar(&opt.identityFile, "identity", os.Getenv("AIRC_IDENTITY_FILE"), "registered user identity file; defaults to this server/nickname's saved identity")
	addTransportOptions(fs, opt)
	return opt
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func defaultQueryNick() string {
	if suffix, err := randomHex(8); err == nil {
		return "observer-" + suffix
	}
	return fmt.Sprintf("observer-%d", time.Now().UnixNano())
}

// randomHex returns n random bytes, hex encoded.
func randomHex(n int) (string, error) {
	raw := make([]byte, n)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func clientConfig(opt options) irc.Config {
	network, address := "tcp", opt.addr
	if opt.unix != "" {
		network, address = "unix", opt.unix
	}
	return irc.Config{Nick: opt.nick, Addr: address, Network: network, KeepAlive: sessionKeepAlive, ProbeTimeout: sessionProbeTimeout}
}

// A connection that has read nothing for sessionKeepAlive is probed, and
// dropped if the server stays silent for sessionProbeTimeout more: a
// long-running command notices a dead link in 8s instead of two minutes.
var sessionKeepAlive, sessionProbeTimeout = 5 * time.Second, 3 * time.Second

// indentContinuation indents the second and later lines of a multi-line message
// so each message stays visually distinct in line-oriented output.
func indentContinuation(message string) string {
	return strings.ReplaceAll(message, "\n", "\n  ")
}

func sameTarget(a, b string) bool {
	if protocol.IsChannel(a) {
		return a == b
	}
	return strings.EqualFold(a, b)
}
