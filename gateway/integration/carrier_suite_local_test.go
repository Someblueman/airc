//go:build darwin || linux

package gateway_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/adapter"
	"github.com/Someblueman/airc/gateway/internal/backend"
	"github.com/Someblueman/airc/gateway/internal/config"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/internal/testfs"
	"github.com/Someblueman/airc/pkg/irc"
)

// The local carriers an in-process gateway serves join the cross-carrier
// suite here: a Unix datagram socket, a directory mailbox and an append log
// (and, in its own file, Unix sequenced packets). Carriers that run as their
// own process (stdio, socket pair, inherited stream, file mailbox) own the
// journal while they run, so they cannot share the suite's gateway; they run
// the shared scenarios in TestLocalProcessCarriers instead.
var _ = registerSuiteCarriers(
	suiteCarrier{name: "unixgram", listener: config.Listener{Protocol: "unixgram"}, configure: prepareLocalSocket, dial: dialUnixgramSuite, results: true, reads: true, wire: unixSuiteWire},
	suiteCarrier{name: "dirmailbox", listener: config.Listener{Protocol: "directory_mailbox", Operations: mailboxOperations}, configure: prepareDirectoryMailbox, dial: dialDirectoryMailboxSuite, results: true, wire: mailboxHistoryWire},
	suiteCarrier{name: "appendlog", listener: config.Listener{Protocol: "append_log", Operations: mailboxOperations, BoundAuthor: &config.BoundAuthor{Nick: "log-robot"}}, configure: prepareAppendLog, dial: dialAppendLogSuite, results: true, wire: appendLogSuiteWire},
)

// mailboxOperations are all a mailbox carries: no search, context or watch.
var mailboxOperations = []core.Operation{core.Send, core.ReceiptGet, core.HistoryRead}

// localSuiteClient speaks correlated plain frames over one local carrier:
// write delivers a frame and read returns the next reply, or nil once the
// carrier has closed the connection. A packet carrier drops a frame it
// cannot decode without an answer; its read returns nil when nothing
// arrives within wait. A carrier that answers every producer on one shared
// channel (a reply queue, the append log's response slot) can return the
// reply to an earlier unread request; call skips it.
type localSuiteClient struct {
	write func(t *testing.T, id string, frame []byte)
	read  func(t *testing.T, id string, wait time.Duration) []byte
	done  func()
}

var localSuiteFrames atomic.Int64

// localSuiteFrame embeds request verbatim, so it may be invalid JSON.
func localSuiteFrame(id string, request []byte, stream bool) []byte {
	return fmt.Appendf(nil, `{"version":1,"correlation_id":%q,"profile":"plain","stream":%t,"request":%s}`, id, stream, request)
}

// localSuiteID is a correlation ID unique across clients, which share a
// mailbox's result names.
func localSuiteID(kind string) string { return fmt.Sprint(kind, "-", localSuiteFrames.Add(1)) }

func (c *localSuiteClient) call(t *testing.T, request []byte) suiteReply {
	t.Helper()
	id := localSuiteID("call")
	frame := localSuiteFrame(id, request, false)
	c.write(t, id, frame)
	// Fail fast on silence: the fixture's IRC clients time out after 10s idle.
	wait := 10 * time.Second
	if !json.Valid(frame) {
		wait = 2 * time.Second
	}
	for {
		body := c.read(t, id, wait)
		if body == nil {
			return suiteReply{closed: true}
		}
		var response adapter.FrameResponse
		if err := json.Unmarshal(body, &response); err != nil {
			t.Fatalf("reply %s: %v", body, err)
		}
		// An undecodable frame is refused without a correlation ID.
		if response.CorrelationID != id && (response.CorrelationID != "" || response.Error == nil) {
			t.Logf("skipped the reply to %s", response.CorrelationID)
			continue
		}
		if response.Error != nil {
			return suiteReply{failure: response.Error}
		}
		return suiteReply{result: response.Result}
	}
}

func (c *localSuiteClient) send(t *testing.T, request []byte) {
	t.Helper()
	id := localSuiteID("send")
	c.write(t, id, localSuiteFrame(id, request, false))
}

func (c *localSuiteClient) close() {
	if c.done != nil {
		c.done()
	}
}

// localSuiteDir is a short private directory: Unix socket paths are
// limited, and mailbox roots must be owner-only.
func localSuiteDir(t *testing.T) string {
	t.Helper()
	root, err := testfs.MkdirTemp("agw-lc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	if root, err = filepath.EvalSymlinks(root); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	return root
}

func prepareLocalSocket(t *testing.T, l *config.Listener) {
	l.Address = filepath.Join(localSuiteDir(t), l.ID+".sock")
}

// localSuiteDatagrams splits a frame into AGD1 fragments sent last first,
// with the first sent repeated: the carrier reassembles out of order and
// ignores a duplicate.
func localSuiteDatagrams(t *testing.T, frame []byte) [][]byte {
	t.Helper()
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		t.Fatal(err)
	}
	fragments := webTransportDatagrams(frame, id)
	slices.Reverse(fragments)
	return append([][]byte{fragments[0]}, fragments...)
}

// readLocalDatagrams reassembles one AGD1 reply, or returns nil when no
// fragment arrives within wait.
func readLocalDatagrams(t *testing.T, conn datagramResponseReader, wait time.Duration) []byte {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(wait)); err != nil {
		t.Fatal(err)
	}
	first := make([]byte, 1001)
	n, err := conn.Read(first)
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() || errors.Is(err, context.DeadlineExceeded) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return readAGD1Payload(t, &primedDatagrams{datagramResponseReader: conn, first: first[:n]}, time.Now().Add(10*time.Second))
}

// primedDatagrams returns a datagram already read before reading on.
type primedDatagrams struct {
	datagramResponseReader
	first []byte
}

func (r *primedDatagrams) Read(body []byte) (int, error) {
	if r.first == nil {
		return r.datagramResponseReader.Read(body)
	}
	n := copy(body, r.first)
	r.first = nil
	return n, nil
}

// dialUnixgramSuite binds a client socket of its own: the gateway answers
// each sender at its bound address.
func dialUnixgramSuite(t *testing.T, d suiteDial) (suiteClient, error) {
	conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: filepath.Join(localSuiteDir(t), "client.sock"), Net: "unixgram"})
	if err != nil {
		return nil, err
	}
	server := &net.UnixAddr{Name: d.address, Net: "unixgram"}
	return &localSuiteClient{
		write: func(t *testing.T, _ string, frame []byte) {
			t.Helper()
			for _, fragment := range localSuiteDatagrams(t, frame) {
				if _, err := conn.WriteTo(fragment, server); err != nil {
					t.Fatal(err)
				}
			}
		},
		read: func(t *testing.T, _ string, wait time.Duration) []byte {
			t.Helper()
			return readLocalDatagrams(t, conn, wait)
		},
		done: func() { _ = conn.Close() },
	}, nil
}

func prepareDirectoryMailbox(t *testing.T, l *config.Listener) {
	t.Helper()
	l.Address = localSuiteDir(t)
	for _, name := range []string{"inbox", "processing", "results"} {
		if err := os.Mkdir(filepath.Join(l.Address, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
}

// dialDirectoryMailboxSuite publishes each frame into inbox/ by rename,
// named by its correlation ID, and reads results/ under the same name.
func dialDirectoryMailboxSuite(_ *testing.T, d suiteDial) (suiteClient, error) {
	directory := d.address
	return &localSuiteClient{
		write: func(t *testing.T, id string, frame []byte) {
			t.Helper()
			staged := filepath.Join(directory, id+".tmp")
			if err := os.WriteFile(staged, frame, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(staged, filepath.Join(directory, "inbox", id+".json")); err != nil {
				t.Fatal(err)
			}
		},
		read: func(t *testing.T, id string, _ time.Duration) []byte {
			t.Helper()
			return takeLocalFile(t, filepath.Join(directory, "results", id+".json"))
		},
	}, nil
}

// takeLocalFile waits for a published result and consumes it.
func takeLocalFile(t *testing.T, path string) []byte {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		body, err := os.ReadFile(path)
		if err == nil {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			return body
		}
		if !errors.Is(err, os.ErrNotExist) || time.Now().After(deadline) {
			t.Fatal("no result", path, err)
		}
	}
}

func prepareAppendLog(t *testing.T, l *config.Listener) {
	t.Helper()
	l.Address = localSuiteDir(t)
	if err := os.Mkdir(filepath.Join(l.Address, "logs"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(appendLogSuiteFile(l.Address), nil, 0600); err != nil {
		t.Fatal(err)
	}
}

func appendLogSuiteFile(directory string) string {
	return filepath.Join(directory, "logs", "00000000000000000001.jsonl")
}

// dialAppendLogSuite appends one frame per line; the log answers in its one
// response slot, which the reader consumes to release the next frame.
func dialAppendLogSuite(_ *testing.T, d suiteDial) (suiteClient, error) {
	directory := d.address
	return &localSuiteClient{
		write: func(t *testing.T, _ string, frame []byte) {
			t.Helper()
			file, err := os.OpenFile(appendLogSuiteFile(directory), os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				t.Fatal(err)
			}
			_, err = file.Write(append(frame, '\n'))
			if err = errors.Join(err, file.Close()); err != nil {
				t.Fatal(err)
			}
		},
		read: func(t *testing.T, _ string, _ time.Duration) []byte {
			t.Helper()
			return takeLocalFile(t, filepath.Join(directory, "response.json"))
		},
	}, nil
}

// appendLogSuiteWire: a frame that names no author runs as the listener's
// bound author.
func appendLogSuiteWire(t *testing.T, e *suiteEnv) {
	t.Run("bound_author", func(t *testing.T) {
		id, text := e.id("bound"), e.text("bound")
		e.admitted(t, e.dial(t).call(t, sendBody(t, id, "#wire", text, "", "")), id)
		e.posted(t, id, "#wire", text, "log-robot", "")
	})
	mailboxHistoryWire(t, e)
}

// mailboxHistoryWire is bounded_reads for a mailbox, which reads history
// only.
func mailboxHistoryWire(t *testing.T, e *suiteEnv) {
	t.Run("bounded_history", func(t *testing.T) {
		for i := range 2 {
			id := e.id("seed")
			plainHTTP(t, http.MethodPost, e.receipts+"/v1/messages", sendBody(t, id, "#wire", fmt.Sprintf("history-seed %d\n", i)+strings.Repeat("世界 🙂", 200), "seeder", ""), nil, nil)
			if r := e.receipt(t, id, "seeder", ""); r.State != core.Accepted {
				t.Fatalf("seed %d: %+v", i, r)
			}
		}
		client, reader := e.dial(t), e.guest("reader")
		checkHistoryPages(t, nativeWebTransportMessages(t, e.socket, "#wire"), func(fields map[string]any) *backend.HistoryPage {
			fields["request_id"] = e.id("page")
			if r := client.call(t, mustBody(t, plainRequest(fields, reader, ""))); r.result != nil {
				return r.result.Page
			}
			return nil
		})
	})
}

// checkHistoryPages pages a room's history one message at a time to a
// caught-up cursor that stays caught up, each record equal to what aircd
// itself holds (native).
func checkHistoryPages(t *testing.T, native []*irc.MessageMetadata, read func(fields map[string]any) *backend.HistoryPage) {
	t.Helper()
	if len(native) == 0 {
		t.Fatal("no history to page")
	}
	cursor := ""
	for i := range len(native) + 2 {
		fields := map[string]any{"operation": core.HistoryRead, "target": map[string]any{"kind": "room", "name": native[0].Target}, "limit": 1}
		if cursor == "" {
			fields["start"] = "retained"
		} else {
			fields["cursor"] = cursor
		}
		page, status, want := read(fields), "ok", []*irc.MessageMetadata{}
		if page == nil {
			t.Fatalf("no page %d", i)
		}
		if i < len(native) {
			want = native[i : i+1]
		}
		if i < len(native)-1 {
			status = "more"
		}
		if page.Gap || page.Status != status || page.Cursor == "" || len(page.Messages) != len(want) || len(want) == 1 && !reflect.DeepEqual(page.Messages[0], want[0]) {
			t.Fatalf("page %d: %+v, aircd holds %+v", i, page, want)
		}
		cursor = page.Cursor
	}
}
