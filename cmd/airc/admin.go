package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Someblueman/airc/internal/admin"
	"github.com/Someblueman/airc/pkg/irc"
)

func defaultAdminTokenFile() (string, error) {
	if path := os.Getenv("AIRC_ADMIN_TOKEN_FILE"); path != "" {
		return path, nil
	}
	dir, err := stateDir()
	return filepath.Join(dir, "admin.token"), err
}

func runAdmin(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("use airc admin init|list|mute|unmute|kick|ban|unban|account-list|account-delete [NICK] [options]")
	}
	action, args := args[0], args[1:]
	switch action {
	case "init", "list", "mute", "unmute", "kick", "ban", "unban", "account-list", "account-delete":
	default:
		return fmt.Errorf("unknown admin action %q", action)
	}
	// Like history/thread, accept the principal argument before flags.
	nick := ""
	noNick := action == "init" || action == "list" || action == "account-list"
	if !noNick && len(args) > 0 && args[0] != "" && args[0][0] != '-' {
		nick, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet("airc admin "+action, flag.ContinueOnError)
	fs.SetOutput(stderr)
	opt := addOptions(fs)
	tokenFile := fs.String("token-file", "", "admin credential file (AIRC_ADMIN_TOKEN_FILE or state directory/admin.token)")
	channel := fs.String("channel", "", "limit mute/ban to this channel; default is server-wide")
	duration := fs.Duration("for", 0, "mute/ban duration, 1s-720h; default is indefinite")
	reason := fs.String("reason", "", "reason for mute, ban or kick, up to 400 bytes")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		if nick != "" || fs.NArg() != 1 || noNick {
			return errors.New("unexpected admin arguments")
		}
		nick = fs.Arg(0)
	}
	if *tokenFile == "" {
		var err error
		*tokenFile, err = defaultAdminTokenFile()
		if err != nil {
			return err
		}
	}
	if noNick || action == "account-delete" {
		if *channel != "" || *duration != 0 || *reason != "" {
			return fmt.Errorf("%s does not accept channel, duration or reason", action)
		}
	}
	if !noNick && nick == "" {
		return errors.New("admin action requires a nickname")
	}
	if *duration < 0 || *duration > 30*24*time.Hour || *duration > 0 && *duration < time.Second {
		return errors.New("--for must be 1s-720h, or 0 for indefinite")
	}
	if action != "mute" && action != "ban" && *duration != 0 {
		return errors.New("--for applies to mute and ban")
	}
	if action == "kick" && *channel != "" {
		return errors.New("kick disconnects all current sessions; omit --channel")
	}
	if action == "init" {
		if err := admin.CreateToken(*tokenFile); err != nil {
			return fmt.Errorf("create admin token: %w", err)
		}
		_, err := fmt.Fprintf(stdout, "Created admin credential: %s\nEnable with aircd --admin-token-file %q\n", *tokenFile, *tokenFile)
		return err
	}
	token, err := admin.ReadToken(*tokenFile)
	if err != nil {
		return fmt.Errorf("read admin token: %w", err)
	}
	if err := queryIdentity(opt); err != nil {
		return err
	}
	scope := "*"
	if *channel != "" {
		scope = channelName(*channel)
	}
	request := irc.AdminRequest{Action: action, Nick: nick, Scope: scope, Seconds: int64((*duration + time.Second - 1) / time.Second), Reason: *reason}
	return chatRequest(*opt, "ADMIN", func(ctx context.Context, client *irc.Client) error {
		if err := client.AuthenticateAdmin(token); err != nil {
			return err
		}
		if err := client.Moderate(request); err != nil {
			return err
		}
		count := 0
		for {
			select {
			case event, ok := <-client.Events():
				if !ok {
					return disconnectError("server disconnected before confirming administration")
				}
				if err := serverError(event); err != nil {
					return err
				}
				switch value := event.(type) {
				case *irc.AdminEvent:
					count++
					if opt.json {
						err = json.NewEncoder(stdout).Encode(value)
					} else {
						_, err = fmt.Fprintln(stdout, adminText(value.AdminResult))
					}
					if err != nil {
						return err
					}
				case *irc.EndOfAdminEvent:
					if count == 0 && action != "list" && action != "account-list" {
						return errors.New("server omitted admin result")
					}
					if count == 0 && !opt.json {
						text := "No active mutes or bans"
						if action == "account-list" {
							text = "No registered accounts"
						}
						_, err = fmt.Fprintln(stdout, text)
					}
					return err
				}
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	})
}

func adminText(result irc.AdminResult) string {
	if rule := result.Rule; rule != nil {
		text := fmt.Sprintf("%s %s in %s; set by %s", rule.Kind, rule.Nick, rule.Scope, rule.SetBy)
		if !rule.ExpiresAt.IsZero() {
			text += "; expires " + rule.ExpiresAt.Format(time.RFC3339)
		}
		if rule.Reason != "" {
			text += "; " + rule.Reason
		}
		if result.Action == "ban" {
			text += fmt.Sprintf("; disconnected %d sessions", result.Kicked)
		}
		return text
	}
	switch result.Action {
	case "account-list":
		return fmt.Sprintf("%s %s", result.Nick, result.AccountID)
	case "account-delete":
		if !result.Changed {
			return fmt.Sprintf("account-delete %s: no such account", result.Nick)
		}
		return fmt.Sprintf("account-delete %s: removed account %s; the nickname can be registered again (open sessions stay connected)", result.Nick, result.AccountID)
	}
	if result.Action == "kick" {
		return fmt.Sprintf("kick %s: disconnected %d sessions (reconnection allowed)", result.Nick, result.Kicked)
	}
	return fmt.Sprintf("%s %s in %s: changed=%t", result.Action, result.Nick, result.Scope, result.Changed)
}
