package backend

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/pkg/irc"
)

func pinCollectorInput(t *testing.T) (store.Attempt, string, string) {
	t.Helper()
	parent := strings.Repeat("a", 32)
	request := core.Request{Version: 1, RequestID: "pin-boundary", Operation: core.PinsAdd, Target: &core.Target{Kind: "room", Name: "#room"}, ParentID: parent}
	message := irc.MessageMetadata{ID: parent, Target: request.Target.Name, From: "author", Message: "root", Timestamp: time.Now().UTC()}
	proof := pinChatLine(t, irc.ChatEntry{Action: "context-message", Message: &message}) + pinChatLine(t, irc.ChatEntry{Action: "context", Context: &irc.ContextSummary{TriggerID: parent, RootID: parent}})
	ack := pinChatLine(t, irc.ChatEntry{Action: "pin", Target: request.Target.Name, ID: parent})
	return store.Attempt{Kind: store.SendAttempt, Deadline: time.Now().Add(2 * time.Second), Job: store.Job{Request: request}}, proof, ack
}

func TestPinMutationCollectorRejectsMixedAndUncertainEvidence(t *testing.T) {
	attempt, proof, valid := pinCollectorInput(t)
	for name, ack := range map[string]string{
		"duplicate":     valid + valid,
		"mixed-author":  pinChatLine(t, irc.ChatEntry{Action: "pin", Target: "#room", ID: attempt.Job.Request.ParentID, From: "author"}),
		"mixed-message": pinChatLine(t, irc.ChatEntry{Action: "pin", Target: "#room", ID: attempt.Job.Request.ParentID, Message: &irc.MessageMetadata{}}),
		"malformed":     ":server 777 reader :{\r\n",
		"absent":        "",
	} {
		t.Run(name, func(t *testing.T) {
			client, _, stop := pinMutationFixture(t, proof, ack)
			defer stop()
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			outcome := pinExchange(ctx, client, attempt)
			if outcome.Pin != nil || !outcome.Completion.MayHavePosted || outcome.Completion.Failure.Code != core.DeliveryUncertain {
				t.Fatalf("untrusted ACK accepted or lost uncertainty: %+v", outcome)
			}
		})
	}
}

func TestPinMutationCollectorContinuousNoiseHonorsDeadline(t *testing.T) {
	for _, phase := range []string{"proof", "ack"} {
		t.Run(phase, func(t *testing.T) {
			attempt, proof, _ := pinCollectorInput(t)
			client, commands, stop := pinMutationScriptFixture(t, func(conn net.Conn, line string) bool {
				if strings.Contains(line, `"action":"context"`) && phase == "ack" {
					_, _ = fmt.Fprint(conn, proof, ":server 778 reader :End of chat response\r\n")
					return false
				}
				if strings.Contains(line, `"action":"context"`) || strings.Contains(line, `"action":"pin"`) {
					ticker := time.NewTicker(2 * time.Millisecond)
					defer ticker.Stop()
					for range ticker.C {
						if _, err := fmt.Fprint(conn, ":server NOTICE reader :continuous noise\r\n"); err != nil {
							return true
						}
					}
				}
				return false
			})
			defer stop()
			ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
			defer cancel()
			start := time.Now()
			outcome := pinExchange(ctx, client, attempt)
			if time.Since(start) > time.Second || outcome.Pin != nil || outcome.Completion.MayHavePosted != (phase == "ack") {
				t.Fatalf("%s noise extended deadline or lost write boundary: %+v", phase, outcome)
			}
			if phase == "proof" {
				select {
				case command := <-commands:
					t.Fatalf("timed out proof wrote: %s", command)
				default:
				}
			}
		})
	}
}
