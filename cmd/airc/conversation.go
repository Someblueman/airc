package main

import (
	"errors"
	"io"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

// Reuse history's bounded paging and output without moving inbox cursors.
func runThread(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || !protocol.ValidMessageID(args[0]) {
		return errors.New("usage: airc thread MESSAGE_ID [--after ID] [--limit 50] [--json]")
	}
	query := append([]string{"thread:" + args[0]}, args[1:]...)
	return runHistory(query, stdout, stderr)
}

func historyLabel(target string, message *irc.HistoryEvent) string {
	label := message.From
	if _, _, conversation := protocol.ConversationTarget(target); conversation {
		label += " [" + message.ID + "]"
	}
	if message.Reaction != "" {
		label += " (reacted " + message.Reaction + " to " + message.ReplyTo + ")"
	} else if message.ReplyTo != "" {
		label += " (reply to " + message.ReplyTo + ")"
	}
	return label
}
