package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/pathcheck"
)

type Status struct {
	Name       string `json:"name"`
	Installed  bool   `json:"installed"`
	Loaded     bool   `json:"loaded"`
	Running    bool   `json:"running"`
	PID        int    `json:"pid,omitempty"`
	Definition string `json:"definition"`
	StateDir   string `json:"state_dir"`
	// Gateway is the bundled gateway job's status, when the service has one.
	Gateway *Status `json:"gateway,omitempty"`
	// Carriers is the advertised carrier list, in preference order.
	Carriers []string `json:"carriers,omitempty"`
}

func command(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, args[0], args[1:]...).CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("%s: %w: %s", args[0], err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}
func target(label string) string { return "gui/" + strconv.Itoa(os.Getuid()) + "/" + label }

var sourcePattern = regexp.MustCompile(`(?m)^\s*path = (.+)$`)
var pidPattern = regexp.MustCompile(`(?m)^\s*pid = (\d+)\s*$`)

// Inspect reports aircd's job and, when bundled, the gateway's.
func Inspect(c Config) (Status, error) {
	result, err := inspectJob(c, c.daemonJob())
	if err != nil || c.Gateway == nil {
		return result, err
	}
	gateway, err := inspectJob(c, c.gatewayJob())
	result.Gateway = &gateway
	if err != nil {
		return result, fmt.Errorf("gateway: %w", err)
	}
	discovery, err := c.Discovery()
	for _, entry := range discovery {
		result.Carriers = append(result.Carriers, entry.String())
	}
	return result, err
}

func inspectJob(c Config, j job) (Status, error) {
	path, err := c.manifest(runtime.GOOS, j.label)
	if err != nil {
		return Status{}, err
	}
	result := Status{Name: c.Name, Installed: true, Definition: path, StateDir: c.StateDir}
	if j.label != c.Label() {
		result.Name = "gateway"
	}
	if err := ownedDefinition(c, j, path); err != nil {
		return result, err
	}
	switch runtime.GOOS {
	case "darwin":
		output, err := command("launchctl", "print", target(j.label))
		if err != nil {
			if strings.Contains(output, "Could not find service") {
				return result, nil
			}
			return result, err
		}
		source := sourcePattern.FindStringSubmatch(output)
		if len(source) != 2 || strings.TrimSpace(source[1]) != path {
			return result, errors.New("loaded service uses another definition; refusing to manage it")
		}
		result.Loaded = true
		match := pidPattern.FindStringSubmatch(output)
		if len(match) == 2 {
			result.PID, _ = strconv.Atoi(match[1])
		}
		result.Running = result.PID > 0
	case "linux":
		output, err := command("systemctl", "--user", "show", j.label+".service", "--property=LoadState,ActiveState,MainPID,FragmentPath")
		if err != nil {
			return result, err
		}
		values := map[string]string{}
		for line := range strings.SplitSeq(output, "\n") {
			if key, value, ok := strings.Cut(line, "="); ok {
				values[key] = value
			}
		}
		if source := values["FragmentPath"]; source != "" && source != path {
			return result, errors.New("loaded service uses another definition; refusing to manage it")
		}
		result.Loaded = values["LoadState"] == "loaded"
		result.PID, _ = strconv.Atoi(values["MainPID"])
		result.Running = values["ActiveState"] == "active" && result.PID > 0
	}
	return result, nil
}

// Start starts aircd, then the gateway. The gateway reaches aircd lazily per
// request, so it also tolerates aircd restarting underneath it.
func Start(c Config) error {
	if runtime.GOOS == "linux" {
		if _, err := command("systemctl", "--user", "daemon-reload"); err != nil {
			return err
		}
	}
	for _, j := range c.jobs() {
		if err := startJob(c, j); err != nil {
			return err
		}
	}
	return nil
}

func startJob(c Config, j job) error {
	status, err := inspectJob(c, j)
	if err != nil {
		return err
	}
	if status.Running {
		return nil
	}
	if runtime.GOOS == "darwin" {
		if status.Loaded {
			_, err = command("launchctl", "kickstart", target(j.label))
		} else {
			if _, err = command("launchctl", "enable", target(j.label)); err == nil {
				_, err = command("launchctl", "bootstrap", "gui/"+strconv.Itoa(os.Getuid()), status.Definition)
			}
		}
	} else {
		_, err = command("systemctl", "--user", "enable", "--now", j.label+".service")
	}
	if err != nil {
		return err
	}
	return waitState(c, j, true)
}

// Stop stops the gateway first, then aircd, and disables automatic starts.
func Stop(c Config) error {
	jobs := c.jobs()
	for i := len(jobs) - 1; i >= 0; i-- {
		if err := stopJob(c, jobs[i]); err != nil {
			return err
		}
	}
	return nil
}

func stopJob(c Config, j job) error {
	status, err := inspectJob(c, j)
	if err != nil {
		return err
	}
	if runtime.GOOS == "darwin" {
		if _, err = command("launchctl", "disable", target(j.label)); err != nil {
			return err
		}
		if status.Loaded {
			_, err = command("launchctl", "bootout", target(j.label))
		}
	} else {
		_, err = command("systemctl", "--user", "disable", "--now", j.label+".service")
	}
	if err != nil {
		return err
	}
	return waitState(c, j, false)
}

func waitState(c Config, j job, running bool) error {
	deadline := time.Now().Add(time.Duration(j.stopTimeout/2) * time.Second)
	for {
		status, err := inspectJob(c, j)
		if err != nil {
			return err
		}
		if status.Running == running {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New(j.label + " did not reach requested state; inspect service logs and run airc doctor")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// Uninstall stops both jobs and removes their definitions, the generated
// gateway configuration and service.json. Chat data, credentials and the
// gateway journal stay.
func Uninstall(c Config) error {
	if err := Stop(c); err != nil {
		return err
	}
	jobs := c.jobs()
	for i := len(jobs) - 1; i >= 0; i-- {
		path, err := c.manifest(runtime.GOOS, jobs[i].label)
		if err != nil {
			return err
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	if c.Gateway != nil {
		if err := os.Remove(c.GatewayConfigPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := os.Remove(c.StateDir + "/service.json"); err != nil {
		return err
	}
	if runtime.GOOS == "linux" {
		_, err := command("systemctl", "--user", "daemon-reload")
		return err
	}
	return nil
}

func ownedDefinition(c Config, j job, path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !pathcheck.OwnerOnly(info) {
		return errors.New("service definition must be an owner-only regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 65537))
	if err != nil {
		return err
	}
	expected, err := renderJob(c, j, runtime.GOOS)
	if err != nil {
		return err
	}
	if !bytes.Equal(data, expected) {
		return errors.New("service definition differs from saved config; refusing to manage it")
	}
	return nil
}
