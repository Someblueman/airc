//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/testtls"
	"github.com/Someblueman/airc/pkg/irc"
)

func framedFixture(t *testing.T, secure bool) (net.Conn, *detachedCredentials, string, func()) {
	t.Helper()
	return framedFixtureAddress(t, secure, "127.0.0.1:0")
}

func framedFixtureAddress(t *testing.T, secure bool, listenAddress string) (net.Conn, *detachedCredentials, string, func()) {
	t.Helper()
	socket := fixture(t)
	const account = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	provision := dial(t, socket, "fixture-author", account, true, true)
	_ = provision.Close()
	c, _ := acceptanceJournalFixture(t, socket, account)
	credentials := provisionDetached(t, &c)
	c.CursorKeyFile = filepath.Join(filepath.Dir(c.StateDir), "cursor.key")
	if err := auth.CreateCredential(c.CursorKeyFile); err != nil {
		t.Fatal(err)
	}
	c.Limits.Watches, c.Limits.WatchesPerAuthor = 2, 1
	listener := config.Listener{ID: "stream", Protocol: "framed", Address: listenAddress, Enabled: true, Operations: dispatch.ImplementedOperations(), Limits: fixtureListenerLimits()}
	certificate := testtls.New(t)
	if secure {
		listener.TLSCertFile, listener.TLSKeyFile = certificate.CertPath, certificate.KeyPath
	}
	c.Listeners = []config.Listener{listener}
	p := startGateway(t, gatewayBinary(t), c)
	address := gatewayAddress(t, p)
	exampleCA := ""
	if secure {
		exampleCA = certificate.CAPath
	}
	framedExample(t, address, exampleCA, credentials)
	var conn net.Conn
	var err error
	if secure {
		trust := certificate.Client.Clone()
		trust.NextProtos = []string{adapter.FramedALPN}
		var secured *tls.Conn
		if secured, err = tls.DialWithDialer(&net.Dialer{Timeout: 3 * time.Second}, "tcp", address, trust); err == nil {
			if state := secured.ConnectionState(); state.Version != tls.VersionTLS13 || len(state.VerifiedChains) == 0 || state.NegotiatedProtocol != adapter.FramedALPN {
				t.Fatal("TLS verification or protocol mismatch", state)
			}
			conn = secured
		}
	} else {
		conn, err = net.DialTimeout("tcp", address, 3*time.Second)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	stop := func() {
		if err := p.command.Process.Signal(os.Interrupt); err != nil {
			t.Fatal(err)
		}
		select {
		case <-p.done:
			if p.err != nil {
				t.Fatal(p.err, p.stderr.String())
			}
		case <-time.After(8 * time.Second):
			t.Fatal("framed CLI did not join")
		}
	}
	return conn, credentials, socket, stop
}

func writeFrame(t *testing.T, conn adapter.FramedStream, r frameRequest) {
	t.Helper()
	body, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(body)))
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, err = io.Copy(conn, io.MultiReader(bytes.NewReader(header[:]), bytes.NewReader(body))); err != nil {
		t.Fatal(err)
	}
}

func readFrame(t *testing.T, conn adapter.FramedStream) adapter.FrameResponse {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(8 * time.Second))
	var header [4]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		t.Fatal(err)
	}
	size := binary.BigEndian.Uint32(header[:])
	if size == 0 || size > adapter.MaxResponseFrameBytes {
		t.Fatal("unbounded response", size)
	}
	body := make([]byte, size)
	if _, err := io.ReadFull(conn, body); err != nil {
		t.Fatal(err)
	}
	var r adapter.FrameResponse
	if err := json.Unmarshal(body, &r); err != nil {
		t.Fatal(err)
	}
	if r.Version != 1 || r.CorrelationID == "" {
		t.Fatal("uncorrelated response", r)
	}
	return r
}

// framedAuthenticatedSendAndBoundedResume posts twice over a framed stream,
// checks each native acceptance and stable replay, then pages history one
// message at a time to a caught-up cursor.
func framedAuthenticatedSendAndBoundedResume(t *testing.T, conn adapter.FramedStream, credentials *detachedCredentials, socket string, encrypted bool) {
	t.Helper()
	observer := dial(t, socket, "framed-observer", "", false, false)
	if err := observer.Join("#gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	next(t, observer, func(e *irc.JoinEvent) bool { return e.Agent == "framed-observer" })
	correlation := 0
	call := func(id string, request core.Request, requestBody string) *dispatch.DetachedResult {
		t.Helper()
		correlation++
		id = fmt.Sprintf("%s-%d", id, correlation)
		if requestBody == "" {
			requestBody = credentials.token(t, request, encrypted)
		}
		profile := "plain"
		writeFrame(t, conn, frameRequest{Version: 1, CorrelationID: id, Profile: profile, Request: requestBody})
		response := readFrame(t, conn)
		if response.CorrelationID != id || response.Error != nil || response.Result == nil {
			t.Fatal("framed operation failed", response)
		}
		return response.Result
	}
	room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	var observed []*irc.MessageEvent
	for _, id := range []string{"framed-one", "framed-two"} {
		request := core.Request{Version: 1, RequestID: id, Operation: core.Send, Target: room, Message: id + "\nwhole body 🙂"}
		requestBody := credentials.token(t, request, encrypted)
		admitted := call(id, request, requestBody)
		if admitted.Result == nil || !admitted.Result.Durable {
			t.Fatal("framed send was not durable", admitted)
		}
		var accepted *core.Result
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
			result := call(id+"-receipt", core.Request{Version: 1, RequestID: id + "-receipt", Operation: core.ReceiptGet, LookupID: id}, "")
			if result.Result != nil && result.Result.State == core.Accepted {
				accepted = result.Result
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if accepted == nil {
			t.Fatal("framed send did not reach native acceptance")
		}
		seen := next(t, observer, func(e *irc.MessageEvent) bool { return e.ID == accepted.MessageID })
		if seen.From != "fixture-author" || seen.Target != room.Name || seen.Message != request.Message {
			t.Fatal("framed native observer mismatch", seen)
		}
		observed = append(observed, seen)
		replayed := call(id+"-repeat", request, requestBody)
		if replayed.Result == nil || replayed.Result.MessageID != seen.ID || replayed.Result.SendAttempts != 1 {
			t.Fatal("framed stable replay changed acceptance", replayed)
		}
	}
	request := core.Request{Version: 1, RequestID: "framed-history", Operation: core.HistoryRead, Target: room, Limit: 1, Start: "retained"}
	for i, seen := range observed {
		result := call("framed-page", request, "")
		page := result.Page
		status := "more"
		if i == len(observed)-1 {
			status = "ok"
		}
		if page == nil || len(page.Messages) != 1 || page.Messages[0].ID != seen.ID || page.Messages[0].Message != seen.Message || page.Messages[0].From != seen.From || page.Messages[0].Target != seen.Target || page.Status != status || page.Cursor == "" || page.Gap {
			t.Fatal("framed bounded page mismatch", page)
		}
		request.Start, request.Cursor = "", page.Cursor
	}
	empty := call("framed-caught-up", request, "").Page
	if empty == nil || len(empty.Messages) != 0 || empty.Status != "ok" || empty.Cursor == "" || empty.Gap {
		t.Fatal("framed cursor replayed or lost position", empty)
	}
}
