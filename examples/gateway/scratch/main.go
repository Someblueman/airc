// Scratch owns a temporary native server, gateway and independent observer.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

type child struct {
	cmd  *exec.Cmd
	done chan struct{}
	err  error
}

func start(ctx context.Context, binary string, args []string, root string, native bool) (*child, string, error) {
	p := &child{cmd: exec.CommandContext(ctx, binary, args...), done: make(chan struct{})}
	p.cmd.Env = append(os.Environ(), "HOME="+root, "XDG_CONFIG_HOME="+root, "XDG_STATE_HOME="+root, "AIRC_STATE_DIR="+root)
	var stream io.ReadCloser
	var err error
	if native {
		stream, err = p.cmd.StderrPipe()
		p.cmd.Stdout = io.Discard
	} else {
		stream, err = p.cmd.StdoutPipe()
		p.cmd.Stderr = os.Stderr
	}
	if err != nil {
		return nil, "", err
	}
	if err := p.cmd.Start(); err != nil {
		return nil, "", err
	}
	ready := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stream)
		announced := false
		for scanner.Scan() {
			var event struct {
				Message   string `json:"msg"`
				Address   string `json:"address"`
				State     string `json:"state"`
				Listeners []struct {
					Address string `json:"address"`
				} `json:"listeners"`
			}
			if announced || json.Unmarshal(scanner.Bytes(), &event) != nil {
				continue
			}
			if native && event.Message == "server_started" {
				ready <- event.Address
				announced = true
			} else if !native && event.State == "ready" && len(event.Listeners) == 1 {
				ready <- event.Listeners[0].Address
				announced = true
			}
		}
		p.err = p.cmd.Wait()
		close(p.done)
	}()
	select {
	case address := <-ready:
		return p, address, nil
	case <-p.done:
		return nil, "", fmt.Errorf("child exited before ready: %v", p.err)
	case <-time.After(8 * time.Second):
		return nil, "", errors.Join(errors.New("child readiness timed out"), p.stop())
	case <-ctx.Done():
		return nil, "", errors.Join(ctx.Err(), p.stop())
	}
}

func (p *child) stop() error {
	select {
	case <-p.done:
		return p.err
	default:
	}
	if err := p.cmd.Process.Signal(os.Interrupt); err != nil {
		return err
	}
	select {
	case <-p.done:
		return p.err
	case <-time.After(7 * time.Second):
		_ = p.cmd.Process.Kill()
		<-p.done
		return errors.New("child required forced termination")
	}
}

func call(ctx context.Context, client *http.Client, address, token, method, path string, body any, result any) error {
	var encoded []byte
	var err error
	if body != nil {
		encoded, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	r, err := http.NewRequestWithContext(ctx, method, "http://"+address+path, bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	r.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(r)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusAccepted {
		return fmt.Errorf("%s: HTTP %d", path, response.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(result)
}

func run(nativeBinary, gatewayBinary string) (err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	root, err := os.MkdirTemp("/tmp", "airc-demo-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	socket := filepath.Join(root, "native.sock")
	native, address, err := start(ctx, nativeBinary, []string{"--unix", socket, "--history", "32", "--history-file", filepath.Join(root, "history.jsonl"), "--sync", "fsync", "--log-format", "json"}, root, true)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, native.stop()) }()
	if address != socket {
		return errors.New("native server announced another address")
	}
	accountFile, bearerFile := filepath.Join(root, "account.token"), filepath.Join(root, "bearer.token")
	for _, file := range []string{accountFile, bearerFile, filepath.Join(root, "cursor.key")} {
		if err := auth.CreateCredential(file); err != nil {
			return err
		}
	}
	account, err := auth.ReadToken(accountFile)
	if err != nil {
		return err
	}
	provision, err := irc.DialContext(ctx, irc.Config{Network: "unix", Addr: socket, Nick: "scratch-author", IdentityToken: account, CreateAccount: true, Ephemeral: true, ReadTimeout: 5 * time.Second, WriteTimeout: time.Second})
	if err != nil {
		return err
	}
	if err := provision.Close(); err != nil {
		return err
	}
	observer, err := irc.DialContext(ctx, irc.Config{Network: "unix", Addr: socket, Nick: "scratch-observer", ReadTimeout: 30 * time.Second, WriteTimeout: time.Second})
	if err != nil {
		return err
	}
	defer observer.Close()
	room := core.Target{Kind: "room", Name: "#scratch"}
	if err := observer.Join(room.Name); err != nil {
		return err
	}
	joined := false
	for !joined {
		select {
		case event, ok := <-observer.Events():
			if !ok {
				return errors.New("observer closed before joining")
			}
			if join, ok := event.(*irc.JoinEvent); ok && join.Agent == "scratch-observer" {
				joined = true
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	c := config.Defaults()
	c.Version, c.Realm, c.StateDir = 1, "scratch-demo", filepath.Join(root, "journal")
	c.CursorKeyFile = filepath.Join(root, "cursor.key")
	if err := os.Mkdir(c.StateDir, 0700); err != nil {
		return err
	}
	ops := []core.Operation{core.Send, core.ReceiptGet, core.HistoryRead}
	c.Bindings = []config.Binding{{ID: "native", Network: "unix", Address: socket, Account: "scratch-author", AccountTokenFile: accountFile}}
	c.Principals = []config.Principal{{ID: "client", Binding: "native", BearerTokenFile: bearerFile, Operations: ops, Targets: []core.Target{room}}}
	c.Listeners = []config.Listener{{ID: "http", Protocol: "http", Enabled: true, Address: "127.0.0.1:0", Operations: ops}}
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	file := filepath.Join(root, "gateway.json")
	if err := os.WriteFile(file, data, 0600); err != nil {
		return err
	}
	gateway, address, err := start(ctx, gatewayBinary, []string{"serve", "--file", file}, root, false)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, gateway.stop()) }()
	token, err := auth.ReadToken(bearerFile)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	request := core.Request{Version: 1, RequestID: "scratch-send", Operation: core.Send, Target: &room, Message: "scratch whole body\nhello 🙂"}
	var accepted core.Result
	if err := call(ctx, client, address, token, http.MethodPost, "/v1/messages", request, &accepted); err != nil {
		return err
	}
	if !accepted.Durable {
		return errors.New("submission was not durably admitted")
	}
	deadline := time.Now().Add(5 * time.Second)
	for accepted.State != core.Accepted {
		if time.Now().After(deadline) {
			return errors.New("native acceptance timed out")
		}
		if err := call(ctx, client, address, token, http.MethodGet, "/v1/requests/"+request.RequestID, nil, &accepted); err != nil {
			return err
		}
		select {
		case <-time.After(20 * time.Millisecond):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	var seen *irc.MessageEvent
	for seen == nil {
		select {
		case event, ok := <-observer.Events():
			if !ok {
				return errors.New("observer closed before acceptance")
			}
			if message, ok := event.(*irc.MessageEvent); ok && message.ID == accepted.MessageID {
				seen = message
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if seen.From != "scratch-author" || seen.Target != room.Name || seen.Message != request.Message {
		return errors.New("independent observer disagrees with accepted intent")
	}
	path := "/v1/history?kind=room&target=" + url.QueryEscape(room.Name) + "&limit=1"
	var page backend.HistoryPage
	if err := call(ctx, client, address, token, http.MethodGet, path, nil, &page); err != nil {
		return err
	}
	if len(page.Messages) != 1 || page.Messages[0].ID != seen.ID || page.Messages[0].Message != seen.Message || page.Messages[0].From != seen.From || page.Messages[0].Target != seen.Target || page.Cursor == "" || page.Status != "ok" || page.Gap {
		return errors.New("bounded read disagrees with native observer")
	}
	cursor := page.Cursor
	if err := call(ctx, client, address, token, http.MethodGet, path+"&cursor="+url.QueryEscape(cursor), nil, &page); err != nil {
		return err
	}
	if len(page.Messages) != 0 || page.Status != "ok" || page.Cursor == "" || page.Gap {
		return errors.New("caught-up cursor replayed or lost position")
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"author": seen.From, "target": seen.Target, "body": seen.Message, "message_id": seen.ID, "first_page_messages": 1, "resumed_page_messages": 0})
}

func main() {
	native := flag.String("native", "", "explicit native server executable")
	gateway := flag.String("gateway", "", "explicit gateway executable")
	flag.Parse()
	if *native == "" || *gateway == "" || flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "require --native PATH --gateway PATH")
		os.Exit(2)
	}
	if err := run(*native, *gateway); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
