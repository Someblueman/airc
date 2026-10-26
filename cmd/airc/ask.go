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
)

const askUsage = "usage: airc ask --nick N (--channel ROOM | --to NICK) --message TEXT|- [--wait 60s] [--run ID] [--json]"

// askMaxWait bounds how long one ask waits for its answer.
const askMaxWait = time.Hour

// runAskSession posts a question through the ordinary send path (the same
// outbox and receipt rules) and then waits, as check --reply-to --wait does,
// for the first textual reply from someone else: reactions and signals do
// not count. It prints the question's receipt, then the reply row, or a
// wait_expired status; the reply is marked read the way check --reply-to
// marks it. One call, for a host that cannot run a wait in the background.
func runAskSession(ctx context.Context, session *agentConnection, args []string, stdin io.Reader, stdout, stderr io.Writer) (resultErr error) {
	fs := flag.NewFlagSet("airc ask", flag.ContinueOnError)
	fs.SetOutput(stderr)
	opt := addOptions(fs)
	opt.session = session
	channel := fs.String("channel", "", "room to ask in (env AIRC_CHANNEL)")
	to := fs.String("to", "", "nickname to ask directly")
	message := fs.String("message", "", "the question; - reads stdin")
	wait := fs.Duration("wait", time.Minute, "how long to wait for the first reply (0 reads only a reply already retained; at most 1h)")
	runFlag := addRunFlag(fs)
	defer func() { reportFailure(&resultErr, opt.json, stderr) }()
	if err := parseAgentFlags(fs, args, opt, stderr); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New(askUsage)
	}
	if *channel == "" && *to == "" {
		*channel = os.Getenv("AIRC_CHANNEL")
	}
	*channel = channelName(*channel)
	if (*channel == "") == (*to == "") || *channel != "" && !protocol.IsChannel(*channel) || *to != "" && !protocol.ValidNick(*to) {
		return errors.New("ask exactly one of a room (--channel) or a nickname (--to)")
	}
	if *wait < 0 || *wait > askMaxWait {
		return errors.New("--wait must be 0 through 1h")
	}
	run, err := runLabel(*runFlag)
	if err != nil {
		return err
	}
	body, err := sendBody("", "", *message, stdin)
	if err != nil {
		return err
	}
	if err := identity(opt); err != nil {
		return err
	}

	// The question goes through send itself, so retries, the outbox and
	// the carriers behave as for any post; its receipt is read back here.
	sendArgs := []string{"--nick", opt.nick, "--message=-", "--json", "--addr", opt.addr}
	for _, pair := range [][2]string{{"--unix", opt.unix}, {"--identity", opt.identityFile}, {"--channel", *channel}, {"--to", *to}, {"--run", run}} {
		if pair[1] != "" {
			sendArgs = append(sendArgs, pair[0], pair[1])
		}
	}
	sendArgs = append(sendArgs, transportArgs(*opt)...)
	var receipt, diagnostics bytes.Buffer
	if err := runSendSession(ctx, session, sendArgs, strings.NewReader(body), &receipt, &diagnostics); err != nil {
		return err // reported once, here
	}
	if _, err := io.Copy(stderr, &diagnostics); err != nil {
		return err
	}
	var sent sendResult
	if err := json.Unmarshal(receipt.Bytes(), &sent); err != nil || sent.MessageEvent == nil || !protocol.ValidMessageID(sent.ID) {
		return fmt.Errorf("the question was sent but its receipt could not be read: %s", strings.TrimSpace(receipt.String()))
	}
	if opt.json {
		if _, err := stdout.Write(receipt.Bytes()); err != nil {
			return err
		}
	} else if _, err := fmt.Fprintf(stdout, "asked %s [%s]: %s\n", sent.Target, sent.ID, indentContinuation(sent.Message)); err != nil {
		return err
	}

	// Then wait as check --reply-to does, printing the first textual reply:
	// the wait bounds the whole read, login included, as check's does.
	timeout := requestTimeout
	if *wait > 0 {
		timeout = *wait
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	settings := &checkOptions{replyTo: sent.ID, wait: *wait, limit: 50, initial: 20, maxMessages: 1, maxBytes: 32768}
	var answer bytes.Buffer
	if err := checkWithClient(waitCtx, *opt, settings, nil, &answer, stderr); err != nil {
		return fmt.Errorf("question sent as %s; waiting for the reply failed: %w (do not ask again; airc check --reply-to %s --wait 60s waits again)", sent.ID, err, sent.ID)
	}
	if opt.json {
		_, err := io.Copy(stdout, &answer)
		return err
	}
	if answer.Len() == 0 {
		_, err := fmt.Fprintf(stdout, "no reply within %s; airc check --reply-to %s --wait 60s waits again\n", *wait, sent.ID)
		return err
	}
	_, err = io.Copy(stdout, &answer)
	return err
}
