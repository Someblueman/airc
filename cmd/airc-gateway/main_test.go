package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalCommands(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.json")
	data := `{"version":1,"realm":"fixture","state_dir":"` + filepath.Join(root, "state") + `","listeners":[{"id":"future-http","protocol":"http","enabled":false}]}`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run([]string{"config", "validate", "--file", path}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "no runtime or listeners started") {
		t.Fatal(out.String())
	}
	out.Reset()
	if err := run([]string{"capabilities", "--file", path}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"implemented_operations":["send","reply","receipt.get","history.read","search"]`) || !strings.Contains(out.String(), `"status":"disabled"`) || strings.Contains(out.String(), root) {
		t.Fatal(out.String())
	}
	for _, args := range [][]string{{"serve"}, {"config", "validate"}, {"capabilities", "--file", path, "stray"}, {"capabilities", "--unknown", "x"}} {
		if err := run(args, &out); err == nil {
			t.Fatalf("accepted invalid command: %v", args)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "state")); !os.IsNotExist(err) {
		t.Fatal("local CLI mutated state")
	}
}
