//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/service"
)

// A UART without certificates carries plain author requests in the clear;
// the serial line is the access boundary.
func TestLocalAuthorsOverPlaintextUART(t *testing.T) {
	pty := newPTYBroker(t)
	f := newLocalAuthorFixture(t)
	c, _ := f.config(t, config.Listener{ID: "uart", Protocol: "uart", Address: pty.path, Operations: []core.Operation{core.Send, core.ReceiptGet}})
	process := startUARTProcess(t, currentGatewayBinary(t), c)
	pty.waitReady(t, process)
	stream := framedStream{in: pty.master, out: pty.master}
	exerciseLocalAuthors(t, f, "uart", func(id string, request map[string]any) plainFrameResponse { return stream.call(t, id, request) })
	process.stop(t)
}

// A parent-owned socket pair takes plain author requests.
func TestLocalAuthorsOverSocketPair(t *testing.T) {
	f := newLocalAuthorFixture(t)
	c, _ := f.config(t, config.Listener{ID: "pair", Protocol: "socketpair"})
	parent, child := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.RunSocketPair(ctx, c, child) }()
	defer func() {
		_ = parent.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Error("socket pair service", err)
			}
		case <-time.After(15 * time.Second):
			t.Error("socket pair service did not join")
		}
		cancel()
	}()
	exerciseLocalAuthors(t, f, "pair", func(id string, request map[string]any) plainFrameResponse {
		return (&frameConn{stream: parent}).call(t, id, request)
	})
}

// A directory mailbox without an owner principal takes plain frames named
// by their correlation ID.
func TestLocalAuthorsOverDirectoryMailbox(t *testing.T) {
	f := newLocalAuthorFixture(t)
	c, root := f.config(t, config.Listener{ID: "dir", Protocol: "directory_mailbox", Operations: []core.Operation{core.Send, core.ReceiptGet}})
	directory := filepath.Join(root, "dir")
	for _, path := range []string{directory, filepath.Join(directory, "inbox"), filepath.Join(directory, "processing"), filepath.Join(directory, "results")} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	c.Listeners[0].Address = directory
	startAuthorGateway(t, c)
	exerciseLocalAuthors(t, f, "dir", func(id string, request map[string]any) plainFrameResponse {
		staged := filepath.Join(root, id+".json")
		if err := os.WriteFile(staged, plainFrame(t, id, request), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(staged, filepath.Join(directory, "inbox", id+".json")); err != nil {
			t.Fatal(err)
		}
		result := filepath.Join(directory, "results", id+".json")
		for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
			body, err := os.ReadFile(result)
			if err == nil {
				return decodePlainResponse(t, id, body)
			}
			if time.Now().After(deadline) {
				t.Fatal("no directory mailbox result", err)
			}
		}
	})
}

// A BPv7 mapping without a principal imports a plain send whose request ID
// is the bundle identity; importing the same bundle again is a replay.
func TestLocalAuthorsOverBPv7(t *testing.T) {
	f := newLocalAuthorFixture(t)
	room := core.Target{Kind: "room", Name: "#lobby"}
	c, root := f.config(t, config.Listener{ID: "bp", Protocol: "bpv7", Operations: []core.Operation{core.Send}, BundleProtocol: &config.BundleProtocol{Source: "dtn://producer/", Destination: "dtn://gateway/send", Target: room}})
	path := writeGatewayConfig(t, root, c)
	binary := currentGatewayBinary(t)
	issued := time.Now().Add(-time.Minute).Unix()
	run := func(name string, p bpProducer, wantErr bool) service.OfflineImportReport {
		file := filepath.Join(root, name)
		if err := os.WriteFile(file, p.wire(), 0600); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		cmd := exec.Command(binary, "import-bpv7", "--file", path, "--input", file)
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); (err != nil) != wantErr {
			t.Fatalf("import-bpv7: %v %s", err, stderr.String())
		}
		var report service.OfflineImportReport
		if stdout.Len() > 0 {
			if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
				t.Fatal(err)
			}
		}
		return report
	}
	bundle := func(seq uint64, text, nick, token string) bpProducer {
		p := bpProducer{source: "dtn://producer/", destination: "dtn://gateway/send", creation: uint64(issued-946684800) * 1000, sequence: seq, lifetime: 3600 * 1000}
		body, err := json.Marshal(sendRequest(p.id(), "#lobby", text, nick, token))
		if err != nil {
			t.Fatal(err)
		}
		p.payload = string(body)
		return p
	}
	guest := bundle(1, "guest via bpv7", "bp-guest", "")
	if r := run("guest.bp", guest, false); len(r.Entries) != 1 || r.Entries[0].Author != "bp-guest" || r.Entries[0].Result.State != core.Accepted {
		t.Fatalf("guest import: %+v", r)
	}
	if from := f.posted(t, "guest via bpv7"); from != "bp-guest" {
		t.Fatalf("guest bundle authored as %q", from)
	}
	if r := run("alice.bp", bundle(2, "alice via bpv7", "alice", aliceToken), false); len(r.Entries) != 1 || r.Entries[0].Author != "alice" {
		t.Fatalf("registered import: %+v", r)
	}
	if from := f.posted(t, "alice via bpv7"); from != "alice" {
		t.Fatalf("registered bundle authored as %q", from)
	}
	// The payload must use the bundle identity as its request ID.
	wrong := bundle(3, "wrong identity", "bp-guest", "")
	wrong.payload = string(bytes.Replace([]byte(wrong.payload), []byte(wrong.id()), []byte("not-the-identity"), 1))
	if r := run("wrong.bp", wrong, true); r.Version != 0 {
		t.Fatal("mismatched identity imported", r)
	}
	if r := run("replay.bp", guest, true); len(r.Entries) != 1 || !r.Entries[0].ReplayRejected {
		t.Fatalf("replay: %+v", r)
	}
	if n := f.nativeCount(t, "guest via bpv7"); n != 1 {
		t.Fatalf("replayed BPv7 bundle posted %d times", n)
	}
}

// An append log without an owner principal takes plain frames, one per
// line; the bound author posts requests that name none.
func TestLocalAuthorsOverAppendLog(t *testing.T) {
	f := newLocalAuthorFixture(t)
	c, root := f.config(t, config.Listener{ID: "log", Protocol: "append_log", Operations: []core.Operation{core.Send, core.ReceiptGet}, BoundAuthor: &config.BoundAuthor{Nick: "log-robot"}})
	directory := filepath.Join(root, "log")
	if err := os.MkdirAll(filepath.Join(directory, "logs"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	logFile := filepath.Join(directory, "logs", "00000000000000000001.jsonl")
	if err := os.WriteFile(logFile, nil, 0600); err != nil {
		t.Fatal(err)
	}
	c.Listeners[0].Address = directory
	startAuthorGateway(t, c)
	call := func(id string, request map[string]any) plainFrameResponse {
		file, err := os.OpenFile(logFile, os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		_, err = file.Write(append(plainFrame(t, id, request), '\n'))
		if err = errors.Join(err, file.Close()); err != nil {
			t.Fatal(err)
		}
		result := filepath.Join(directory, "response.json")
		for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
			body, err := os.ReadFile(result)
			if err == nil {
				if err := os.Remove(result); err != nil {
					t.Fatal(err)
				}
				return decodePlainResponse(t, id, body)
			}
			if time.Now().After(deadline) {
				t.Fatal("no append log result", err)
			}
		}
	}
	exerciseLocalAuthors(t, f, "log", call)
	if r := call("log-4", sendRequest("log-bound-post", "#lobby", "bound via log", "", "")); r.Error != nil {
		t.Fatalf("bound author: %+v", r.Error)
	}
	if from := f.posted(t, "bound via log"); from != "log-robot" {
		t.Fatalf("bound log post authored as %q", from)
	}
}
