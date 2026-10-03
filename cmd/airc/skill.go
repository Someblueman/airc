package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Someblueman/airc/skills"
)

const skillUsage = `usage:
  airc skill show                      print the skill (for agents without skill support)
  airc skill reference                 print the detailed reference the skill points to
  airc skill install [--dir DIR]       install DIR/airc/SKILL.md and REFERENCE.md
  airc skill install --project         install into ./.claude/skills for this project only
  airc skill path [--dir DIR]          print where install would write

The default DIR is $CLAUDE_CONFIG_DIR/skills, or ~/.claude/skills. For another
agent, pass the skills directory that agent reads with --dir.`

func runSkill(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] == "show" {
		_, err := io.WriteString(stdout, skills.Airc)
		return err
	}
	if args[0] == "reference" {
		_, err := io.WriteString(stdout, skills.Reference)
		return err
	}
	action := args[0]
	if action != "install" && action != "path" {
		return skillUsageError(stderr)
	}
	fs := flag.NewFlagSet("airc skill "+action, flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", "", "skills directory to install into")
	project := fs.Bool("project", false, "install into ./.claude/skills instead of the user-level directory")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 || (*dir != "" && *project) {
		return skillUsageError(stderr)
	}
	root := *dir
	switch {
	case *project:
		root = filepath.Join(".claude", "skills")
	case root == "":
		var err error
		if root, err = defaultSkillsDir(); err != nil {
			return err
		}
	}
	target := filepath.Join(root, "airc", "SKILL.md")
	if action == "path" {
		_, err := fmt.Fprintln(stdout, target)
		return err
	}
	reference := filepath.Join(filepath.Dir(target), "REFERENCE.md")
	previous, readErr := os.ReadFile(target)
	previousReference, _ := os.ReadFile(reference)
	if readErr == nil && string(previous) == skills.Airc && string(previousReference) == skills.Reference {
		_, err := fmt.Fprintf(stdout, "airc skill is already up to date: %s\n", target)
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("create skill directory: %w", err)
	}
	if err := os.WriteFile(reference, []byte(skills.Reference), 0o644); err != nil {
		return fmt.Errorf("write skill reference: %w", err)
	}
	if err := os.WriteFile(target, []byte(skills.Airc), 0o644); err != nil {
		return fmt.Errorf("write skill: %w", err)
	}
	verb := "installed"
	if readErr == nil {
		verb = "updated"
	}
	_, err := fmt.Fprintf(stdout, "airc skill %s: %s\n", verb, target)
	return err
}

func skillUsageError(stderr io.Writer) error {
	fmt.Fprintln(stderr, skillUsage)
	return errors.New("unknown or invalid airc skill command")
}

func defaultSkillsDir() (string, error) {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "skills"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home directory (pass --dir): %w", err)
	}
	return filepath.Join(home, ".claude", "skills"), nil
}
