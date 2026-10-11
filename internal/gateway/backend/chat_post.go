package backend

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

func chatPostExchange(ctx context.Context, client *irc.Client, a store.Attempt) Outcome {
	request := a.Job.Request
	if a.Kind != store.SendAttempt || request.Target == nil || request.Target.Kind != "room" && request.Target.Kind != "direct" {
		return failed(a, core.Forbidden, false, false)
	}
	nativeAction := "action"
	if request.Operation == core.MessageCorrect {
		nativeAction = "correct"
	} else if request.Operation == core.MessageRetract {
		nativeAction = "retract"
	}
	if request.Operation != core.MessageAction && request.Operation != core.MessageCorrect && request.Operation != core.MessageRetract || request.Operation == core.MessageAction && request.ParentID != "" || request.Operation != core.MessageAction && !validHistoryID(request.ParentID) {
		return failed(a, core.Forbidden, false, false)
	}
	parentRoot := ""
	if request.Operation != core.MessageAction {
		proofCtx, cancelProof := context.WithTimeout(ctx, 5*time.Second)
		proofDeadline, _ := proofCtx.Deadline()
		closedProof := make(chan struct{})
		stopProof := context.AfterFunc(proofCtx, func() { _ = client.Close(); close(closedProof) })
		var err error
		parentRoot, err = proveChatParent(proofCtx, client, request)
		proofTimedOut := proofCtx.Err() != nil || !time.Now().Before(proofDeadline)
		stoppedProof := stopProof()
		cancelProof()
		if !stoppedProof {
			<-closedProof
		}
		if proofTimedOut {
			return failed(a, core.UpstreamUnavailable, true, false)
		}
		if err != nil {
			if errors.Is(err, auth.ErrForbidden) {
				return failed(a, core.Forbidden, false, false)
			}
			if errors.Is(err, ErrReadUnsupported) {
				return failed(a, core.UnsupportedOperation, false, false)
			}
			return failed(a, core.UpstreamUnavailable, true, false)
		}
	}
	if ctx.Err() != nil || !time.Now().Before(a.Deadline) {
		return failed(a, core.UpstreamUnavailable, true, false)
	}
	writeCtx, cancelWrite := context.WithTimeout(ctx, 5*time.Second)
	closed := make(chan struct{})
	stopClose := context.AfterFunc(writeCtx, func() { _ = client.Close(); close(closed) })
	err := client.Chat(irc.ChatRequest{Action: nativeAction, Target: request.Target.Name, ID: request.ParentID, Text: request.Message})
	timedOut := writeCtx.Err() != nil
	stopped := stopClose()
	cancelWrite()
	if !stopped {
		<-closed
	}
	if err != nil || timedOut {
		return failed(a, core.UpstreamUnavailable, true, true)
	}

	waitCtx, cancelWait := context.WithTimeout(ctx, 10*time.Second)
	defer cancelWait()
	var receipt *irc.SendReceiptEvent
	var message *irc.MessageMetadata
	wireBytes := 0
	for {
		select {
		case <-waitCtx.Done():
			return failed(a, core.DeliveryUncertain, true, true)
		case event, ok := <-client.Events():
			if !ok {
				return failed(a, core.DeliveryUncertain, true, true)
			}
			switch value := event.(type) {
			case *irc.SendReceiptEvent:
				if receipt != nil || value.Receipt == nil || value.Type != "send_receipt" {
					return failed(a, core.DeliveryUncertain, true, true)
				}
				copy := *value
				receipt = &copy
				receiptMeta := irc.MessageMetadata{Receipt: value.Receipt, ChatMetadata: value.ChatMetadata, ID: value.ID, ReplyTo: value.ReplyTo, ThreadID: value.ThreadID, Seq: value.Seq, From: value.From, Target: value.Target, Message: value.Message, Timestamp: value.Timestamp}
				wire := len(protocol.Format("server", "762", []string{client.Nick(), value.Target}, protocol.EncodeMessageMetadata(receiptMeta)))
				if wire > core.MaxReceiptBytes || wireBytes > core.MaxReceiptBytes-wire {
					return failed(a, core.CapacityExceeded, true, true)
				}
				wireBytes += wire
			case *irc.ChatEvent:
				if message != nil || receipt == nil || value.Action != nativeAction || value.From != "" || value.AccountID != "" || value.Message == nil || value.Profile != nil || value.Context != nil || value.Text != "" || value.SlowSeconds != 0 || value.HistoryLimit != 0 || len(value.Options) != 0 || len(value.Votes) != 0 || value.Closed || !value.ExpiresAt.IsZero() || value.ID == "" || value.Target != request.Target.Name {
					return failed(a, core.DeliveryUncertain, true, true)
				}
				meta := *value.Message
				if meta.ID != value.ID || meta.Target != value.Target || meta.From != client.Nick() || meta.Kind != nativeAction || meta.SupersededBy != "" || meta.Supersedes != request.ParentID || nativeAction == "action" && request.ParentID != "" || meta.Retracted || nativeAction != "action" && (meta.ReplyTo != request.ParentID || meta.ThreadID != parentRoot) {
					return failed(a, core.DeliveryUncertain, true, true)
				}
				expected := request.Message
				if nativeAction == "retract" && expected == "" {
					expected = "Retracted"
				}
				if meta.Message != expected {
					return failed(a, core.DeliveryUncertain, true, true)
				}
				if !validContextMessageMetadata(meta) || (request.Target.Kind == "room" && !validRoom(meta.Target)) || request.Target.Kind == "direct" && !validNick(meta.Target) {
					return failed(a, core.DeliveryUncertain, true, true)
				}
				copy := meta
				message = &copy
				encoded, err := json.Marshal(value.ChatEntry)
				if err != nil {
					return failed(a, core.DeliveryUncertain, true, true)
				}
				wire := len(protocol.Format("server", "777", []string{client.Nick()}, string(encoded)))
				if wire > core.MaxReceiptBytes || wireBytes > core.MaxReceiptBytes-wire {
					return failed(a, core.CapacityExceeded, true, true)
				}
				wireBytes += wire
			case *irc.EndOfChatEvent:
				if receipt == nil || message == nil {
					return failed(a, core.DeliveryUncertain, true, true)
				}
				end := len(protocol.Format("server", "778", []string{client.Nick()}, "End of chat response"))
				if wireBytes > core.MaxReceiptBytes-end {
					return failed(a, core.CapacityExceeded, true, true)
				}
				if receipt.ID != message.ID || receipt.Target != message.Target {
					return failed(a, core.DeliveryUncertain, true, true)
				}
				receiptMeta := irc.MessageMetadata{ChatMetadata: receipt.ChatMetadata, ID: receipt.ID, ReplyTo: receipt.ReplyTo, ThreadID: receipt.ThreadID, Seq: receipt.Seq, From: receipt.From, Target: receipt.Target, Message: receipt.Message, Timestamp: receipt.Timestamp}
				if !contextMetadataEqual(receiptMeta, *message) {
					return failed(a, core.DeliveryUncertain, true, true)
				}
				return accepted(waitCtx, a, core.Acceptance{Receipt: receipt}, client.Nick())
			case *irc.RawEvent:
				if value.Command == "762" || value.Command == "777" || value.Command == "778" {
					return failed(a, core.DeliveryUncertain, true, true)
				}
				if outcome, handled := numericFailure(a, value); handled {
					if receipt != nil || message != nil {
						uncertain := failed(a, core.DeliveryUncertain, true, true)
						uncertain.Completion.Failure.UpstreamCode = outcome.Completion.Failure.UpstreamCode
						return uncertain
					}
					return outcome
				}
			}
		}
	}
}

func proveChatParent(ctx context.Context, client *irc.Client, request core.Request) (string, error) {
	if request.Target.Kind == "direct" {
		return proveDirectChatParent(ctx, client, request)
	}
	pins, err := boundedPinsRequestBudget(ctx, client, request.Target.Name, core.MaxContextBytes)
	if err != nil {
		return "", err
	}
	for _, pin := range pins {
		if pin.ID == request.ParentID && validContextMessage(pin, request.Target.Name) {
			root := pin.ThreadID
			if root == "" {
				root = pin.ID
			}
			if !validHistoryID(root) {
				return "", auth.ErrForbidden
			}
			return root, nil
		}
	}
	proof, err := boundedContextRequest(ctx, client, request.ParentID, 1, core.MaxContextBytes, "room", nil)
	if err != nil {
		return "", err
	}
	return validateContextProof(request, proof)
}

func proveDirectChatParent(ctx context.Context, client *irc.Client, request core.Request) (string, error) {
	if client.Features()["DIRECTORY"] != "1" {
		return "", ErrReadUnsupported
	}
	cardCtx := ctx
	if err := client.Directory(client.Nick()); err != nil {
		return "", ErrReadUnavailable
	}
	var card *irc.AgentCard
	for {
		select {
		case <-cardCtx.Done():
			return "", ErrReadUnavailable
		case event, ok := <-client.Events():
			if !ok {
				return "", ErrReadUnavailable
			}
			switch value := event.(type) {
			case *irc.DirectoryEvent:
				if card != nil || value.Nick != client.Nick() || !validCard(value.AgentCard) {
					return "", auth.ErrForbidden
				}
				copy := value.AgentCard
				card = &copy
			case *irc.EndOfDirectoryEvent:
				if card == nil {
					return "", auth.ErrForbidden
				}
				goto directoryComplete
			case *irc.RawEvent:
				if outcome, handled := numericFailure(store.Attempt{Kind: store.SendAttempt}, value); handled {
					if outcome.Completion.Failure.Code == core.UnsupportedOperation {
						return "", ErrReadUnsupported
					}
					return "", ErrReadUnavailable
				}
			}
		}
	}

directoryComplete:
	proof, err := boundedContextRequest(ctx, client, request.ParentID, 1, core.MaxContextBytes, "direct", nil)
	if err != nil {
		return "", err
	}
	if !validDirectCorrectionProof(request, proof, *card) {
		return "", auth.ErrForbidden
	}
	return proof.RootID, nil
}

func validDirectCorrectionProof(request core.Request, proof irc.ConversationContext, card irc.AgentCard) bool {
	if card.AccountID == "" || !validHistoryID(card.AccountID) || !validContextSummary(proof) || proof.TriggerID != request.ParentID || len(proof.Messages) != 1 || proof.Messages[0].ID != request.ParentID || containsString(proof.Missing, request.ParentID) {
		return false
	}
	message := proof.Messages[0]
	if !validContextMessageMetadata(message) || message.AccountID != card.AccountID {
		return false
	}
	parentTarget := core.Target{Kind: "direct", Name: message.Target}
	requestedTarget := *request.Target
	if parentTarget.Normalize() != nil || requestedTarget.Normalize() != nil || parentTarget != requestedTarget {
		return false
	}
	root := message.ThreadID
	if root == "" {
		root = message.ID
	}
	return root == proof.RootID && validHistoryID(root)
}
