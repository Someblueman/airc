package server

import (
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
)

// A run label travels as +airc/run on PRIVMSG, REPLY and CHAT, is kept in
// the message's metadata, the live tag, history and the archive, and SEARCH
// and HISTORY filter by it, in the window and in the archive alike.
func TestRunLabelRoundTripThroughHistorySearchAndArchive(t *testing.T) {
	dir := t.TempDir()
	address, _ := startServer(t, Config{HistoryLimit: 3, PingInterval: time.Hour, ReadTimeout: time.Hour}, func(s *Server) error {
		return s.RestoreHistory(filepath.Join(dir, "history"))
	})
	member, mr := wireRegister(t, address, "member", false)
	io.WriteString(member, "JOIN #room\r\n")
	expectLine(t, mr, member, " 366 ")
	writer, wr := wireRegister(t, address, "writer", true)
	receipt := func(line string) protocol.MessageMetadata {
		t.Helper()
		m, err := protocol.DecodeMessageMetadata(strings.TrimSpace(strings.SplitN(line, " :", 2)[1]))
		if err != nil {
			t.Fatalf("receipt %q: %v", line, err)
		}
		return m
	}

	// Invalid labels are refused; valid ones come back in the receipt and
	// reach a member in the live chat tag.
	long := strings.Repeat("r", 65)
	for _, bad := range []string{long, "", "a/b", `sp\sace`} {
		io.WriteString(writer, "@+airc/run="+bad+" PRIVMSG #room :refused\r\n")
		expectLine(t, wr, writer, " 417 ")
	}
	io.WriteString(writer, "@+airc/run="+strings.Repeat("r", 64)+" PRIVMSG #room :first\r\n")
	first := receipt(expectLine(t, wr, writer, " 762 "))
	if first.Run != strings.Repeat("r", 64) {
		t.Fatalf("receipt %+v", first)
	}
	live := expectLine(t, mr, member, "PRIVMSG #room :first")
	tags, _, _ := strings.Cut(strings.TrimPrefix(live, "@"), " ")
	var chat protocol.ChatMetadata
	for tag := range strings.SplitSeq(tags, ";") {
		if value, ok := strings.CutPrefix(tag, protocol.ChatTag+"="); ok {
			chat = protocol.DecodeChat(value)
		}
	}
	if chat.Run != first.Run {
		t.Fatalf("live tags %q carry run %q", tags, chat.Run)
	}
	io.WriteString(writer, "@+airc/run=run-B PRIVMSG #room :second\r\n")
	expectLine(t, wr, writer, " 762 ")
	io.WriteString(writer, "@+airc/run=run.A_1 REPLY "+first.ID+" :third\r\n")
	third := receipt(expectLine(t, wr, writer, " 762 "))
	if third.Run != "run.A_1" || third.ReplyTo != first.ID {
		t.Fatalf("reply receipt %+v", third)
	}
	status, _ := json.Marshal(protocol.ChatRequest{Action: "status", Target: "#room", Task: "port the fix", State: "working"})
	io.WriteString(writer, "@+airc/run=run.A_1 CHAT :"+string(status)+"\r\n")
	if line := expectLine(t, wr, writer, " 777 "); !strings.Contains(line, `"run":"run.A_1"`) {
		t.Fatalf("status entry %q", line)
	}
	io.WriteString(writer, "PRIVMSG #room :unlabelled\r\n")
	expectLine(t, wr, writer, " 762 ")

	// The window holds three messages; the first two are in the archive.
	bodies := func(line string) []string {
		t.Helper()
		got, _, _ := searchWire(t, writer, wr, line)
		return got
	}
	if got := bodies("SEARCH #room 10 * * run=run.A_1 :t"); strings.Join(got, ",") != "third,Status [working] port the fix" {
		t.Fatalf("window search by run = %v", got)
	}
	if got := bodies("SEARCH #room 10 " + first.ID + " * run=run-B :second"); strings.Join(got, ",") != "second" {
		t.Fatalf("archive search by run = %v", got)
	}
	if got := bodies("HISTORY #room 10 - run=run.A_1"); strings.Join(got, ",") != "third,Status [working] port the fix" {
		t.Fatalf("history by run = %v", got)
	}
	if got := bodies("HISTORY #room 10 " + first.ID + " run=run.A_1"); strings.Join(got, ",") != "third,Status [working] port the fix" {
		t.Fatalf("archive history by run = %v", got)
	}
	io.WriteString(writer, "HISTORY #room 10 - run=a/b\r\n")
	expectLine(t, wr, writer, " 461 ")
	io.WriteString(writer, "SEARCH #room 10 * * run="+long+" :x\r\n")
	expectLine(t, wr, writer, " 461 ")
}
