package dispatch

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func sendBody(t *testing.T, id string, author map[string]string) []byte {
	t.Helper()
	request := map[string]any{"version": 1, "request_id": id, "operation": "send", "target": map[string]string{"kind": "room", "name": "#ops"}, "message": "hello"}
	if author != nil {
		request["author"] = author
	}
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestConnectAppliesListenerPassword(t *testing.T) {
	open, _ := gateFixture(t, config.Listener{Protocol: "http"})
	if open.PasswordRequired() {
		t.Fatal("password without password_file")
	}
	session := NewSession()
	if err := open.Connect(session, core.Password{}); err != nil {
		t.Fatal("open listener refused a connection", err)
	}
	locked, _ := gateFixture(t, config.Listener{Protocol: "http", PasswordFile: "letmein"})
	if !locked.PasswordRequired() {
		t.Fatal("password_file not required")
	}
	wrong, _ := core.NewPassword("nope")
	if err := locked.Connect(NewSession(), wrong); !errors.Is(err, auth.ErrForbidden) {
		t.Fatal("wrong password admitted", err)
	}
	if err := locked.Connect(NewSession(), core.Password{}); !errors.Is(err, auth.ErrForbidden) {
		t.Fatal("missing password admitted", err)
	}
	right, _ := core.NewPassword("letmein")
	admitted := NewSession()
	if err := locked.Connect(admitted, right); err != nil {
		t.Fatal(err)
	}
	// Later requests on the admitted connection need no password.
	if _, err := locked.Submit(t.Context(), Ingress{Source: "192.0.2.1", Session: admitted}, sendBody(t, "after-connect", map[string]string{"nick": "erin"}), time.Unix(1000, 0)); err != nil {
		t.Fatal("admitted connection refused", err)
	}
	if err := (*Dispatcher)(nil).Connect(admitted, right); !errors.Is(err, ErrUnsupported) {
		t.Fatal("nil dispatcher", err)
	}
}
