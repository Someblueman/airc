package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/fallback"
	"github.com/Someblueman/airc/pkg/irc"
)

type sendResult struct {
	Receipt   *protocol.ReceiptInfo `json:"receipt,omitempty"`
	Code      string                `json:"code"`
	Retryable bool                  `json:"retryable"`
	*irc.MessageEvent
	// Delivered is reported for direct messages: true when the recipient was
	// connected, false when the message was queued for them to read later.
	Delivered *bool `json:"delivered,omitempty"`
	// Carrier names the fallback carrier that posted the message when native
	// IRC was unreachable; it is omitted for native IRC.
	Carrier string `json:"carrier,omitempty"`
}

func runSendSession(ctx context.Context, session *agentConnection, args []string, stdin io.Reader, stdout, stderr io.Writer) (resultErr error) {
	fs := flag.NewFlagSet("airc send", flag.ContinueOnError)
	fs.SetOutput(stderr)
	opt := addOptions(fs)
	opt.session = session
	channel := fs.String("channel", "", "channel target (env AIRC_CHANNEL)")
	to := fs.String("to", "", "direct message recipient")
	reaction := fs.String("reaction", "", "reaction symbol; requires --reply-to")
	replyTo := fs.String("reply-to", "", "reply to this message ID in its original room or DM conversation")
	requestID := fs.String("request-id", "", "safe retry key (1-64 letters/digits/-/_); retained-history window")
	retry := fs.String("retry", "", "recover a saved request receipt without creating a message")
	pending := fs.Bool("pending", false, "list saved sends with uncertain outcomes")
	forget := fs.String("forget", "", "forget an outbox entry without changing server history")
	check := fs.Bool("check", false, "read bounded new messages after sending, using the same connection")
	maxMessages := fs.Int("max-messages", 100, "maximum messages returned by --check (1-1000)")
	maxBytes := fs.Int("max-bytes", 32768, "maximum combined send/check output bytes (1024-1048576)")
	message := fs.String("message", "", "message body; may span lines; use - to read it from stdin")
	file := fs.String("file", "", "share a UTF-8 file as a code block; - reads stdin; --message adds a caption")
	language := fs.String("language", "", "code-block language (inferred for --file); formats --message as code when used alone")
	defer func() { reportFailure(&resultErr, opt.json, stderr) }()
	if err := parseAgentFlags(fs, args, opt, stderr); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: airc send --message TEXT (--channel ROOM | --to NICK | --reply-to ID) [--check]")
	}
	if *retry != "" || *pending || *forget != "" {
		modes := 0
		if *retry != "" {
			modes++
		}
		if *pending {
			modes++
		}
		if *forget != "" {
			modes++
		}
		if modes != 1 || *channel != "" || *to != "" || *replyTo != "" || *message != "" || *file != "" || *language != "" || *requestID != "" || *reaction != "" || *check {
			return errors.New("choose one of --retry, --pending or --forget; omit message, target and --check flags")
		}
		if err := identity(opt); err != nil {
			return err
		}
		return runOutboxCommand(ctx, *opt, *retry, *pending, *forget, stdout)
	}
	if *channel == "" && *to == "" && *replyTo == "" {
		*channel = os.Getenv("AIRC_CHANNEL")
	}
	*channel = channelName(*channel)
	target := *channel
	if *replyTo != "" {
		if !protocol.ValidMessageID(*replyTo) || *channel != "" || *to != "" {
			return errors.New("--reply-to requires a message ID and cannot be combined with --channel or --to")
		}
		target = "reply:" + *replyTo
	} else if (*channel == "") == (*to == "") {
		return errors.New("provide exactly one of --channel or --to")
	}
	if *to != "" {
		target = *to
	}
	if *requestID != "" && !protocol.ValidRequestID(*requestID) {
		return errors.New("--request-id must contain 1-64 letters/digits/-/_")
	}
	if *reaction != "" {
		if *requestID != "" {
			return errors.New("--request-id applies to messages and replies; omit it for reactions")
		}
		if *replyTo == "" || !protocol.ValidReaction(*reaction) || *message != "" || *file != "" || *language != "" {
			return errors.New("a reaction requires --reply-to and a single symbol; omit message/file/language")
		}
		*message = *reaction
	}
	var err error
	if *message, err = sendBody(*file, *language, *message, stdin); err != nil {
		return err
	}
	if err := identity(opt); err != nil {
		return err
	}
	settings := &checkOptions{limit: 100, initial: 20, maxMessages: *maxMessages, maxBytes: *maxBytes}
	// Until the receipt resolves a reply's destination, check only the inbox.
	// A launcher channel must not redirect a DM conversation's follow-up read.
	settings.mentions = *replyTo != ""
	if *channel != "" {
		settings.channels = listFlag{*channel}
	}
	if *check {
		if _, err := settings.targets(opt.nick); err != nil {
			return err
		}
		encoded, _ := json.Marshal(*message)
		minimum := len(encoded) + len(target) + len(opt.nick) + 512 + 1024
		if *maxBytes < minimum {
			return fmt.Errorf("send --check needs --max-bytes at least %d for the receipt and check metadata", minimum)
		}
	}
	client, err := dialPrimary(ctx, *opt)
	if err != nil {
		// Native IRC is unreachable: post over the server's other carriers.
		// A reply's room is resolved by native IRC, so replies wait for it.
		if fb, ok := fallbackFor(*opt, err); ok && *reaction == "" && *replyTo == "" {
			return sendViaFallback(ctx, *opt, fb, err, target, *message, *requestID, *check, settings, *maxBytes, stdout, stderr)
		}
		return failure(err, "login")
	}
	defer func() { closeOneShot(*opt, client) }()
	initialClient := client
	checkClient := client
	defer closeOnCancel(ctx, initialClient)()
	if *channel != "" && !client.Ephemeral() {
		if err := client.Join(*channel); err != nil {
			return err
		}
	}
	var box *outbox
	var entry *outboundMessage
	var result *sendResult
	lookup := false
	if *reaction == "" && client.Supports("SAFE_RETRY") {
		box, err = openOutbox(*opt)
		if err != nil {
			return err
		}
		defer box.close()
		lookup = *requestID != "" && box.find(*requestID) != nil
		entry, err = box.add(target, *replyTo, *message, *requestID)
		if err != nil {
			return err
		}
		*requestID = entry.RequestID
		result = entry.Result
	}
	if result == nil {
		awaited := false
		if lookup {
			err = client.RetryRequest(*requestID)
		} else if *reaction != "" {
			err = client.React(*replyTo, *reaction)
		} else if *replyTo != "" {
			err = client.ReplyWithID(*replyTo, *message, *requestID)
		} else {
			err = client.SendWithID(target, *message, *requestID)
		}
		if err == nil {
			awaited = true
			attempt := ctx
			stop := func() {}
			if entry != nil {
				attempt, stop = sendAttemptContext(ctx)
			}
			result, err = awaitSend(attempt, client, opt.nick, target, *message, *replyTo, *reaction, *requestID, nil)
			stop()
		}
		if err != nil && entry != nil && failure(err, "send").Retryable && !rejectedSend(err) && ctx.Err() == nil {
			_ = client.Close()
			lookup = true // recovery failures do not prove that the first send was rejected
			var recovered *irc.Client
			result, recovered, err = recoverSend(ctx, *opt, entry)
			if recovered != nil {
				client, checkClient = recovered, recovered
			}
			// Native IRC went away after the send may have landed: retry on
			// the server's other carriers with the same request ID, which
			// resolves to that message if it exists.
			if fb, ok := fallbackFor(*opt, err); ok && entry.ReplyTo == "" && ctx.Err() == nil {
				result, _, err = postViaFallback(ctx, fb, entry, opt.nick)
				checkClient = nil
			}
		}
		if err != nil {
			if entry != nil && !lookup && (rejectedSend(err) || !awaited && !failure(err, "send").Retryable) {
				box.remove(entry.RequestID)
				if saveErr := box.save(); saveErr != nil {
					return fmt.Errorf("send rejected (%v); outbox cleanup failed: %w", err, saveErr)
				}
				return failure(err, "send")
			}
			if entry != nil {
				return uncertainSend(err, entry.RequestID)
			}
			return fmt.Errorf("send to %s failed: %w", target, err)
		}
		if entry != nil {
			entry.Result = result
			if err := box.save(); err != nil {
				e := acceptedFailure(err, result)
				e.Code = "accepted_state_failed"
				e.Phase = "outbox"
				return e
			}
		}
	}
	if err := outputSend(ctx, *opt, result, *check, settings, *maxBytes, checkClient, stdout, stderr); err != nil {
		return acceptedFailure(err, result)
	}
	return nil
}

// sendViaFallback posts over the server's other carriers when native IRC is
// unreachable. The outbox keeps the request ID across attempts exactly as a
// native send does, so a later retry on any carrier is the same message.
func sendViaFallback(ctx context.Context, opt options, fb *fallback.Client, native error, target, message, requestID string, check bool, settings *checkOptions, maxBytes int, stdout, stderr io.Writer) error {
	box, err := openOutbox(opt)
	if err != nil {
		return err
	}
	defer box.close()
	lookup := requestID != "" && box.find(requestID) != nil
	entry, err := box.add(target, "", message, requestID)
	if err != nil {
		return err
	}
	result := entry.Result
	if result == nil {
		var answered fallback.Endpoint
		result, answered, err = postViaFallback(ctx, fb, entry, opt.nick)
		if err != nil {
			if !lookup && (fallback.Definite(err) || !fallback.MayHavePosted(err)) {
				box.remove(entry.RequestID)
				if saveErr := box.save(); saveErr != nil {
					return fmt.Errorf("send failed (%v); outbox cleanup failed: %w", err, saveErr)
				}
				if fallback.Definite(err) {
					return failure(err, "send")
				}
				return failure(unreachableEverywhere(native, err), "login")
			}
			return uncertainSend(unreachableEverywhere(native, err), entry.RequestID)
		}
		entry.Result = result
		if err := box.save(); err != nil {
			e := acceptedFailure(err, result)
			e.Code = "accepted_state_failed"
			e.Phase = "outbox"
			return e
		}
		learnFromGateway(ctx, opt, fb, answered)
	}
	if err := outputSend(ctx, opt, result, check, settings, maxBytes, nil, stdout, stderr); err != nil {
		return acceptedFailure(err, result)
	}
	return nil
}

// postViaFallback posts an outbox entry with its own request ID.
func postViaFallback(ctx context.Context, fb *fallback.Client, entry *outboundMessage, nick string) (*sendResult, fallback.Endpoint, error) {
	receipt, err := fb.Post(ctx, fallback.Message{Target: entry.Target, Body: entry.Body, RequestID: entry.RequestID})
	if err != nil {
		return nil, fallback.Endpoint{}, err
	}
	return fallbackResult(receipt, nick, entry.Target, entry.Body), receipt.Endpoint, nil
}

// fallbackResult is a carrier's receipt in the shape of a native one. The
// timestamp is when this client saw the acceptance.
func fallbackResult(receipt *fallback.Receipt, nick, target, body string) *sendResult {
	msg := &irc.MessageEvent{Type: "message", ID: receipt.MessageID, From: nick, Target: target, Message: body, Timestamp: time.Now().UTC()}
	msg.RequestID = receipt.RequestID
	r := &sendResult{MessageEvent: msg, Code: "accepted", Carrier: receipt.Endpoint.String()}
	if receipt.Persisted != nil {
		r.Receipt = &protocol.ReceiptInfo{Accepted: true, Persisted: *receipt.Persisted, RecipientConnected: receipt.RecipientConnected}
		if !*receipt.Persisted {
			r.Code = "accepted_not_persisted"
		}
	}
	if !protocol.IsChannel(target) && receipt.RecipientConnected != nil {
		delivered := *receipt.RecipientConnected
		r.Delivered = &delivered
	}
	return r
}

// sendBody resolves what to post: a snippet from a file, text from stdin, or
// the literal flag value, normalised and checked against the message limit.
func sendBody(file, language, message string, stdin io.Reader) (string, error) {
	if file != "" || language != "" {
		body, err := snippetMessage(file, language, message, stdin)
		if err != nil {
			return "", err
		}
		message = body
	} else if message == "-" {
		data, err := io.ReadAll(io.LimitReader(stdin, 1<<20))
		if err != nil {
			return "", fmt.Errorf("read message from stdin: %w", err)
		}
		message = strings.TrimRight(string(data), "\r\n")
	}
	message = irc.NormalizeMessage(message)
	if len(message) > 4096 {
		return "", errors.New("message exceeds 4096 bytes")
	}
	if strings.TrimSpace(message) == "" {
		return "", errors.New("--message is required")
	}
	return message, nil
}

func outputSend(ctx context.Context, opt options, result *sendResult, check bool, settings *checkOptions, maxBytes int, client *irc.Client, stdout, stderr io.Writer) error {
	msg := result.MessageEvent
	var response bytes.Buffer
	var err error
	if opt.json {
		err = json.NewEncoder(&response).Encode(result)
	} else {
		suffix := ""
		if msg.Reaction != "" {
			suffix = " (reaction to " + msg.ReplyTo + ")"
		}
		if result.Delivered != nil && !*result.Delivered {
			suffix = fmt.Sprintf(" (queued: %s can read it with airc check)", msg.Target)
		}
		if result.Receipt != nil && !result.Receipt.Persisted {
			suffix += " (accepted in memory; not persisted to disk)"
		}
		_, err = fmt.Fprintf(&response, "%s -> %s: %s%s\n", msg.From, msg.Target, indentContinuation(msg.Message), suffix)
	}
	if err != nil {
		return err
	}
	if check && response.Len()+1024 > maxBytes {
		return fmt.Errorf("message sent as %s; combined output needs a larger --max-bytes budget; do not resend", msg.ID)
	}
	receiptBytes := response.Len()
	if _, err := io.Copy(stdout, &response); err != nil {
		return err
	}
	if check {
		if client == nil {
			return fmt.Errorf("message sent as %s; --check reads over native IRC, which is unreachable (do not resend; use airc check)", msg.ID)
		}
		if msg.ReplyTo != "" && protocol.IsChannel(msg.Target) {
			settings.channels = listFlag{msg.Target}
			settings.mentions = false
		}
		settings.maxBytes -= receiptBytes
		if err := checkWithClient(ctx, opt, settings, client, stdout, stderr); err != nil {
			return fmt.Errorf("message sent as %s; check failed: %w (do not resend; use airc check)", msg.ID, err)
		}
	}
	return nil
}
