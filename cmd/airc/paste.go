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
	"path/filepath"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

// A paste is text larger than a message: a diff, a log, test output. send
// --paste uploads it and posts a message that shares it; paste get, info and
// list read pastes back; paste put uploads one without posting.

// maxPasteRead bounds what the CLI reads for a paste before the daemon says
// how much it accepts: the largest --paste-max-size, plus one byte to tell
// an oversized file from one exactly at the limit.
const maxPasteRead = 8<<20 + 1

// pasteSource is text to upload as a paste before posting.
type pasteSource struct {
	name, language string
	content        []byte
}

// resolveShare decides what a send posts: a paste from --paste, a snippet
// from --file that becomes a paste when it would not fit a message (said on
// stderr), or the message itself. With a paste the body is the caption, and
// the reference line is appended once the paste is uploaded.
func resolveShare(file, paste, name, language, message string, stdin io.Reader, stderr io.Writer) (string, *pasteSource, error) {
	if paste != "" && file != "" {
		return "", nil, errors.New("choose --paste or --file")
	}
	if name != "" && paste == "" && file == "" {
		return "", nil, errors.New("--name goes with --paste or --file")
	}
	if name != "" && !protocol.ValidPasteName(name) {
		return "", nil, fmt.Errorf("--name is 1-%d bytes of printable text without path separators", protocol.MaxPasteNameBytes)
	}
	if paste == "" && file == "" {
		body, err := sendBody("", language, message, stdin)
		return body, nil, err
	}
	if message == "-" {
		return "", nil, errors.New("with --paste or --file, --message is a caption; use - as the path to read stdin")
	}
	caption := irc.NormalizeMessage(message)
	if paste != "" {
		src, err := readPasteSource(paste, name, language, stdin)
		if err != nil {
			return "", nil, err
		}
		return caption, src, nil
	}
	// --file: a snippet when it fits, else the file as a paste.
	var buffered io.Reader = stdin
	var stdinData []byte
	if file == "-" {
		data, err := io.ReadAll(io.LimitReader(stdin, maxPasteRead))
		if err != nil {
			return "", nil, fmt.Errorf("read snippet: %w", err)
		}
		stdinData, buffered = data, bytes.NewReader(data)
	}
	body, err := snippetMessage(file, language, message, buffered)
	if err == nil {
		return body, nil, nil
	}
	if !errors.Is(err, errSnippetTooLarge) {
		return "", nil, err
	}
	var src *pasteSource
	if file == "-" {
		src, err = pasteSourceOf(stdinData, cmpName(name, "stdin.txt"), language)
	} else {
		src, err = readPasteSource(file, name, language, nil)
	}
	if err != nil {
		return "", nil, err
	}
	fmt.Fprintf(stderr, "airc: %s does not fit a %d-byte message as a snippet; sharing it as a paste\n", src.name, 4096)
	return caption, src, nil
}

// readPasteSource reads path (- for stdin) as a paste named name, or after
// the file, in language, or the language its name suggests.
func readPasteSource(path, name, language string, stdin io.Reader) (*pasteSource, error) {
	var reader io.Reader
	if path == "-" {
		if stdin == nil {
			return nil, errors.New("stdin is not available here")
		}
		reader = stdin
		name = cmpName(name, "stdin.txt")
	} else {
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("inspect paste: %w", err)
		}
		if !info.Mode().IsRegular() {
			return nil, errors.New("--paste must be a regular file; use - for stdin")
		}
		file, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("open paste: %w", err)
		}
		defer file.Close()
		reader = file
		name = cmpName(name, filepath.Base(path))
	}
	data, err := io.ReadAll(io.LimitReader(reader, maxPasteRead))
	if err != nil {
		return nil, fmt.Errorf("read paste: %w", err)
	}
	return pasteSourceOf(data, name, language)
}

func cmpName(name, fallback string) string {
	if name != "" {
		return name
	}
	return fallback
}

// pasteSourceOf checks data as a paste: UTF-8 text without NUL, line
// endings normalised, within what any daemon could accept.
func pasteSourceOf(data []byte, name, language string) (*pasteSource, error) {
	if len(data) >= maxPasteRead {
		return nil, fmt.Errorf("the paste exceeds %d bytes, more than any aircd accepts", maxPasteRead-1)
	}
	data = []byte(irc.NormalizeMessage(string(data)))
	if len(data) == 0 || len(bytes.TrimSpace(data)) == 0 {
		return nil, errors.New("the paste is empty")
	}
	if !protocol.ValidPasteText(data) {
		return nil, errors.New("a paste is UTF-8 text without NUL bytes; binary cannot be shared")
	}
	if !protocol.ValidPasteName(name) {
		return nil, fmt.Errorf("the paste's name must be 1-%d bytes of printable text; give one with --name", protocol.MaxPasteNameBytes)
	}
	if language == "" {
		language = snippetLanguage(name)
	}
	if !protocol.ValidPasteLanguage(language) {
		return nil, errors.New("--language must be a short language name, such as go, python or text")
	}
	return &pasteSource{name: name, language: language, content: data}, nil
}

// uploadFor uploads src over client, which must serve PASTE, and returns the
// paste as stored.
func (src *pasteSource) upload(ctx context.Context, client *irc.Client) (irc.PasteInfo, error) {
	if !client.Supports("PASTE") {
		return irc.PasteInfo{}, errors.New("this aircd lacks PASTE; share a smaller excerpt with --file, or upgrade when safe")
	}
	if limit := client.PasteLimit(); len(src.content) > limit {
		return irc.PasteInfo{}, fmt.Errorf("%s is %d bytes; this aircd accepts pastes of at most %d", src.name, len(src.content), limit)
	}
	return irc.UploadPaste(ctx, client, src.name, src.language, src.content, nil)
}

// pasteRow is a paste as paste info, list and get --json print it.
type pasteRow struct {
	Type       string    `json:"type"`
	ID         string    `json:"id"`
	Name       string    `json:"name,omitempty"`
	Language   string    `json:"language,omitempty"`
	Size       int       `json:"size"`
	Lines      int       `json:"lines"`
	From       string    `json:"from"`
	AccountID  string    `json:"account_id,omitempty"`
	Created    time.Time `json:"created"`
	Referenced time.Time `json:"referenced,omitzero"`
	Content    string    `json:"content,omitempty"`
}

func newPasteRow(e irc.ChatEntry) pasteRow {
	row := pasteRow{Type: "paste", ID: e.ID, From: e.From, AccountID: e.AccountID, Created: e.Created, Referenced: e.Referenced}
	if e.Paste != nil {
		row.Name, row.Language, row.Size, row.Lines = e.Paste.Name, e.Paste.Language, e.Paste.Size, e.Paste.Lines
	}
	return row
}

// pasteText describes a paste on one line.
func pasteText(e irc.ChatEntry) string {
	if e.Paste == nil {
		return e.ID
	}
	return fmt.Sprintf("%s  by %s, %s", protocol.PasteReferenceLine(*e.Paste), e.From, e.Created.Local().Format("2006-01-02 15:04"))
}

// runPasteSession reads a paste's content (get), its description (info),
// lists the caller's pastes (list) or uploads one without posting (put).
func runPasteSession(ctx context.Context, session *agentConnection, args []string, stdin io.Reader, stdout, stderr io.Writer) (resultErr error) {
	action := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		action, args = args[0], args[1:]
	}
	id := ""
	if (action == "get" || action == "info") && len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		id, args = args[0], args[1:]
	}
	path := ""
	if action == "put" && len(args) > 0 && !strings.HasPrefix(args[0], "-") || action == "put" && len(args) > 0 && args[0] == "-" {
		path, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet("airc paste", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: airc paste get ID [--out FILE] | info ID | list [--limit N] | put FILE|- [--name N] [--language L]  [--nick N] [--json]")
		fs.PrintDefaults()
	}
	opt := addOptions(fs)
	opt.session = session
	out := fs.String("out", "", "get: write the content to this file instead of stdout")
	limit := fs.Int("limit", 0, "list: newest pastes to list, at most 1000 (default 200)")
	name := fs.String("name", "", "put: the paste's name (default: the file's)")
	language := fs.String("language", "", "put: the paste's language (default: from the name)")
	defer func() { reportFailure(&resultErr, opt.json, stderr) }()
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	switch action {
	case "get", "info":
		if !protocol.ValidIDArgument(id) {
			return fmt.Errorf("usage: airc paste %s PASTE_ID", action)
		}
		if *out != "" && action != "get" {
			return errors.New("--out goes with get")
		}
	case "list":
		if id != "" || *out != "" {
			return errors.New("usage: airc paste list [--limit N] [--json]")
		}
	case "put":
		if path == "" {
			return errors.New("usage: airc paste put FILE|- [--name N] [--language L]")
		}
	default:
		return errors.New("usage: airc paste get ID [--out FILE] | info ID | list | put FILE|-")
	}
	var err error
	if action == "put" || action == "list" {
		err = identity(opt)
	} else {
		err = queryIdentity(opt)
	}
	if err != nil {
		return err
	}
	var src *pasteSource
	if action == "put" {
		var err error
		if src, err = readPasteSource(path, *name, *language, stdin); err != nil {
			return err
		}
	}
	return chatRequestWithContext(ctx, *opt, "PASTE", func(ctx context.Context, client *irc.Client) error {
		switch action {
		case "put":
			info, err := src.upload(ctx, client)
			if err != nil {
				return err
			}
			entries, err := irc.RequestChat(ctx, client, irc.ChatRequest{Action: "pastes", ID: info.ID}, nil)
			if err != nil || len(entries) != 1 {
				return fmt.Errorf("the paste was stored as %s but could not be described: %v", info.ID, err)
			}
			return printPaste(opt.json, entries[0], nil, stdout)
		case "info":
			entries, err := irc.RequestChat(ctx, client, irc.ChatRequest{Action: "pastes", ID: id}, nil)
			if err != nil {
				return err
			}
			if len(entries) != 1 {
				return errors.New("paste is not retained")
			}
			return printPaste(opt.json, entries[0], nil, stdout)
		case "list":
			entries, err := irc.RequestChat(ctx, client, irc.ChatRequest{Action: "pastes", Limit: *limit}, nil)
			if err != nil {
				return err
			}
			if !opt.json && len(entries) == 0 {
				_, err := fmt.Fprintln(stdout, "No pastes.")
				return err
			}
			for _, e := range entries {
				if err := printPaste(opt.json, e, nil, stdout); err != nil {
					return err
				}
			}
			return nil
		}
		meta, content, err := irc.FetchPaste(ctx, client, id, nil)
		if err != nil {
			return err
		}
		if *out != "" {
			if err := os.WriteFile(*out, content, 0o600); err != nil {
				return fmt.Errorf("write %s: %w", *out, err)
			}
			if opt.json {
				return printPaste(true, meta, nil, stdout)
			}
			_, err := fmt.Fprintf(stdout, "%s -> %s\n", protocol.PasteReferenceLine(*meta.Paste), *out)
			return err
		}
		if opt.json {
			return printPaste(true, meta, content, stdout)
		}
		_, err = stdout.Write(content)
		return err
	})
}

// printPaste prints one paste: a JSON row, with its content when given, or
// one line of text.
func printPaste(machine bool, e irc.ChatEntry, content []byte, stdout io.Writer) error {
	if machine {
		row := newPasteRow(e)
		row.Content = string(content)
		return json.NewEncoder(stdout).Encode(row)
	}
	_, err := fmt.Fprintln(stdout, pasteText(e))
	return err
}
