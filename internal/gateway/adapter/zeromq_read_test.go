//go:build darwin || linux

package adapter

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

// Scripted native CHECK output exercises the SDK; it is not native qualification.
func TestZeroMQWholeHistoryAndOversizedReply(t *testing.T) {
	for _, large := range []bool{false, true} {
		t.Run(map[bool]string{false: "whole", true: "oversized"}[large], func(t *testing.T) {
			f, e := localREPReadFixture(t)
			// Reuse the existing SDK CHECK fixture's fixed target selector.
			f.c.Kafka = []config.Kafka{{Route: config.KafkaRoute{Target: *amqpRequest().Target}}}
			messages := []irc.MessageMetadata{{ID: fmt.Sprintf("%032x", 1), Target: "#fixture", From: "native-author", Timestamp: time.Unix(1000, 0).UTC(), Message: "exact\n世界"}, {ID: fmt.Sprintf("%032x", 2), Target: "#fixture", From: "native-author", Timestamp: time.Unix(1000, 0).UTC(), Message: "whole end"}}
			if large {
				for i := 0; i < 20; i++ {
					messages = append(messages, irc.MessageMetadata{ID: fmt.Sprintf("%032x", i+3), Target: "#fixture", From: "native-author", Timestamp: time.Unix(1000, 0).UTC(), Message: strings.Repeat("x", 4096)})
				}
			}
			reads := kafkaNativeHistory(t, f, messages, "CHECK=1")
			server, client := net.Pipe()
			defer client.Close()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan struct{})
			go func() { serveZeroMQConnection(ctx, server, f.d, make(chan struct{}, 1)); close(done) }()
			defer func() { cancel(); <-done }()
			zeroMQClient(t, client)
			request := core.Request{Version: 1, RequestID: "read", Operation: core.HistoryRead, Target: amqpRequest().Target, Limit: len(messages)}
			response := zeroMQRequest(t, client, localREPEncryptedRequest(t, f, e, request))
			if large {
				if response.Result != nil || response.Error == nil || response.Error.Code != core.CapacityExceeded {
					t.Fatalf("oversized reply %+v", response)
				}
			} else {
				if response.Error != nil || response.Result == nil || response.Result.Page == nil {
					t.Fatalf("read %+v failure %+v", response, response.Error)
				}
				page := response.Result.Page
				if len(page.Messages) != 2 || page.Messages[0].Message != "exact\n世界" || page.Messages[1].Message != "whole end" || page.Status != "more" || !page.Gap || page.Cursor == "" {
					t.Fatalf("whole page %+v", page)
				}
			}
			if reads.Load() != 1 {
				t.Fatalf("native reads %d", reads.Load())
			}
			_ = client.SetDeadline(time.Now().Add(time.Second))
		})
	}
}
