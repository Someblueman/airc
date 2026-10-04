package main

import (
	"fmt"
	"github.com/Someblueman/airc/pkg/irc"
	"strings"
)

// chatBody renders a message body with the markers its chat metadata calls for.
func chatBody(m irc.ChatMetadata, id, from, text string) string {
	body := text
	switch m.Kind {
	case "notice":
		body = "[notice] " + body
	case "bot":
		body = "[bot] " + body
	case "action":
		body = "* " + from + " " + body
	case "correct":
		body = "[correction of " + m.Supersedes + "] " + body
	case "retract":
		body = "[retraction of " + m.Supersedes + "] " + body
	case "poll":
		body = "[poll " + id + "] " + body
		for i, option := range m.PollOptions {
			body += fmt.Sprintf("\n%d. %s", i+1, option)
		}
	}
	if m.Retracted {
		body = "[retracted; see " + m.SupersededBy + "] " + body
	} else if m.SupersededBy != "" {
		body = "[superseded; see " + m.SupersededBy + "] " + body
	}
	return strings.TrimRight(body, "\n")
}
