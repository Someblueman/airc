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

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

const wakeUsage = "usage: airc wake --nick N [--url URL [--secret-file FILE] [--quiet 1m] [--mentions-only] [--quiet-hours HH:MM-HH:MM] | --clear] [--json]"

// wakeResult is the daemon's 787 status, plus a secret the CLI generated
// for the receiver (shown once, never kept).
type wakeResult struct {
	Type         string     `json:"type"`
	Registered   bool       `json:"registered"`
	URL          string     `json:"url,omitempty"`
	QuietSeconds int        `json:"quiet_seconds,omitempty"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	MentionsOnly bool       `json:"mentions_only,omitempty"`
	QuietHours   string     `json:"quiet_hours,omitempty"`
	QuietUntil   *time.Time `json:"quiet_until,omitempty"`
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
	mentionsOnly := fs.Bool("mentions-only", false, "notify for a direct message, a message that tags you or a handoff or decision offered to you, not for the steps of one you offered")
	quietHours := fs.String("quiet-hours", "", "hold notices during this daily window in the daemon's local time (HH:MM-HH:MM, overnight allowed); the news goes as one notice when it ends")
	clear := fs.Bool("clear", false, "remove the registration")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *clear && *target != "" || *target == "" && (*secretFile != "" || *mentionsOnly || *quietHours != "") || *quiet < 10*time.Second || *quiet > time.Hour {
		return errors.New(wakeUsage)
	}
	var options []string
	if *mentionsOnly {
		options = append(options, "mentions-only")
	}
	if *quietHours != "" {
		window, err := protocol.ParseQuietHours(*quietHours)
		if err != nil {
			return fmt.Errorf("--quiet-hours: %w", err)
		}
		options = append(options, "quiet-hours="+window.String())
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
		if len(options) > 0 {
			command += " " + strings.Join(options, ",")
		}
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
		if err == nil && result.MentionsOnly {
			_, err = fmt.Fprintln(stdout, "only for direct messages, messages that tag you and handoffs or decisions offered to you")
		}
		if err == nil && result.QuietHours != "" {
			_, err = fmt.Fprintf(stdout, "held during quiet hours %s (the daemon's local time)\n", result.QuietHours)
		}
		if err == nil && result.QuietUntil != nil {
			_, err = fmt.Fprintf(stdout, "%d messages wait for the quiet hours to end at %s\n", result.Pending, result.QuietUntil.Local().Format(time.RFC3339))
		}
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
