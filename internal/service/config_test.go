package service

import (
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefinitionsPreserveArguments(t *testing.T) {
	c := Config{Name: "test", Binary: `/tmp/bin & <"quoted">/aircd`, StateDir: `/tmp/state space%$`, Args: []string{"--unix", `/tmp/a\b"c%$socket`, "--history", "1000"}}
	data, err := render(c, "darwin")
	if err != nil {
		t.Fatal(err)
	}
	decoder := xml.NewDecoder(strings.NewReader(string(data)))
	var values []string
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if element, ok := token.(xml.StartElement); ok && element.Name.Local == "string" {
			var value string
			if err := decoder.DecodeElement(&value, &element); err != nil {
				t.Fatal(err)
			}
			values = append(values, value)
		}
	}
	for _, arg := range append([]string{c.Binary, c.StateDir}, c.Args...) {
		found := false
		for _, value := range values {
			if value == arg {
				found = true
			}
		}
		if !found {
			t.Fatalf("lost argument %q", arg)
		}
	}
	unit, err := render(c, "linux")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(unit), `"/tmp/a\\b\"c%%$$socket"`) || !strings.Contains(string(unit), `WorkingDirectory="/tmp/state space%%$"`) {
		t.Fatal(string(unit))
	}
	for _, platform := range []string{"darwin", "linux"} {
		invalid := c
		invalid.Args = []string{"injected\nExecStart=evil"}
		if _, err := render(invalid, platform); err == nil {
			t.Fatal("allowed control character injection")
		}
	}
}

func TestDefinitionsCarryDurabilityMode(t *testing.T) {
	c := Config{Name: "test", Binary: "/tmp/aircd", StateDir: "/tmp/state", Args: []string{"--listen", "127.0.0.1:6667", "--sync", "fsync"}}
	for platform, want := range map[string][]string{"darwin": {"<string>--sync</string>", "<string>fsync</string>"}, "linux": {` "--sync" "fsync"`}} {
		data, err := render(c, platform)
		if err != nil {
			t.Fatal(err)
		}
		for _, text := range want {
			if !strings.Contains(string(data), text) {
				t.Fatalf("%s definition lost the durability mode (%s):\n%s", platform, text, data)
			}
		}
	}
}

func TestInstallAndLoadDoNotOverwriteState(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	c := Config{Name: "test", Binary: "/tmp/aircd", StateDir: t.TempDir(), Args: []string{"--listen", "127.0.0.1:6667"}}
	history := filepath.Join(c.StateDir, "history.jsonl")
	os.WriteFile(history, []byte("preserved"), 0600)
	if err := Install(c); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(c.StateDir, c.Name)
	if err != nil || loaded.Binary != c.Binary {
		t.Fatal(loaded, err)
	}
	if err := Install(c); err == nil {
		t.Fatal("overwrote service definition")
	}
	if data, _ := os.ReadFile(history); string(data) != "preserved" {
		t.Fatal("mutated history")
	}
	if _, err := Load(c.StateDir, "other"); err == nil {
		t.Fatal("accepted another instance")
	}
	if err := os.Chmod(filepath.Join(c.StateDir, "service.json"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(c.StateDir, c.Name); err == nil {
		t.Fatal("accepted exposed service config")
	}
}
