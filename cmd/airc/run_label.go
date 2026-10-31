package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/Someblueman/airc/internal/protocol"
)

// A run label names the harness run a post belongs to, so a later search or
// history read can pick out one run's traffic. It travels as the +airc/run
// tag, which the daemon keeps in the message's metadata as run; AIRC_RUN is
// the default, as AIRC_SESSION identifies a session for cursor warnings.

// addRunFlag adds --run, defaulting to AIRC_RUN.
func addRunFlag(fs *flag.FlagSet) *string {
	return fs.String("run", os.Getenv("AIRC_RUN"), "run label kept on the post (1-64 letters, digits, '.', '_' or '-'; env AIRC_RUN)")
}

// runLabel checks --run: empty means none.
func runLabel(value string) (string, error) {
	if value != "" && !protocol.ValidRunID(value) {
		return "", fmt.Errorf("--run must be 1-64 letters, digits, '.', '_' or '-', not %q", value)
	}
	return value, nil
}

// warnRunDropped says when a post over another carrier went without its run
// label: the gateway predates the field and refused it, so the post was
// made again without it (the result then carries no run).
func warnRunDropped(run string, result *sendResult, stderr io.Writer) {
	if run != "" && result != nil && result.Carrier != "" && result.Run == "" {
		fmt.Fprintln(stderr, "airc: warning: this gateway predates run labels, so the post carries none; upgrade it when safe")
	}
}
