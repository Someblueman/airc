package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func initializeOSCORE(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("oscore init", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	file := flags.String("file", "", "owner gateway configuration")
	listenerID := flags.String("listener", "", "coap_oscore listener the context belongs to")
	keyFile := flags.String("context-file", "", "new owner-private OSCORE context file")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *file == "" || *listenerID == "" || *keyFile == "" || flags.NArg() != 0 {
		return errors.New("oscore init requires --file, --listener and --context-file; no positional arguments")
	}
	// The listener names its context file, which does not exist yet: check
	// the listener first, create the context, then check every reference.
	parsed, err := config.ParseFile(*file)
	if err != nil {
		return err
	}
	known := false
	for _, l := range parsed.Listeners {
		known = known || l.ID == *listenerID && l.Protocol == "coap_oscore" && l.OSCORE != nil && l.OSCORE.ContextFile == *keyFile
	}
	if !known {
		return errors.New("--listener must name a configured coap_oscore listener whose context_file is --context-file")
	}
	owner := auth.OSCOREOwner{Realm: parsed.Realm, Listener: *listenerID}
	material, err := auth.CreateOSCOREContext(*keyFile, owner)
	if err != nil {
		return err
	}
	digest, err := material.Digest()
	if err != nil {
		return err
	}
	// Until its journal state exists the new context is unused: remove it if
	// the configuration or journal cannot be opened.
	c, err := config.Load(*file)
	var s *store.Store
	if err == nil {
		s, err = store.Open(context.Background(), c)
	}
	if err != nil {
		return errors.Join(err, os.Remove(*keyFile))
	}
	defer s.Close()
	if err := s.ProvisionOSCORE(context.Background(), store.OSCOREKey{Digest: digest, Listener: owner.Listener}); err != nil {
		return fmt.Errorf("OSCORE state provisioning failed; context file is retained but must not be used without its journal state: %w", err)
	}
	if err := s.Close(); err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, "fresh OSCORE context and durable state created; material stored in owner-only file")
	return err
}
