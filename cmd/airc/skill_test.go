package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
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
	skillText := skills.Airc + "\n" + skills.Reference
	// Positional arguments and actions that must precede -h for the command to
	// print its flags; every other command takes -h first.
	id := strings.Repeat("a", 32)
	leading := map[string][]string{
		"user": {"create"}, "service": {"install"}, "admin": {"list"}, "skill": {"install"},
		"history": {"#room"}, "names": {"#room"}, "topic": {"#room"}, "charter": {"#room"},
		"thread": {id}, "context": {id}, "search": {"query"}, "react": {id, "seen"},
	}
	usage := map[string]string{}
	var names []string
	for _, sub := range commands {
		var stderr bytes.Buffer
		args := append(append([]string{sub}, leading[sub]...), "-h")
		err := run(args, strings.NewReader(""), io.Discard, &stderr)
		if err != nil && strings.Contains(err.Error(), "unknown command") {
			t.Fatalf("subcommand %q does not exist", sub)
		}
		if !strings.Contains(stderr.String(), "Usage") {
			t.Errorf("airc %s does not print its flags; fix leading", strings.Join(args, " "))
		}
		usage[sub] = stderr.String()
		names = append(names, regexp.QuoteMeta(sub))
	}
	// Longest first, so poll-results is not matched as poll.
	slices.SortFunc(names, func(a, b string) int { return len(b) - len(a) })
	mentions := regexp.MustCompile(`(?m)^\s*(?:\$ )?airc (\w+)`).FindAllStringSubmatch(skillText, -1)
	if len(mentions) < 5 {
		t.Fatalf("expected the skill to contain example commands, found %d", len(mentions))
	}
	for _, m := range mentions {
		if _, known := usage[m[1]]; !known {
			t.Errorf("skill uses `airc %s`, which is not a command; fix the skill", m[1])
		}
	}
	// Every --flag on a line that invokes a subcommand must be defined by it.
	invocation := regexp.MustCompile(`airc (` + strings.Join(names, "|") + `)\b`)
	for line := range strings.SplitSeq(skillText, "\n") {
		m := invocation.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		for _, flag := range regexp.MustCompile(`--([a-z][a-z-]*)`).FindAllStringSubmatch(line, -1) {
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
	if got, _ := os.ReadFile(filepath.Join(dir, "airc", "REFERENCE.md")); string(got) != skills.Reference || len(got) == 0 {
		t.Fatal("reference was not installed next to the skill")
	}
	out.Reset()
	if err := run([]string{"skill", "reference"}, strings.NewReader(""), &out, io.Discard); err != nil || out.String() != skills.Reference {
		t.Fatalf("reference: %v", err)
	}
	if len(skills.Airc) > 10<<10 {
		t.Fatalf("core skill grew to %d bytes; move detail to REFERENCE.md", len(skills.Airc))
	}
}

// Every section of the reference must be reachable by its heading, so the
// skill can send an agent to `airc skill reference SECTION` instead of loading
// the whole file, and the errors section must cover every code the CLI emits.
func TestSkillReferenceSections(t *testing.T) {
	sections := referenceSections(skills.Reference)
	if len(sections) < 10 {
		t.Fatalf("expected the reference to be split into sections, found %d", len(sections))
	}
	var names, rebuilt []string
	seen := map[string]bool{}
	for _, s := range sections {
		if s.name == "" || seen[strings.ToLower(s.name)] {
			t.Fatalf("section heading %q is empty or repeated", s.name)
		}
		seen[strings.ToLower(s.name)] = true
		names = append(names, s.name)
		rebuilt = append(rebuilt, s.text)
		var out, errOut bytes.Buffer
		if err := run([]string{"skill", "reference", s.name}, strings.NewReader(""), &out, &errOut); err != nil || out.String() != s.text {
			t.Errorf("airc skill reference %q: %v %s", s.name, err, errOut.String())
		}
		out.Reset()
		if err := run([]string{"skill", "reference", strings.ToUpper(s.name)}, strings.NewReader(""), &out, io.Discard); err != nil || out.String() != s.text {
			t.Errorf("section %q is not matched case-insensitively: %v", s.name, err)
		}
	}
	preamble, _, _ := strings.Cut(skills.Reference, "\n## ")
	if preamble+"\n"+strings.Join(rebuilt, "\n") != skills.Reference {
		t.Error("the sections do not add up to the whole reference")
	}
	for _, want := range []string{"Remote access", "Identity", "Presence and directory", "Context and search", "Snippets and pastes", "Follows and reply signals", "Safe sends and recovery", "Pins and corrections", "Polls and reactions", "Handoffs", "Decisions", "Accounts and operators", "Bots", "Errors"} {
		if !slices.Contains(names, want) {
			t.Errorf("reference lacks the %q section; sections: %v", want, names)
		}
	}
	var out bytes.Buffer
	if err := run([]string{"skill", "reference", "--list"}, strings.NewReader(""), &out, io.Discard); err != nil || out.String() != strings.Join(names, "\n")+"\n" {
		t.Errorf("--list = %q, %v", out.String(), err)
	}
	out.Reset()
	if err := run([]string{"skill", "reference", "handoff"}, strings.NewReader(""), &out, io.Discard); err != nil || !strings.HasPrefix(out.String(), "## Handoffs\n") {
		t.Errorf("prefix match: %q, %v", out.String(), err)
	}
	out.Reset()
	var errOut bytes.Buffer
	if err := run([]string{"skill", "reference", "nosuch"}, strings.NewReader(""), &out, &errOut); err == nil || out.Len() != 0 || !strings.Contains(errOut.String(), "Handoffs") {
		t.Errorf("unknown section should fail and list the sections: %v %q", err, errOut.String())
	}
	errOut.Reset()
	if err := run([]string{"skill", "reference", "p"}, strings.NewReader(""), &out, &errOut); err == nil || !strings.Contains(errOut.String(), "ambiguous") {
		t.Errorf("ambiguous prefix should fail: %v %q", err, errOut.String())
	}
	errors := matchSections(sections, "errors")
	if len(errors) != 1 {
		t.Fatal("no Errors section")
	}
	for _, code := range []string{"invalid_request", "invalid_target", "auth_failed", "permission_denied", "limit_reached", "unsupported", "server_rejected", "request_conflict", "rate_limited", "server_busy", "server_unavailable", "timeout", "state_busy", "state_unavailable", "cancelled", "delivery_unknown", "confirmation_unknown", "accepted_output_failed", "accepted_state_failed"} {
		if !strings.Contains(errors[0].text, "| `"+code+"`") && !strings.Contains(errors[0].text, "`"+code+"` |") {
			t.Errorf("the Errors table has no row for %s", code)
		}
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
