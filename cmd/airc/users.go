package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Someblueman/airc/internal/admin"
	"github.com/Someblueman/airc/internal/pathcheck"
	"github.com/Someblueman/airc/pkg/irc"
)

type savedIdentity struct {
	Nick   string `json:"nick"`
	Server string `json:"server"`
	Token  string `json:"token"`
}

func serverKey(opt options) string { return connectionKey(opt, ":") }

func identityPath(opt options) (string, error) {
	if opt.identityFile != "" {
		return opt.identityFile, nil
	}
	dir, err := stateDir()
	key := sha256.Sum256([]byte(serverKey(opt) + "\n" + strings.ToLower(opt.nick)))
	return filepath.Join(dir, "identities", hex.EncodeToString(key[:])+".json"), err
}

func loadIdentity(path string, opt options) (savedIdentity, error) {
	var value savedIdentity
	f, err := os.Open(path)
	if err != nil {
		return value, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return value, err
	}
	if !pathcheck.OwnerOnly(info) {
		return value, errors.New("identity file must be owner-only (chmod 600)")
	}
	data, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil {
		return value, err
	}
	if len(data) > 4096 || json.Unmarshal(data, &value) != nil || !admin.ValidToken(value.Token) {
		return value, errors.New("invalid identity file")
	}
	if value.Server != serverKey(opt) || opt.nick != "" && !strings.EqualFold(opt.nick, value.Nick) {
		return value, errors.New("identity belongs to another server or nickname")
	}
	return value, nil
}

func dialConfig(opt options) (irc.Config, error) {
	cfg, err := transportConfig(opt)
	if err != nil {
		return cfg, err
	}
	path, err := identityPath(opt)
	if err != nil {
		return cfg, err
	}
	value, err := loadIdentity(path, opt)
	if errors.Is(err, os.ErrNotExist) && opt.identityFile == "" {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	cfg.Nick, cfg.IdentityToken = value.Nick, value.Token
	return cfg, nil
}

func runUser(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] != "create" && args[0] != "path" && args[0] != "login" {
		return errors.New("use airc user create|login|path --nick NICK [--model NAME] [--about TEXT]")
	}
	action := args[0]
	fs := flag.NewFlagSet("airc user "+action, flag.ContinueOnError)
	fs.SetOutput(stderr)
	opt := addOptions(fs)
	fields := map[string]*string{}
	for _, field := range []string{"model", "workspace", "tools", "about"} {
		fields[field] = fs.String(field, "", "fixed profile "+field)
	}
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected user arguments")
	}
	if err := identity(opt); err != nil {
		return err
	}
	path, err := identityPath(*opt)
	if err != nil {
		return err
	}
	if action == "path" {
		if opt.json {
			return json.NewEncoder(stdout).Encode(map[string]string{"nick": opt.nick, "identity_file": path})
		}
		_, err := fmt.Fprintln(stdout, path)
		return err
	}
	cfg, err := transportConfig(*opt)
	if err != nil {
		return err
	}
	value, err := loadIdentity(path, *opt)
	if errors.Is(err, os.ErrNotExist) && action == "create" {
		var token [32]byte
		if _, err = rand.Read(token[:]); err != nil {
			return err
		}
		value = savedIdentity{Nick: opt.nick, Server: serverKey(*opt), Token: hex.EncodeToString(token[:])}
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		writeErr := json.NewEncoder(f).Encode(value)
		if writeErr == nil {
			writeErr = f.Sync()
		}
		if err := errors.Join(writeErr, f.Close()); err != nil {
			_ = os.Remove(path)
			return err
		}
	} else if err != nil {
		return err
	}
	ctx, cancel := commandContext()
	defer cancel()
	cfg.IdentityToken, cfg.CreateAccount, cfg.Ephemeral = value.Token, action == "create", true
	client, err := irc.DialContext(ctx, cfg)
	if err != nil {
		if action == "login" {
			return fmt.Errorf("account login failed: %w", err)
		}
		return fmt.Errorf("user registration unconfirmed; identity saved at %s; retry this command with the same file: %w", path, err)
	}
	defer client.Close()
	defer closeOnCancel(ctx, client)()
	patch := map[string]string{}
	fs.Visit(func(f *flag.Flag) {
		if value, ok := fields[f.Name]; ok {
			patch[f.Name] = *value
		}
	})
	if len(patch) > 0 {
		if err := client.UpdateProfile(patch); err != nil {
			return err
		}
		for {
			select {
			case e, ok := <-client.Events():
				if !ok {
					return disconnectError("connection closed saving fixed profile")
				}
				if err := serverError(e); err != nil {
					return err
				}
				if _, ok := e.(*irc.EndOfDirectoryEvent); ok {
					goto saved
				}
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
saved:
	if opt.json {
		return json.NewEncoder(stdout).Encode(struct {
			Nick         string `json:"nick"`
			IdentityFile string `json:"identity_file"`
			Ready        bool   `json:"ready"`
		}{opt.nick, path, true})
	}
	reconnect := "--nick " + opt.nick
	if opt.identityFile != "" {
		reconnect = fmt.Sprintf("--identity %q", path)
	}
	_, err = fmt.Fprintf(stdout, "User %s ready. Identity file: %s\nUse %s for authenticated reconnects on this server.\n", opt.nick, path, reconnect)
	return err
}
