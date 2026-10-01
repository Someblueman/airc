package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Someblueman/airc/skills"
)

func TestSkillHasValidFrontmatter(t *testing.T) {
	text := skills.Airc
	if !strings.HasPrefix(text, "---\n") {
		t.Fatal("SKILL.md must start with frontmatter")
	}
	end := strings.Index(text[4:], "\n---\n")
	if end < 0 {
		t.Fatal("frontmatter is not closed")
	}
	front := text[4 : 4+end]
	name := regexp.MustCompile(`(?m)^name: (.+)$`).FindStringSubmatch(front)
	desc := regexp.MustCompile(`(?m)^description: (.+)$`).FindStringSubmatch(front)
	if name == nil || name[1] != "airc" {
		t.Fatalf("name must be airc, frontmatter:\n%s", front)
	}
	if desc == nil || len(desc[1]) < 80 || len(desc[1]) > 1024 {
		t.Fatalf("description must be specific but under 1024 characters, got %v", desc)
	}
	if strings.Contains(text, "\t") {
		t.Error("SKILL.md contains tabs")
	}
	onDisk, err := os.ReadFile("../../skills/airc/SKILL.md")
	if err != nil || string(onDisk) != text {
		t.Errorf("embedded skill differs from skills/airc/SKILL.md: %v", err)
	}
}

// Everything the skill tells an agent to type must exist, so renaming a command
// or flag cannot silently leave agents with broken instructions.
func TestSkillOnlyMentionsRealCommandsAndFlags(t *testing.T) {
	usage := map[string]string{}
	for _, sub := range []string{"send", "check", "history", "agents", "names", "watch", "topic"} {
		var stderr bytes.Buffer
		args := []string{sub, "-h"}
		if sub == "history" || sub == "names" || sub == "topic" {
			args = []string{sub, "#room", "-h"} // these take the channel first
		}
		err := run(args, strings.NewReader(""), io.Discard, &stderr)
		if err != nil && strings.Contains(err.Error(), "unknown command") {
			t.Fatalf("subcommand %q does not exist", sub)
		}
		usage[sub] = stderr.String()
	}
	commands := regexp.MustCompile(`(?m)^\s*(?:\$ )?airc (\w+)`).FindAllStringSubmatch(skills.Airc, -1)
	if len(commands) < 5 {
		t.Fatalf("expected the skill to contain example commands, found %d", len(commands))
	}
	for _, m := range commands {
		if _, known := usage[m[1]]; !known {
			t.Errorf("skill uses `airc %s`, which this test does not know; add it or fix the skill", m[1])
		}
	}
	// Every --flag on a line that invokes a subcommand must be defined by it.
	for _, line := range strings.Split(skills.Airc, "\n") {
		m := regexp.MustCompile(`airc (send|check|history|agents|names|topic)\b`).FindStringSubmatch(line)
		if m == nil {
			continue
		}
		for _, flag := range regexp.MustCompile(`--([a-z]+)`).FindAllStringSubmatch(line, -1) {
			if !strings.Contains(usage[m[1]], "-"+flag[1]) {
				t.Errorf("skill passes --%s to `airc %s`, which does not accept it:\n%s", flag[1], m[1], line)
			}
		}
	}
}

func TestSkillInstallWritesUpdatesAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	if err := run([]string{"skill", "install", "--dir", dir}, strings.NewReader(""), &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "airc", "SKILL.md")
	got, err := os.ReadFile(target)
	if err != nil || string(got) != skills.Airc || !strings.Contains(out.String(), "installed") {
		t.Fatalf("install: %v %q", err, out.String())
	}
	out.Reset()
	if err := run([]string{"skill", "install", "--dir", dir}, strings.NewReader(""), &out, io.Discard); err != nil || !strings.Contains(out.String(), "up to date") {
		t.Fatalf("second install should be a no-op: %v %q", err, out.String())
	}
	if err := os.WriteFile(target, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := run([]string{"skill", "install", "--dir", dir}, strings.NewReader(""), &out, io.Discard); err != nil || !strings.Contains(out.String(), "updated") {
		t.Fatalf("stale skill should be updated: %v %q", err, out.String())
	}
	if got, _ := os.ReadFile(target); string(got) != skills.Airc {
		t.Fatal("stale skill was not replaced")
	}
}

func TestSkillShowPathAndDefaults(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"skill", "show"}, strings.NewReader(""), &out, io.Discard); err != nil || out.String() != skills.Airc {
		t.Fatalf("show: %v", err)
	}
	out.Reset()
	t.Setenv("CLAUDE_CONFIG_DIR", "/cfg")
	if err := run([]string{"skill", "path"}, strings.NewReader(""), &out, io.Discard); err != nil || strings.TrimSpace(out.String()) != "/cfg/skills/airc/SKILL.md" {
		t.Fatalf("path = %q, %v", out.String(), err)
	}
	if err := run([]string{"skill", "install", "--dir", "a", "--project"}, strings.NewReader(""), io.Discard, io.Discard); err == nil {
		t.Fatal("--dir and --project together should be rejected")
	}
	if err := run([]string{"skill", "bogus"}, strings.NewReader(""), io.Discard, io.Discard); err == nil {
		t.Fatal("unknown skill action should be rejected")
	}
}
