package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func initializeOSCORE(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("oscore init", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	file := flags.String("file", "", "owner gateway configuration")
	principal := flags.String("principal", "", "existing authorized principal")
	keyFile := flags.String("context-file", "", "new owner-private OSCORE context file")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *file == "" || *principal == "" || *keyFile == "" || flags.NArg() != 0 {
		return errors.New("oscore init requires --file, --principal and --context-file; no positional arguments")
	}
	c, err := config.Load(*file)
	if err != nil {
		return err
	}
	owner := auth.OSCOREOwner{Realm: c.Realm, Principal: *principal}
	for _, p := range c.Principals {
		if p.ID == *principal && !p.Revoked {
			owner.Binding = p.Binding
		}
	}
	if owner.Binding == "" {
		return errors.New("OSCORE principal is unknown or revoked")
	}
	s, err := store.Open(context.Background(), c)
	if err != nil {
		return err
	}
	defer s.Close()
	material, err := auth.CreateOSCOREContext(*keyFile, owner)
	if err != nil {
		return err
	}
	digest, err := material.Digest()
	if err != nil {
		return err
	}
	if err := s.ProvisionOSCORE(context.Background(), store.OSCOREKey{Digest: digest, Principal: owner.Principal, Binding: owner.Binding}); err != nil {
		return fmt.Errorf("OSCORE state provisioning failed; context file is retained but must not be used without its journal state: %w", err)
	}
	if err := s.Close(); err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, "fresh OSCORE context and durable state created; material stored in owner-only file")
	return err
}
