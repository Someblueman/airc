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
	"strconv"
	"strings"
	"syscall"

	"github.com/Someblueman/airc/pkg/irc"
)

type commandFailure struct {
	Type      string `json:"type"`
	Code      string `json:"code"`
	Phase     string `json:"phase"`
	Retryable bool   `json:"retryable"`
	RequestID string `json:"request_id,omitempty"`
	Accepted  bool   `json:"accepted,omitempty"`
	MessageID string `json:"message_id,omitempty"`
	Message   string `json:"message"`
	err       error
	reported  bool
}

func (e *commandFailure) Error() string { return e.Message }
func (e *commandFailure) Unwrap() error { return e.err }

func failure(err error, phase string) *commandFailure {
	var existing *commandFailure
	if errors.As(err, &existing) {
		return existing
	}
	e := &commandFailure{Type: "error", Code: "invalid_request", Phase: phase, Message: explain(err), err: err}
	var network net.Error
	var rejection *irc.RejectedError
	switch {
	case errors.As(err, &rejection):
		e.Code = "server_rejected"
		switch rejection.Code {
		case "464", "498", "904", "905", "906", "907":
			e.Code = "auth_failed"
		case "488":
			e.Code = "delivery_unknown"
		case "487":
			e.Code = "request_conflict"
		case "403", "401", "430":
			e.Code = "invalid_target"
		case "404", "484", "485", "465", "474", "481", "482":
			e.Code = "permission_denied"
		case "437":
			e.Code, e.Retryable = "server_busy", true
		case "486":
			e.Code, e.Retryable = "rate_limited", true
		}
	case errors.Is(err, context.DeadlineExceeded):
		e.Code, e.Retryable = "timeout", true
	case errors.Is(err, context.Canceled):
		e.Code = "cancelled"
	case errors.Is(err, syscall.EWOULDBLOCK):
		e.Code, e.Retryable = "state_busy", true
	case errors.As(err, &network), errors.Is(err, io.EOF), errors.Is(err, net.ErrClosed):
		e.Code, e.Retryable = "server_unavailable", true
	}
	return e
}

// JSON failures go to stderr; stdout keeps its existing receipt/NDJSON contract.
func reportFailure(err *error, machine bool, stderr io.Writer) {
	if *err == nil || !machine || errors.Is(*err, flag.ErrHelp) {
		return
	}
	e := failure(*err, "request")
	if encodeErr := json.NewEncoder(stderr).Encode(e); encodeErr != nil {
		e.Message = fmt.Sprintf("%s (write error: %v)", e.Message, encodeErr)
	} else {
		e.reported = true
	}
	*err = e
}

func parseAgentFlags(fs *flag.FlagSet, args []string, opt *options, stderr io.Writer) error {
	var diagnostics bytes.Buffer
	fs.SetOutput(&diagnostics)
	requestedJSON := jsonFlagValue(fs, args)
	err := fs.Parse(args)
	if err != nil {
		opt.json = requestedJSON
	}
	if !opt.json || errors.Is(err, flag.ErrHelp) {
		if _, writeErr := io.Copy(stderr, &diagnostics); writeErr != nil {
			return writeErr
		}
	}
	return err
}

func acceptedFailure(err error, r *sendResult) *commandFailure {
	return &commandFailure{Type: "error", Code: "accepted_output_failed", Phase: "output", Accepted: true, MessageID: r.ID, RequestID: r.RequestID, Message: fmt.Sprintf("message accepted as %s; output/check failed: %v; do not resend", r.ID, err), err: err}
}

// Match flag parsing when looking ahead past an invalid flag: a message whose
// value is "--json" is still a message, and explicit --json=false stays false.
func jsonFlagValue(fs *flag.FlagSet, args []string) bool {
	machine := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" || !strings.HasPrefix(arg, "-") {
			break
		}
		name, value, hasValue := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		f := fs.Lookup(name)
		if f == nil {
			continue
		}
		boolean, ok := f.Value.(interface{ IsBoolFlag() bool })
		if ok && boolean.IsBoolFlag() {
			if name == "json" {
				if !hasValue {
					machine = true
				} else if v, err := strconv.ParseBool(value); err == nil {
					machine = v
				}
			}
		} else if !hasValue {
			i++
		}
	}
	return machine
}
