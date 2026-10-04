package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "airc-gateway:", err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	command := ""
	if len(args) >= 2 && args[0] == "config" && args[1] == "validate" {
		command = "validate"
		args = args[2:]
	} else if len(args) >= 1 && args[0] == "capabilities" {
		command = "capabilities"
		args = args[1:]
	} else {
		return errors.New("usage: airc-gateway config validate --file PATH | capabilities --file PATH")
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
	c, err := config.Load(*path)
	if err != nil {
		return err
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
		instances = append(instances, instance{ID: l.ID, Protocol: l.Protocol, Status: "disabled_unimplemented", Operations: []core.Operation{}})
	}
	return json.NewEncoder(out).Encode(struct {
		Version    int              `json:"version"`
		Runtime    string           `json:"runtime"`
		Operations []core.Operation `json:"implemented_operations"`
		Listeners  []instance       `json:"listeners"`
		Limits     config.Limits    `json:"limits"`
	}{1, "not_implemented", []core.Operation{}, instances, c.Limits})
}
