//go:build darwin || linux

package gateway_test

import (
	"os"
	"slices"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestMembersReadOverUnixFramed(t *testing.T) {
	conn, credentials, stop, _, socket := unixStreamFixture(t)
	defer conn.Close()
	defer stop()
	seedMembersIPC(t, socket, "members-ipc-unix")
	want := nativeMembers(t, socket, "#gateway-fixture")
	if !slices.Contains(want, core.Member{Nick: "fixture-author"}) || !slices.Contains(want, core.Member{Nick: "members-ipc-unix"}) {
		t.Fatal("missing native member controls", want)
	}
	request := core.Request{Version: 1, RequestID: "members-unix", Operation: core.MembersRead, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}}
	call := func(r core.Request, encrypted bool) adapter.FrameResponse {
		profile := "signed"
		if encrypted {
			profile = "encrypted"
		}
		writeFrame(t, conn, adapter.FrameRequest{Version: 1, CorrelationID: r.RequestID, Profile: profile, Envelope: credentials.token(t, r, encrypted)})
		response := readFrame(t, conn)
		if response.CorrelationID != r.RequestID || response.Event != "result" && response.Event != "error" {
			t.Fatalf("Unix members response=%+v", response)
		}
		return response
	}
	for _, id := range []string{"members-unix", "members-unix-repeat"} {
		request.RequestID = id
		response := call(request, true)
		if response.Error != nil || response.Result == nil || response.Result.Members == nil {
			t.Fatalf("Unix members result=%+v", response)
		}
		assertMembers(t, *response.Result.Members, request.Target, want)
	}
	wrong := request
	wrong.RequestID = "members-unix-foreign"
	wrong.Target = &core.Target{Kind: "room", Name: "#private"}
	response := call(wrong, true)
	if response.Error == nil || response.Error.Code != core.Forbidden || response.Result != nil {
		t.Fatalf("Unix foreign members target=%+v", response)
	}
	lookup := core.Request{Version: 1, RequestID: "members-unix-receipt", Operation: core.ReceiptGet, LookupID: "members-unix"}
	response = call(lookup, true)
	if response.Error == nil || response.Error.Code != core.InvalidRequest || response.Result != nil {
		t.Fatalf("Unix members read created a receipt: %+v", response)
	}
}

func TestMembersReadOverStdioAndShutdown(t *testing.T) {
	socket, config, credentials, observer := stdioFixtureConfig(t)
	defer observer.Close()
	config.Principals[0].Targets = append(config.Principals[0].Targets, core.Target{Kind: "room", Name: "#members-ipc-empty"})
	seedMembersIPC(t, socket, "members-ipc-stdio")
	process := startStdio(t, gatewayBinary(t), config)
	call := func(r core.Request, encrypted bool) adapter.FrameResponse {
		profile := "signed"
		if encrypted {
			profile = "encrypted"
		}
		process.write(t, adapter.FrameRequest{Version: 1, CorrelationID: r.RequestID, Profile: profile, Envelope: credentials.token(t, r, encrypted)})
		response := process.read(t)
		if response.CorrelationID != r.RequestID || response.Event != "result" && response.Event != "error" {
			t.Fatalf("stdio members response=%+v", response)
		}
		return response
	}
	request := core.Request{Version: 1, RequestID: "members-stdio", Operation: core.MembersRead, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}}
	want := nativeMembers(t, socket, request.Target.Name)
	if !slices.Contains(want, core.Member{Nick: "fixture-author"}) || !slices.Contains(want, core.Member{Nick: "members-ipc-stdio"}) {
		t.Fatal("missing native member controls", want)
	}
	response := call(request, false)
	if response.Error != nil || response.Result == nil || response.Result.Members == nil {
		t.Fatalf("stdio members result=%+v", response)
	}
	assertMembers(t, *response.Result.Members, request.Target, want)
	empty := request
	empty.RequestID = "members-stdio-empty"
	empty.Target = &core.Target{Kind: "room", Name: "#members-ipc-empty"}
	response = call(empty, true)
	if response.Error != nil || response.Result == nil || response.Result.Members == nil || len(response.Result.Members.Members) != 0 {
		t.Fatalf("stdio empty members result=%+v", response)
	}
	request.RequestID = "members-stdio-repeat"
	response = call(request, true)
	if response.Error != nil || response.Result == nil || response.Result.Members == nil {
		t.Fatalf("stdio repeated members result=%+v", response)
	}
	assertMembers(t, *response.Result.Members, request.Target, want)
	lookup := core.Request{Version: 1, RequestID: "members-stdio-receipt", Operation: core.ReceiptGet, LookupID: "members-stdio"}
	response = call(lookup, true)
	if response.Error == nil || response.Error.Code != core.InvalidRequest || response.Result != nil {
		t.Fatalf("stdio members read created a receipt: %+v", response)
	}
	if err := process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	if err := process.wait(); err != nil {
		t.Fatalf("stdio gateway did not join: %v", err)
	}
}

func seedMembersIPC(t *testing.T, socket, nick string) {
	t.Helper()
	operator := dial(t, socket, "fixture-author", fixtureAccount, false, false)
	if err := operator.Join("#gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	next(t, operator, func(e *irc.JoinEvent) bool { return e.Agent == "fixture-author" })
	member := dial(t, socket, nick, "", false, false)
	if err := member.Join("#gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	next(t, member, func(e *irc.JoinEvent) bool { return e.Agent == nick })
}
