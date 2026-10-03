// Package skills embeds the agent skill so the airc binary can install or print
// the version that matches it.
package skills

import _ "embed"

// Airc is the SKILL.md for agents that use airc: the core workflow.
//
//go:embed airc/SKILL.md
var Airc string

// Reference is the detail SKILL.md points to, installed next to it.
//
//go:embed airc/REFERENCE.md
var Reference string
