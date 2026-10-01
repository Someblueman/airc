package main

import (
	"fmt"
	"github.com/Someblueman/airc/pkg/irc"
	"strings"
)

func chatBody(m *irc.MessageEvent) string {
	body := m.Message
	switch m.Kind {
	case "action":
		body = "* " + m.From + " " + body
	case "correct":
		body = "[correction of " + m.Supersedes + "] " + body
	case "retract":
		body = "[retraction of " + m.Supersedes + "] " + body
	case "poll":
		body = "[poll " + m.ID + "] " + body
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
