//go:build darwin || linux

package gateway_test

// The local carriers that run as their own gateway process join the
// cross-carrier suite here: a child on its parent's pipe (serve-stdio),
// socket pair (serve-socketpair) or inherited Unix stream (serve-inherited),
// and the one-shot file mailbox (mailbox-once). Each child owns the journal
// while it runs, so the suite runs one child at a time (suiteCarrier.process)
// and reads receipts over the carrier. The parent-owned endpoint or the
// private directory is the access gate, so there is no listener password or
// TLS twin. Behaviour particular to one carrier (signals, crash recovery,
// partial frames, file modes) keeps its focused test in the carrier's own
// files.

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/config"
	"github.com/Someblueman/airc/gateway/internal/core"
)

var _ = registerSuiteCarriers(
	suiteCarrier{name: "stdio", listener: config.Listener{Protocol: "stdio"}, process: startStdioChild, results: true, reads: true},
	suiteCarrier{name: "socketpair", listener: config.Listener{Protocol: "socketpair"}, process: startParentChild("socketpair"), results: true, reads: true},
	suiteCarrier{name: "inherited", listener: config.Listener{Protocol: "inherited"}, process: startParentChild("inherited"), results: true, reads: true},
	suiteCarrier{name: "filemailbox", listener: config.Listener{Protocol: "file_mailbox", Operations: mailboxOperations}, configure: prepareFileMailbox, process: startFileMailbox, results: true, wire: fileMailboxSuiteWire},
)

// childStream exchanges length-prefixed correlated plain frames with a child
// on a parent-owned stream. Its close runs end, which ends the stream (EOF)
// and waits for the child's clean exit.
func childStream(in io.Writer, out interface {
	io.Reader
	SetReadDeadline(time.Time) error
}, end func()) suiteClient {
	return &localSuiteClient{
		write: func(t *testing.T, _ string, frame []byte) {
			t.Helper()
			if _, err := in.Write(append(binary.BigEndian.AppendUint32(nil, uint32(len(frame))), frame...)); err != nil {
				t.Fatal(err)
			}
		},
		read: func(t *testing.T, _ string, wait time.Duration) []byte {
			t.Helper()
			if err := out.SetReadDeadline(time.Now().Add(wait)); err != nil {
				t.Fatal(err)
			}
			var header [4]byte
			if _, err := io.ReadFull(out, header[:]); errors.Is(err, io.EOF) {
				return nil
			} else if err != nil {
				t.Fatal(err)
			}
			body := make([]byte, binary.BigEndian.Uint32(header[:]))
			if _, err := io.ReadFull(out, body); err != nil {
				t.Fatal(err)
			}
			return body
		},
		done: end,
	}
}

// A serve-stdio child reads frames from its parent's pipe and exits at EOF.
func startStdioChild(t *testing.T, e *suiteEnv) suiteClient {
	t.Helper()
	p := startStdio(t, currentGatewayBinary(t), e.config)
	return childStream(p.stdin, p.stdout.(*os.File), func() {
		_ = p.stdin.Close()
		if err := p.wait(); err != nil {
			t.Errorf("serve-stdio at EOF: %v %s", err, p.stderr.String())
		}
	})
}

// serve-socketpair and serve-inherited take a stream from their parent as
// file descriptor 3 and exit at EOF.
func startParentChild(protocol string) func(*testing.T, *suiteEnv) suiteClient {
	return func(t *testing.T, e *suiteEnv) suiteClient {
		t.Helper()
		conn, stop, _ := startParentFD(t, currentGatewayBinary(t), e.config, protocol, 0, false)
		if address, ok := conn.RemoteAddr().(*net.UnixAddr); protocol == "inherited" && (!ok || address.Name == "") {
			t.Fatal("the child did not inherit a named Unix endpoint", conn.RemoteAddr())
		}
		return childStream(conn, conn, func() {
			_ = conn.(*net.UnixConn).CloseWrite()
			stop()
		})
	}
}

// prepareFileMailbox gives the mailbox its private directory and its bound
// author, alice, whose token stays in an owner-only file.
func prepareFileMailbox(t *testing.T, l *config.Listener) {
	t.Helper()
	root := localSuiteDir(t)
	l.Address = filepath.Join(root, "mailbox")
	if err := os.Mkdir(l.Address, 0700); err != nil {
		t.Fatal(err)
	}
	tokenFile := filepath.Join(root, "alice.token")
	if err := os.WriteFile(tokenFile, []byte(aliceToken+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	l.BoundAuthor = &config.BoundAuthor{Nick: "alice", AccountTokenFile: tokenFile}
}

// startFileMailbox: mailbox-once runs on one request in the private
// directory per frame written, and has exited by the time its response is
// read, so there is no child to end.
func startFileMailbox(t *testing.T, e *suiteEnv) suiteClient {
	t.Helper()
	binary := currentGatewayBinary(t)
	path := filepath.Join(filepath.Dir(e.config.StateDir), "gateway.json")
	data, err := json.Marshal(e.config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	directory := e.config.Listeners[0].Address
	request, response := filepath.Join(directory, "request.json"), filepath.Join(directory, "response.json")
	var runErr error
	return &localSuiteClient{
		write: func(t *testing.T, _ string, frame []byte) {
			t.Helper()
			// A response nothing read (send) would answer the next request.
			if err := os.Remove(response); err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
			if err := os.WriteFile(request, frame, 0600); err != nil {
				t.Fatal(err)
			}
			var output []byte
			if output, runErr = exec.Command(binary, "mailbox-once", "--file", path).CombinedOutput(); runErr != nil {
				t.Logf("mailbox-once: %v %s", runErr, output)
			}
		},
		read: func(t *testing.T, _ string, _ time.Duration) []byte {
			t.Helper()
			body, err := os.ReadFile(response)
			if errors.Is(err, os.ErrNotExist) && runErr != nil {
				// The mailbox refused its input without a response.
				return nil
			}
			if err != nil || runErr != nil {
				t.Fatalf("mailbox-once: %v %v", runErr, err)
			}
			if err := os.Remove(response); err != nil {
				t.Fatal(err)
			}
			return body
		},
	}
}

// fileMailboxSuiteWire: a request without an author runs as the listener's
// bound author, whose token stays in an owner-only file instead of in every
// request; and the mailbox pages history like the other mailboxes.
func fileMailboxSuiteWire(t *testing.T, e *suiteEnv) {
	t.Run("bound_author", func(t *testing.T) {
		id, text := e.id("bound"), e.text("bound")
		e.admitted(t, e.dial(t).call(t, sendBody(t, id, "#wire", text, "", "")), id)
		r := e.receipt(t, id, "", "")
		seen := e.observed.wait(t, text)
		if r.State != core.Accepted || r.SendAttempts != 1 || seen.ID != r.MessageID || seen.From != "alice" || seen.Target != "#wire" {
			t.Fatalf("%s: AIRC posted %+v, receipt %+v", id, seen, r)
		}
	})
	mailboxHistoryWire(t, e)
}
