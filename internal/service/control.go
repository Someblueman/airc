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
)

type Status struct {
	Name       string `json:"name"`
	Installed  bool   `json:"installed"`
	Loaded     bool   `json:"loaded"`
	Running    bool   `json:"running"`
	PID        int    `json:"pid,omitempty"`
	Definition string `json:"definition"`
	StateDir   string `json:"state_dir"`
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
func target(c Config) string { return "gui/" + strconv.Itoa(os.Getuid()) + "/" + c.Label() }

var sourcePattern = regexp.MustCompile(`(?m)^\s*path = (.+)$`)
var pidPattern = regexp.MustCompile(`(?m)^\s*pid = (\d+)\s*$`)

func Inspect(c Config) (Status, error) {
	path, err := c.Manifest()
	if err != nil {
		return Status{}, err
	}
	result := Status{Name: c.Name, Installed: true, Definition: path, StateDir: c.StateDir}
	if err := ownedDefinition(c, path); err != nil {
		return result, err
	}
	switch runtime.GOOS {
	case "darwin":
		output, err := command("launchctl", "print", target(c))
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
		output, err := command("systemctl", "--user", "show", c.Label()+".service", "--property=LoadState,ActiveState,MainPID,FragmentPath")
		if err != nil {
			return result, err
		}
		values := map[string]string{}
		for _, line := range strings.Split(output, "\n") {
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

func Start(c Config) error {
	status, err := Inspect(c)
	if err != nil {
		return err
	}
	if status.Running {
		return nil
	}
	if runtime.GOOS == "darwin" {
		if status.Loaded {
			_, err = command("launchctl", "kickstart", target(c))
		} else {
			if _, err = command("launchctl", "enable", target(c)); err == nil {
				_, err = command("launchctl", "bootstrap", "gui/"+strconv.Itoa(os.Getuid()), status.Definition)
			}
		}
	} else {
		if _, err = command("systemctl", "--user", "daemon-reload"); err == nil {
			_, err = command("systemctl", "--user", "enable", "--now", c.Label()+".service")
		}
	}
	if err != nil {
		return err
	}
	return waitState(c, true)
}

func Stop(c Config) error {
	status, err := Inspect(c)
	if err != nil {
		return err
	}
	if runtime.GOOS == "darwin" {
		if _, err = command("launchctl", "disable", target(c)); err != nil {
			return err
		}
		if status.Loaded {
			_, err = command("launchctl", "bootout", target(c))
		}
	} else {
		_, err = command("systemctl", "--user", "disable", "--now", c.Label()+".service")
	}
	if err != nil {
		return err
	}
	return waitState(c, false)
}

func waitState(c Config, running bool) error {
	deadline := time.Now().Add(5 * time.Second)
	for {
		status, err := Inspect(c)
		if err != nil {
			return err
		}
		if status.Running == running {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("service did not reach requested state; inspect service logs and run airc doctor")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func Uninstall(c Config) error {
	if err := Stop(c); err != nil {
		return err
	}
	path, err := c.Manifest()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	if err := os.Remove(c.StateDir + "/service.json"); err != nil {
		return err
	}
	if runtime.GOOS == "linux" {
		_, err = command("systemctl", "--user", "daemon-reload")
	}
	return err
}

func ownedDefinition(c Config, path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
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
	expected, err := render(c, runtime.GOOS)
	if err != nil {
		return err
	}
	if !bytes.Equal(data, expected) {
		return errors.New("service definition differs from saved config; refusing to manage it")
	}
	return nil
}
