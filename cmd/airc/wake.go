package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Someblueman/airc/pkg/irc"
)

const wakeUsage = "usage: airc wake --nick N [--url URL [--secret-file FILE] [--quiet 1m] | --clear] [--json]"

// wakeResult is the daemon's 787 status, plus a secret the CLI generated
// for the receiver (shown once, never kept).
type wakeResult struct {
	Type         string     `json:"type"`
	Registered   bool       `json:"registered"`
	URL          string     `json:"url,omitempty"`
	QuietSeconds int        `json:"quiet_seconds,omitempty"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	Pending      int        `json:"pending,omitempty"`
	LastNotice   *time.Time `json:"last_notice,omitempty"`
	LastError    string     `json:"last_error,omitempty"`
	Secret       string     `json:"secret,omitempty"`
}

// runWake registers, shows or clears the account's wake URL: aircd POSTs a
// signed notice there when the account's inbox gets news while it holds no
// connection (docs/WAKEUP.md).
func runWake(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("airc wake", flag.ContinueOnError)
	fs.SetOutput(stderr)
	opt := addOptions(fs)
	target := fs.String("url", "", "register this URL (it must be on the daemon's --wake-allow list)")
	secretFile := fs.String("secret-file", "", "file holding the notice signing secret, 32-128 hex characters (default: generate one and print it)")
	quiet := fs.Duration("quiet", time.Minute, "at most one notice per this period (10s-1h)")
	clear := fs.Bool("clear", false, "remove the registration")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *clear && *target != "" || *target == "" && *secretFile != "" || *quiet < 10*time.Second || *quiet > time.Hour {
		return errors.New(wakeUsage)
	}
	if err := identity(opt); err != nil {
		return err
	}
	command := "WAKE"
	generated := ""
	switch {
	case *clear:
		command = "WAKE CLEAR"
	case *target != "":
		secret, err := wakeSecret(*secretFile)
		if err != nil {
			return err
		}
		if *secretFile == "" {
			generated = secret
		}
		if strings.ContainsAny(*target, " \r\n") {
			return errors.New("a wake URL cannot contain spaces or line breaks")
		}
		command = "WAKE SET " + *target + " " + secret + " " + strconv.Itoa(int(*quiet/time.Second))
	}
	ctx, cancel := commandContext()
	defer cancel()
	client, err := dialOneShot(ctx, *opt)
	if err != nil {
		return err
	}
	defer client.Close()
	defer closeOnCancel(ctx, client)()
	if !client.Supports("WAKE") {
		return errors.New("this aircd does not send wake notices: start it with --wake-allow (and an accounts file)")
	}
	if err := client.Raw(command); err != nil {
		return err
	}
	var result wakeResult
	err = awaitEvent(ctx, client, requestTimeout, "waiting for the wake status", "", func(event irc.Event) (bool, error) {
		if raw, ok := event.(*irc.RawEvent); ok && raw.Command == "787" {
			return true, json.Unmarshal([]byte(raw.Trailing), &result)
		}
		return false, serverError(event)
	})
	if err != nil {
		return err
	}
	result.Type, result.Secret = "wake", generated
	if opt.json {
		return json.NewEncoder(stdout).Encode(result)
	}
	switch {
	case !result.Registered:
		_, err = fmt.Fprintln(stdout, "no wake URL registered")
	default:
		_, err = fmt.Fprintf(stdout, "wake notices go to %s, at most one every %ds, until %s\n", result.URL, result.QuietSeconds, result.ExpiresAt.Format(time.RFC3339))
		if err == nil && result.LastError != "" {
			_, err = fmt.Fprintf(stdout, "last notice failed: %s (%d messages waiting)\n", result.LastError, result.Pending)
		}
		if err == nil && generated != "" {
			_, err = fmt.Fprintf(stdout, "signing secret (give it to the receiver; it is not shown again): %s\n", generated)
		}
	}
	return err
}

// wakeSecret reads the signing secret from path, or makes a new one.
func wakeSecret(path string) (string, error) {
	if path == "" {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return "", err
		}
		return hex.EncodeToString(key), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	secret := strings.ToLower(strings.TrimSpace(string(data)))
	if _, err := hex.DecodeString(secret); err != nil || len(secret) < 32 || len(secret) > 128 {
		return "", errors.New("the secret file must hold 32-128 hex characters")
	}
	return secret, nil
}
