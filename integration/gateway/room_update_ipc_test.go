//go:build darwin || linux

package gateway_test

import (
	"crypto/rand"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/testfs"
	"github.com/Someblueman/airc/pkg/irc"
)

type roomUpdateFrameCall func(core.Request, string) adapter.FrameResponse

var roomReaderSequence atomic.Uint64

func waitRoomReceipt(t *testing.T, call roomUpdateFrameCall, request core.Request) *core.Result {
	t.Helper()
	var last adapter.FrameResponse
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		lookup := core.Request{Version: 1, RequestID: "receipt-" + request.RequestID, Operation: core.ReceiptGet, LookupID: request.RequestID}
		response := call(lookup, rand.Text())
		last = response
		if response.Error == nil && response.Result != nil && response.Result.Result != nil && response.Result.Result.State == core.Accepted {
			if response.Result.Result.Room == nil || response.Result.Result.MessageID != "" || response.Result.Result.Persisted != nil || response.Result.Result.RecipientConnected != nil || response.Result.Result.LookupAttempts != 0 {
				t.Fatalf("room receipt invented message facts: %+v", response.Result.Result)
			}
			return response.Result.Result
		}
		time.Sleep(20 * time.Millisecond)
	}
	if last.Result != nil {
		t.Fatalf("room receipt was not accepted: %s state=%+v", request.RequestID, last.Result.Result)
	}
	t.Fatalf("room receipt was not accepted: %s last=%+v", request.RequestID, last)
	return nil
}

func assertRoomUpdate(t *testing.T, call roomUpdateFrameCall, socket string, request core.Request) *core.Result {
	t.Helper()
	response := call(request, request.RequestID)
	if response.Error != nil || response.Result == nil || response.Result.Result == nil || !response.Result.Result.Durable {
		t.Fatalf("room.update was not durably admitted: %+v", response)
	}
	result := waitRoomReceipt(t, call, request)
	native := queryNativeRoom(t, socket, request.Target.Name)
	if result.Room == nil || !reflect.DeepEqual(result.Room.Room, native) {
		t.Fatalf("room receipt/native mismatch: receipt=%+v native=%+v", result.Room, native)
	}
	return result
}

func roomUpdateRequest(t *testing.T, id string, target *core.Target, slow *int64, limit *int) core.Request {
	t.Helper()
	issued, expiry := time.Now().Unix(), time.Now().Unix()+120
	return core.Request{Version: 1, RequestID: id, Operation: core.RoomUpdate, Target: target, Room: &core.RoomPatch{SlowSeconds: slow, HistoryLimit: limit}, IssuedAt: &issued, ExpiresAt: &expiry}
}

func queryNativeRoom(t *testing.T, socket, target string) core.RoomResult {
	t.Helper()
	c := dial(t, socket, "room-reader-"+fmt.Sprint(roomReaderSequence.Add(1)), "", false, false)
	defer c.Close()
	if err := c.Chat(irc.ChatRequest{Action: "room", Target: target, Seconds: -1, Limit: -1}); err != nil {
		t.Fatal(err)
	}
	event := next(t, c, func(e *irc.ChatEvent) bool { return e.Action == "room" && e.Target == target })
	next(t, c, func(e *irc.EndOfChatEvent) bool { return true })
	return core.RoomResult{Target: core.Target{Kind: "room", Name: target}, SlowSeconds: event.SlowSeconds, HistoryLimit: event.HistoryLimit}
}

func promoteRoomOperator(t *testing.T, socket string) {
	t.Helper()
	admin := dial(t, socket, "fixture-control", "", false, true)
	defer admin.Close()
	token, err := auth.ReadToken(filepath.Join(filepath.Dir(socket), "admin.token"))
	if err != nil {
		t.Fatal(err)
	}
	if err := admin.AuthenticateAdmin(token); err != nil {
		t.Fatal(err)
	}
	next(t, admin, func(e *irc.RawEvent) bool { return e.Command == "381" })
	if err := admin.Raw("MODE #gateway-fixture +o fixture-author"); err != nil {
		t.Fatal(err)
	}
	if err := admin.Raw("MODE #gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	seen324, seen783 := false, false
	for !seen324 || !seen783 {
		event := next(t, admin, func(e *irc.RawEvent) bool { return e.Command == "324" || e.Command == "783" })
		if event.Command == "324" {
			seen324 = true
		} else {
			if len(event.Params) != 4 || event.Params[1] != "#gateway-fixture" || event.Params[2] != "fixture-author" || len(event.Params[3]) != 32 {
				t.Fatalf("foreign operator evidence: %+v", event)
			}
			seen783 = true
		}
	}
}

func TestRoomUpdateOverStdioWithRestartReplay(t *testing.T) {
	socket, config, credentials, observer := roomUpdateStdioConfig(t)
	defer observer.Close()
	process := startStdio(t, gatewayBinary(t), config)
	tokens := make(map[string]string)
	call := func(request core.Request, correlation string) adapter.FrameResponse {
		token := credentials.token(t, request, true)
		if request.Operation == core.RoomUpdate {
			if cached := tokens[request.RequestID]; cached != "" {
				token = cached
			} else {
				tokens[request.RequestID] = token
			}
		}
		process.write(t, adapter.FrameRequest{Version: 1, CorrelationID: correlation, Profile: "encrypted", Envelope: token})
		response := process.read(t)
		if response.CorrelationID != correlation || response.Event != "result" {
			t.Fatalf("stdio room response mismatch: %+v", response)
		}
		return response
	}
	target := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	slow, limit := int64(17), 12
	request := roomUpdateRequest(t, "stdio-room-update", target, &slow, &limit)
	first := assertRoomUpdate(t, call, socket, request)
	stored := *first.Room
	other := int64(3)
	admin := dial(t, socket, "fixture-control-2", "", false, true)
	token, err := auth.ReadToken(filepath.Join(filepath.Dir(socket), "admin.token"))
	if err != nil {
		t.Fatal(err)
	}
	if err := admin.AuthenticateAdmin(token); err != nil {
		t.Fatal(err)
	}
	next(t, admin, func(e *irc.RawEvent) bool { return e.Command == "381" })
	if err := admin.Chat(irc.ChatRequest{Action: "room", Target: target.Name, Seconds: other, Limit: limit}); err != nil {
		t.Fatal(err)
	}
	next(t, admin, func(e *irc.ChatEvent) bool { return e.Action == "room" && e.SlowSeconds == other })
	next(t, admin, func(e *irc.EndOfChatEvent) bool { return true })
	admin.Close()
	if err := process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	if err := process.wait(); err != nil {
		t.Fatalf("stdio gateway did not stop for replay: %v", err)
	}
	process = startStdio(t, gatewayBinary(t), config)
	replay := call(request, "stdio-room-replay")
	if replay.Error != nil || replay.Result == nil || replay.Result.Result == nil || !replay.Result.Result.Durable {
		t.Fatalf("room replay was not durable: %+v", replay)
	}
	receipt := waitRoomReceipt(t, call, request)
	if receipt.SendAttempts != 1 || receipt.LookupAttempts != 0 || receipt.Room == nil || !reflect.DeepEqual(*receipt.Room, stored) {
		t.Fatalf("room replay changed durable receipt: %+v want=%+v", receipt, stored)
	}
	native := queryNativeRoom(t, socket, target.Name)
	if native.SlowSeconds != other || native.HistoryLimit != limit {
		t.Fatalf("room replay unexpectedly rewrote native settings: %+v", native)
	}
	partial := roomUpdateRequest(t, "stdio-room-partial", target, nil, func() *int { value := 0; return &value }())
	partialResult := assertRoomUpdate(t, call, socket, partial)
	if partialResult.Room.Room.SlowSeconds != other || partialResult.Room.Room.HistoryLimit != 0 {
		t.Fatalf("partial room update did not preserve/return native settings: %+v", partialResult.Room)
	}
	if err := process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	if err := process.wait(); err != nil {
		t.Fatalf("stdio gateway did not stop: %v", err)
	}
}

func roomUpdateStdioConfig(t *testing.T) (string, config.Config, *detachedCredentials, *irc.Client) {
	t.Helper()
	socket := fixtureWithAdmin(t, true)
	observer := dial(t, socket, "stdio-room-observer", "", false, false)
	if err := observer.Join("#gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	next(t, observer, func(e *irc.JoinEvent) bool { return e.Agent == "stdio-room-observer" })
	provision := dial(t, socket, "fixture-author", fixtureAccount, true, true)
	provision.Close()
	promoteRoomOperator(t, socket)
	c, _ := acceptanceJournalFixture(t, socket, fixtureAccount)
	credentials := provisionDetached(t, &c)
	c.Principals[0].Operations = dispatch.ImplementedOperations()
	c.CursorKeyFile = filepath.Join(filepath.Dir(c.StateDir), "cursor.key")
	if err := auth.CreateCredential(c.CursorKeyFile); err != nil {
		t.Fatal(err)
	}
	c.Listeners = []config.Listener{{ID: "stdio", Protocol: "stdio", Enabled: true, Operations: dispatch.ImplementedOperations()}}
	return socket, c, credentials, observer
}

func roomUpdateUnixFixture(t *testing.T) (net.Conn, *detachedCredentials, func(), string) {
	t.Helper()
	socket := fixtureWithAdmin(t, true)
	provision := dial(t, socket, "fixture-author", fixtureAccount, true, true)
	provision.Close()
	c, _ := acceptanceJournalFixture(t, socket, fixtureAccount)
	credentials := provisionDetached(t, &c)
	c.Principals[0].BearerTokenFile = ""
	c.Principals[0].Operations = dispatch.ImplementedOperations()
	root, err := testfs.MkdirTemp("ru-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "gateway.sock")
	c.CursorKeyFile = filepath.Join(root, "cursor.key")
	if err := auth.CreateCredential(c.CursorKeyFile); err != nil {
		t.Fatal(err)
	}
	c.Listeners = []config.Listener{{ID: "unix", Protocol: "unix", Address: path, Enabled: true, Operations: dispatch.ImplementedOperations()}}
	p := startGateway(t, gatewayBinary(t), c)
	address := gatewayAddress(t, p)
	conn, err := net.DialTimeout("unix", address, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	promoteRoomOperator(t, socket)
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
			t.Fatal("Unix gateway did not join")
		}
	}
	return conn, credentials, stop, socket
}

func TestRoomUpdateOverUnixFramed(t *testing.T) {
	conn, credentials, stop, socket := roomUpdateUnixFixture(t)
	defer conn.Close()
	defer stop()
	call := func(request core.Request, correlation string) adapter.FrameResponse {
		writeFrame(t, conn, adapter.FrameRequest{Version: 1, CorrelationID: correlation, Profile: "encrypted", Envelope: credentials.token(t, request, true)})
		response := readFrame(t, conn)
		if response.CorrelationID != correlation || response.Event != "result" {
			t.Fatalf("Unix room response mismatch: %+v", response)
		}
		return response
	}
	target := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	slow, limit := int64(23), 9
	request := roomUpdateRequest(t, "unix-room-update", target, &slow, &limit)
	assertRoomUpdate(t, call, socket, request)
	zero := 0
	partial := roomUpdateRequest(t, "unix-room-partial", target, &slow, &zero)
	result := assertRoomUpdate(t, call, socket, partial)
	if result.Room.Room.SlowSeconds != slow || result.Room.Room.HistoryLimit != 0 {
		t.Fatalf("Unix partial room result mismatch: %+v", result.Room)
	}
}
