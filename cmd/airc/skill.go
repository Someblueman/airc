package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Someblueman/airc/skills"
)

const skillUsage = `Usage:
  airc skill show                      print the skill (for agents without skill support)
  airc skill reference [SECTION]       print the detailed reference the skill points to, or one
                                       of its sections (by heading, case-insensitive, prefix allowed)
  airc skill reference --list          list the reference's section names
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
	action := args[0]
	if action == "-h" || action == "--help" || action == "-help" {
		fmt.Fprintln(stderr, skillUsage)
		return flag.ErrHelp
	}
	if action == "reference" {
		return runSkillReference(args[1:], stdout, stderr)
	}
	if action != "install" && action != "path" {
		return skillUsageError(stderr)
	}
	fs := flag.NewFlagSet("airc skill "+action, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprintln(stderr, skillUsage) }
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

// runSkillReference prints the whole reference, one section of it named by
// its heading, or the list of section names.
func runSkillReference(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("airc skill reference", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprintln(stderr, skillUsage) }
	list := fs.Bool("list", false, "list the section names instead of printing the reference")
	if err := fs.Parse(args); err != nil {
		return err
	}
	sections := referenceSections(skills.Reference)
	if *list {
		for _, s := range sections {
			if _, err := fmt.Fprintln(stdout, s.name); err != nil {
				return err
			}
		}
		return nil
	}
	if fs.NArg() == 0 {
		_, err := io.WriteString(stdout, skills.Reference)
		return err
	}
	want := strings.Join(fs.Args(), " ")
	matches := matchSections(sections, want)
	if len(matches) != 1 {
		why := "matches no section"
		if len(matches) > 1 {
			why = "is ambiguous"
		}
		fmt.Fprintf(stderr, "airc skill reference: %q %s; the sections are:\n", want, why)
		for _, s := range sections {
			fmt.Fprintf(stderr, "  %s\n", s.name)
		}
		return fmt.Errorf("no reference section named %q", want)
	}
	_, err := io.WriteString(stdout, matches[0].text)
	return err
}

type referenceSection struct {
	name string // the heading without its "## "
	text string // the heading line through the end of the section
}

// referenceSections splits the reference at its "## " headings. Text before
// the first heading (the title and its introduction) belongs to no section.
func referenceSections(reference string) []referenceSection {
	var out []referenceSection
	for _, chunk := range strings.Split("\n"+reference, "\n## ")[1:] {
		name, _, _ := strings.Cut(chunk, "\n")
		out = append(out, referenceSection{name: strings.TrimSpace(name), text: "## " + strings.TrimRight(chunk, "\n") + "\n"})
	}
	return out
}

// matchSections finds the sections named by want: an exact heading match,
// ignoring case, wins outright; otherwise every heading want is a prefix of.
func matchSections(sections []referenceSection, want string) []referenceSection {
	want = strings.ToLower(strings.TrimSpace(want))
	var prefixed []referenceSection
	for _, s := range sections {
		name := strings.ToLower(s.name)
		if name == want {
			return []referenceSection{s}
		}
		if want != "" && strings.HasPrefix(name, want) {
			prefixed = append(prefixed, s)
		}
	}
	return prefixed
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
