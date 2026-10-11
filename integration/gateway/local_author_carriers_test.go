//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"crypto/rand"
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

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/service"
	"github.com/Someblueman/airc/internal/testfs"
	"github.com/Someblueman/airc/pkg/irc"
	"golang.org/x/sys/unix"
)

// localAuthorFixture is a current-tree aircd with a provisioned account
// "alice" and a watcher in #lobby, for the local and offline carriers.
type localAuthorFixture struct {
	socket, gatewayToken string
	watcher              *irc.Client
}

func newLocalAuthorFixture(t *testing.T) *localAuthorFixture {
	t.Helper()
	socket, gatewayToken := currentNative(t)
	provision := dial(t, socket, "alice", aliceToken, true, true)
	_ = provision.Close()
	watcher := dial(t, socket, "watcher", "", false, false)
	if err := watcher.Join("#lobby"); err != nil {
		t.Fatal(err)
	}
	next(t, watcher, func(e *irc.JoinEvent) bool { return e.Agent == "watcher" })
	return &localAuthorFixture{socket: socket, gatewayToken: gatewayToken, watcher: watcher}
}

// config is a gateway with no principals, bindings or keys: one upstream
// and one enabled local listener, which keeps its own address.
func (f *localAuthorFixture) config(t *testing.T, l config.Listener) (config.Config, string) {
	t.Helper()
	root, err := testfs.MkdirTemp("agw-local-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	if root, err = filepath.EvalSymlinks(root); err != nil {
		t.Fatal(err)
	}
	c := config.Defaults()
	c.Version, c.Realm, c.StateDir = 1, "local-author", filepath.Join(root, "state")
	if err := os.Mkdir(c.StateDir, 0700); err != nil {
		t.Fatal(err)
	}
	c.CursorKeyFile = filepath.Join(root, "cursor.key")
	if err := auth.CreateCredential(c.CursorKeyFile); err != nil {
		t.Fatal(err)
	}
	c.Upstream = &config.Upstream{Network: "unix", Address: f.socket, GatewayTokenFile: f.gatewayToken}
	l.Enabled = true
	if l.Operations == nil {
		l.Operations = []core.Operation{}
	}
	c.Listeners = []config.Listener{l}
	return c, root
}

func writeGatewayConfig(t *testing.T, root string, c config.Config) string {
	t.Helper()
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "gateway.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

// posted waits for the watcher to see text and returns who posted it.
func (f *localAuthorFixture) posted(t *testing.T, text string) string {
	t.Helper()
	return next(t, f.watcher, func(e *irc.MessageEvent) bool { return e.Message == text }).From
}

// nativeCount is an independent observer: a fresh AIRC client reads #lobby
// history from aircd itself and counts messages with this text.
func (f *localAuthorFixture) nativeCount(t *testing.T, text string) int {
	t.Helper()
	observer := dial(t, f.socket, "history-observer", "", false, false)
	defer observer.Close()
	if err := observer.History("#lobby", 200); err != nil {
		t.Fatal(err)
	}
	count := 0
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event, ok := <-observer.Events():
			if !ok {
				t.Fatal("history observer disconnected")
			}
			switch e := event.(type) {
			case *irc.HistoryEvent:
				if e.Message == text {
					count++
				}
			case *irc.EndOfHistoryEvent:
				return count
			}
		case <-timer.C:
			t.Fatal("history reply timed out")
		}
	}
}

func plainFrame(t *testing.T, id string, request map[string]any) []byte {
	t.Helper()
	frame, err := json.Marshal(map[string]any{"version": 1, "correlation_id": id, "profile": "plain", "request": request})
	if err != nil {
		t.Fatal(err)
	}
	return frame
}

func decodePlainResponse(t *testing.T, id string, body []byte) plainFrameResponse {
	t.Helper()
	var response plainFrameResponse
	if err := json.Unmarshal(body, &response); err != nil || response.CorrelationID != id {
		t.Fatalf("plain response %s: %v", body, err)
	}
	return response
}

// exerciseLocalAuthors runs the common author checks over one carrier: a
// guest post with no token, a registered post with alice's token, and a
// replay of the guest's request ID that must not post twice.
func exerciseLocalAuthors(t *testing.T, f *localAuthorFixture, carrier string, call func(id string, request map[string]any) plainFrameResponse) {
	t.Helper()
	guest := carrier + "-guest"
	guestText := "guest via " + carrier
	guestRequest := sendRequest(carrier+"-guest-post", "#lobby", guestText, guest, "")
	if r := call(carrier+"-1", guestRequest); r.Error != nil || r.Result == nil || r.Result.Result == nil || r.Result.Result.RequestID != carrier+"-guest-post" {
		t.Fatalf("%s guest post: %+v %+v", carrier, r, r.Error)
	}
	if from := f.posted(t, guestText); from != guest {
		t.Fatalf("%s guest post authored as %q", carrier, from)
	}
	aliceText := "alice via " + carrier
	if r := call(carrier+"-2", sendRequest(carrier+"-alice-post", "#lobby", aliceText, "alice", aliceToken)); r.Error != nil || r.Result == nil || r.Result.Result == nil {
		t.Fatalf("%s registered post: %+v %+v", carrier, r, r.Error)
	}
	if from := f.posted(t, aliceText); from != "alice" {
		t.Fatalf("%s registered post authored as %q", carrier, from)
	}
	if r := call(carrier+"-3", guestRequest); r.Error != nil || r.Result == nil || r.Result.Result == nil || r.Result.Result.RequestID != carrier+"-guest-post" {
		t.Fatalf("%s replay: %+v %+v", carrier, r, r.Error)
	}
	if n := f.nativeCount(t, guestText); n != 1 {
		t.Fatalf("%s replay posted %d messages", carrier, n)
	}
}

// framedStream writes length-prefixed frames and reads replies.
type framedStream struct {
	in  io.Writer
	out *os.File
}

func (s framedStream) call(t *testing.T, id string, request map[string]any) plainFrameResponse {
	t.Helper()
	frame := plainFrame(t, id, request)
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(frame)))
	if _, err := s.in.Write(append(header[:], frame...)); err != nil {
		t.Fatal(err)
	}
	if err := s.out.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(s.out, header[:]); err != nil {
		t.Fatal(err)
	}
	body := make([]byte, binary.BigEndian.Uint32(header[:]))
	if _, err := io.ReadFull(s.out, body); err != nil {
		t.Fatal(err)
	}
	return decodePlainResponse(t, id, body)
}

// A serve-stdio child takes plain author requests from its parent with no
// principal or envelope: the parent-owned pipe is the access gate.
func TestLocalAuthorsOverStdio(t *testing.T) {
	f := newLocalAuthorFixture(t)
	c, root := f.config(t, config.Listener{ID: "stdio", Protocol: "stdio"})
	path := writeGatewayConfig(t, root, c)
	cmd := exec.Command(currentGatewayBinary(t), "serve-stdio", "--file", path)
	stdinReader, stdin, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, stdoutWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdinReader, stdoutWriter, &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = stdinReader.Close()
	_ = stdoutWriter.Close()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	defer func() {
		_ = stdin.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("serve-stdio exit: %v %s", err, stderr.String())
			}
		case <-time.After(15 * time.Second):
			_ = cmd.Process.Kill()
			t.Error("serve-stdio did not exit after EOF")
		}
		_ = stdout.Close()
	}()
	stream := framedStream{in: stdin, out: stdout}
	exerciseLocalAuthors(t, f, "stdio", func(id string, request map[string]any) plainFrameResponse { return stream.call(t, id, request) })
}

// A FIFO pair without a sender principal takes plain author requests.
func TestLocalAuthorsOverFIFO(t *testing.T) {
	f := newLocalAuthorFixture(t)
	c, root := f.config(t, config.Listener{ID: "fifo", Protocol: "fifo", Operations: []core.Operation{core.Send, core.ReceiptGet}})
	pair := filepath.Join(root, "fifo")
	if err := os.Mkdir(pair, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"input.fifo", "output.fifo"} {
		if err := unix.Mkfifo(filepath.Join(pair, name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	c.Listeners[0].Address = pair
	path := writeGatewayConfig(t, root, c)
	output, err := os.OpenFile(filepath.Join(pair, "output.fifo"), os.O_RDONLY|unix.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	cmd := exec.Command(currentGatewayBinary(t), "serve-fifo", "--file", path)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var input *os.File
	for deadline := time.Now().Add(5 * time.Second); ; {
		input, err = os.OpenFile(filepath.Join(pair, "input.fifo"), os.O_WRONLY|unix.O_NONBLOCK, 0)
		if err == nil {
			break
		}
		if !errors.Is(err, unix.ENXIO) || time.Now().After(deadline) {
			t.Fatal("FIFO child never opened its input", err, stderr.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	defer func() {
		_ = input.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("serve-fifo exit: %v %s", err, stderr.String())
			}
		case <-time.After(15 * time.Second):
			_ = cmd.Process.Kill()
			t.Error("serve-fifo did not exit after EOF")
		}
	}()
	stream := framedStream{in: input, out: output}
	exerciseLocalAuthors(t, f, "fifo", func(id string, request map[string]any) plainFrameResponse { return stream.call(t, id, request) })
}

// A Unix datagram listener without return routes takes plain author
// requests in AGD1 datagrams and answers each sender at its bound address.
func TestLocalAuthorsOverUnixDatagram(t *testing.T) {
	f := newLocalAuthorFixture(t)
	c, root := f.config(t, config.Listener{ID: "gram", Protocol: "unixgram"})
	c.Listeners[0].Address = filepath.Join(root, "gram.sock")
	g := startAuthorGateway(t, c)
	client, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: filepath.Join(root, "client.sock"), Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	server := &net.UnixAddr{Name: g.addresses["gram"], Net: "unixgram"}
	exerciseLocalAuthors(t, f, "gram", func(id string, request map[string]any) plainFrameResponse {
		var datagram [16]byte
		if _, err := rand.Read(datagram[:]); err != nil {
			t.Fatal(err)
		}
		for _, fragment := range webTransportDatagrams(plainFrame(t, id, request), datagram) {
			if _, err := client.WriteTo(fragment, server); err != nil {
				t.Fatal(err)
			}
		}
		return decodePlainResponse(t, id, readAGD1Payload(t, client, time.Now().Add(10*time.Second)))
	})
}

// mailbox-once runs one plain frame from the private mailbox directory; a
// request without an author runs as the listener's bound author, whose
// token stays in an owner-only file instead of in every request.
func TestLocalAuthorsOverFileMailbox(t *testing.T) {
	f := newLocalAuthorFixture(t)
	c, root := f.config(t, config.Listener{ID: "mailbox", Protocol: "file_mailbox", Operations: []core.Operation{core.Send, core.ReceiptGet}})
	tokenFile := filepath.Join(root, "alice.token")
	if err := os.WriteFile(tokenFile, []byte(aliceToken+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	mailbox := filepath.Join(root, "mailbox")
	if err := os.Mkdir(mailbox, 0700); err != nil {
		t.Fatal(err)
	}
	c.Listeners[0].Address = mailbox
	c.Listeners[0].BoundAuthor = &config.BoundAuthor{Nick: "alice", AccountTokenFile: tokenFile}
	path := writeGatewayConfig(t, root, c)
	binary := currentGatewayBinary(t)
	call := func(id string, request map[string]any) plainFrameResponse {
		if err := os.WriteFile(filepath.Join(mailbox, "request.json"), plainFrame(t, id, request), 0600); err != nil {
			t.Fatal(err)
		}
		if output, err := exec.Command(binary, "mailbox-once", "--file", path).CombinedOutput(); err != nil {
			t.Fatalf("mailbox-once: %v %s", err, output)
		}
		body, err := os.ReadFile(filepath.Join(mailbox, "response.json"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(mailbox, "response.json")); err != nil {
			t.Fatal(err)
		}
		return decodePlainResponse(t, id, body)
	}
	exerciseLocalAuthors(t, f, "mailbox", call)
	// No author in the request: the bound author posts, logged in with the
	// token from its file.
	bound := sendRequest("mailbox-bound-post", "#lobby", "bound author via mailbox", "", "")
	if r := call("mailbox-4", bound); r.Error != nil || r.Result == nil {
		t.Fatalf("bound author post: %+v %+v", r, r.Error)
	}
	if from := f.posted(t, "bound author via mailbox"); from != "alice" {
		t.Fatalf("bound author post authored as %q", from)
	}
}

// import-bundle admits plain author requests; the operator who runs it is
// the access gate. Re-importing the same requests is a replay that posts
// nothing new, and --author-nick names the author of requests without one.
func TestLocalAuthorsOverOfflineBundle(t *testing.T) {
	f := newLocalAuthorFixture(t)
	c, root := f.config(t, config.Listener{ID: "bundle", Protocol: "offline_bundle", Operations: []core.Operation{core.Send}})
	path := writeGatewayConfig(t, root, c)
	binary := currentGatewayBinary(t)
	write := func(name string, requests ...map[string]any) string {
		bundle := struct {
			Version  int              `json:"version"`
			Requests []map[string]any `json:"requests"`
		}{1, requests}
		data, err := json.Marshal(bundle)
		if err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(root, name)
		if err := os.WriteFile(file, data, 0600); err != nil {
			t.Fatal(err)
		}
		return file
	}
	importBundle := func(file string, wantErr bool, extra ...string) service.OfflineImportReport {
		var stdout, stderr bytes.Buffer
		cmd := exec.Command(binary, append([]string{"import-bundle", "--file", path, "--input", file}, extra...)...)
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		if (err != nil) != wantErr {
			t.Fatalf("import-bundle: %v %s", err, stderr.String())
		}
		var report service.OfflineImportReport
		if stdout.Len() == 0 {
			return report
		}
		if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
			t.Fatalf("import report %s: %v", stdout.String(), err)
		}
		return report
	}
	first := write("first.json",
		sendRequest("bundle-guest-post", "#lobby", "guest via bundle", "bundle-guest", ""),
		sendRequest("bundle-alice-post", "#lobby", "alice via bundle", "alice", aliceToken))
	report := importBundle(first, false)
	if len(report.Entries) != 2 {
		t.Fatalf("import report: %+v", report)
	}
	for i, author := range []string{"bundle-guest", "alice"} {
		if e := report.Entries[i]; !e.Imported || e.ReplayRejected || e.Author != author || e.Result.State != core.Accepted {
			t.Fatalf("entry %d: %+v %+v", i, e, e.Result.Failure)
		}
	}
	if from := f.posted(t, "guest via bundle"); from != "bundle-guest" {
		t.Fatalf("guest bundle post authored as %q", from)
	}
	if from := f.posted(t, "alice via bundle"); from != "alice" {
		t.Fatalf("registered bundle post authored as %q", from)
	}
	unnamed := write("unnamed.json", sendRequest("bundle-bound-post", "#lobby", "bound via bundle", "", ""))
	if report := importBundle(unnamed, true); report.Version != 0 {
		t.Fatal("a request without an author imported without a bound author", report)
	}
	if report := importBundle(unnamed, false, "--author-nick", "courier"); len(report.Entries) != 1 || report.Entries[0].Author != "courier" {
		t.Fatalf("bound import: %+v", report)
	}
	if from := f.posted(t, "bound via bundle"); from != "courier" {
		t.Fatalf("bound bundle post authored as %q", from)
	}
	replay := importBundle(first, true)
	if len(replay.Entries) != 2 || !replay.Entries[0].ReplayRejected || !replay.Entries[1].ReplayRejected || replay.Entries[0].Result.State != core.Accepted {
		t.Fatalf("replay report: %+v", replay)
	}
	for _, text := range []string{"guest via bundle", "alice via bundle"} {
		if n := f.nativeCount(t, text); n != 1 {
			t.Fatalf("replayed bundle posted %q %d times", text, n)
		}
	}
}

// The same request ID on two carriers is one job: a client falling back
// from a Unix stream to a mailbox does not post twice.
func TestLocalAuthorRetryAcrossCarriers(t *testing.T) {
	f := newLocalAuthorFixture(t)
	c, root := f.config(t, config.Listener{ID: "unix", Protocol: "unix"})
	c.Listeners[0].Address = filepath.Join(root, "unix.sock")
	g := startAuthorGateway(t, c)
	request := sendRequest("fallback-post", "#lobby", "posted once across carriers", "fallback", "")
	stream := dialFramedUnix(t, g.addresses["unix"])
	if r := stream.call(t, "first", request); r.Error != nil {
		t.Fatalf("unix post: %+v", r.Error)
	}
	f.posted(t, "posted once across carriers")
	// Let the journal record acceptance before the client "falls back".
	for deadline := time.Now().Add(10 * time.Second); ; {
		r := stream.call(t, "receipt", plainRequest(map[string]any{"request_id": "fallback-receipt", "operation": "receipt.get", "lookup_id": "fallback-post"}, "fallback", ""))
		if r.Error == nil && r.Result != nil && r.Result.Result != nil && r.Result.Result.State == core.Accepted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("unix post never accepted: %+v", r)
		}
		time.Sleep(100 * time.Millisecond)
	}
	g.stop(t)

	mailbox := filepath.Join(root, "mailbox")
	if err := os.Mkdir(mailbox, 0700); err != nil {
		t.Fatal(err)
	}
	c.Listeners[0] = config.Listener{ID: "mailbox", Protocol: "file_mailbox", Address: mailbox, Enabled: true, Operations: []core.Operation{core.Send}}
	path := writeGatewayConfig(t, root, c)
	if err := os.WriteFile(filepath.Join(mailbox, "request.json"), plainFrame(t, "again", request), 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(currentGatewayBinary(t), "mailbox-once", "--file", path).CombinedOutput(); err != nil {
		t.Fatalf("mailbox-once: %v %s", err, output)
	}
	body, err := os.ReadFile(filepath.Join(mailbox, "response.json"))
	if err != nil {
		t.Fatal(err)
	}
	if r := decodePlainResponse(t, "again", body); r.Error != nil || r.Result == nil || r.Result.Result.State != core.Accepted {
		t.Fatalf("mailbox retry: %s", body)
	}
	if n := f.nativeCount(t, "posted once across carriers"); n != 1 {
		t.Fatalf("retry across carriers posted %d messages", n)
	}
}
