package core

import (
	"strings"
	"testing"
)

func TestDecodeBoundaries(t *testing.T) {
	base := `{"version":1,"request_id":"req-1","operation":"send","target":{"kind":"room","name":"#Room"},"message":"hello"}`
	for _, input := range []string{
		strings.Replace(base, `"version":1`, `"version":1,"version":1`, 1),
		strings.Replace(base, `"version":1`, `"Version":1`, 1),
		strings.Replace(base, `"version":1`, `"version":2`, 1),
		strings.Replace(base, `"hello"`, `null`, 1),
		strings.Replace(base, `"hello"`, `"\u0000"`, 1),
		strings.Replace(base, `"hello"`, `"\ud800"`, 1),
		strings.Replace(base, `"hello"`, `"\udc00"`, 1),
		strings.Replace(base, `"#Room"`, `"#Room,other"`, 1),
		strings.Replace(base, `"#Room"`, `"#Room\r\nOPER"`, 1),
		strings.Replace(base, `"kind":"room"`, `"kind":"room","kind":"room"`, 1),
		strings.Replace(base, `"message":`, `"principal_id":"attacker","message":`, 1),
		base + `{}`,
		`null`,
		strings.Repeat("[", 10) + "0" + strings.Repeat("]", 10),
		strings.Replace(base, "hello", strings.Repeat("x", MaxEnvelopeBytes), 1),
		strings.Replace(base, "hello", string([]byte{0xff}), 1),
	} {
		if _, err := Decode([]byte(input)); err == nil {
			t.Errorf("accepted invalid input: %.150s", input)
		}
	}
	r, err := Decode([]byte(base))
	if err != nil || r.Target.Name != "#Room" {
		t.Fatalf("valid case-sensitive room: %+v %v", r, err)
	}
	for _, body := range []string{`\ud83d\ude00`, `\\ud800`} {
		if _, err := Decode([]byte(strings.Replace(base, "hello", body, 1))); err != nil {
			t.Fatalf("valid Unicode or literal escape %s: %v", body, err)
		}
	}
	for _, size := range []int{4096, 4097} {
		input := strings.Replace(base, "hello", strings.Repeat("x", size), 1)
		_, err := Decode([]byte(input))
		if (err == nil) != (size == 4096) {
			t.Errorf("message byte boundary %d: %v", size, err)
		}
	}
}

func TestFingerprintAndScope(t *testing.T) {
	r := Request{Version: 1, RequestID: "one", Operation: Send, Target: &Target{Kind: "direct", Name: "Alice"}, Message: "a\r\nb"}
	a, err := r.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	if a != "c84fc5674d0c0b6c3e88effa8ea9ca890b2096744866761b588233ebea1a716e" {
		t.Fatalf("v1 fingerprint encoding changed: %s", a)
	}
	if r.Target.Name != "Alice" {
		t.Fatal("fingerprinting mutated caller")
	}
	r.RequestID, r.Message, r.Target.Name = "two", "a\nb", "alice"
	b, _ := r.Fingerprint()
	if a != b {
		t.Fatal("normalization or independent ID changed intent")
	}
	policy := Retry
	r.DeliveryPolicy = &policy
	explicit, _ := r.Fingerprint()
	if explicit == a {
		t.Fatal("lost explicit/omitted default distinction")
	}
	r.DeliveryPolicy = nil
	r.Target = &Target{Kind: "room", Name: "#Room"}
	a, _ = r.Fingerprint()
	r.Target.Name = "#room"
	b, _ = r.Fingerprint()
	if a == b {
		t.Fatal("lowercased room")
	}
	id, err := UpstreamID("realm", "binding", "principal", "one")
	if err != nil || len(id) != 64 {
		t.Fatalf("upstream ID: %s %v", id, err)
	}
	for _, scope := range [][4]string{{"other", "binding", "principal", "one"}, {"realm", "other", "principal", "one"}, {"realm", "binding", "other", "one"}, {"realm", "binding", "principal", "two"}} {
		other, _ := UpstreamID(scope[0], scope[1], scope[2], scope[3])
		if other == id {
			t.Fatal("scope collision")
		}
	}
}

func TestOperationArguments(t *testing.T) {
	target := &Target{Kind: "room", Name: "#test"}
	for _, r := range []Request{
		{Version: 1, RequestID: "1", Operation: Reply, Target: target, Message: "reply", ParentID: strings.Repeat("a", 32)},
		{Version: 1, RequestID: "1", Operation: ReceiptGet, LookupID: "original"},
		{Version: 1, RequestID: "1", Operation: HistoryRead, Target: target, Limit: 50},
		{Version: 1, RequestID: "1", Operation: Watch, Target: target, Limit: 50},
		{Version: 1, RequestID: "1", Operation: Search, Target: target, Limit: 50, Query: "hello"},
		{Version: 1, RequestID: "1", Operation: Directory, Limit: 50},
	} {
		if err := r.Normalize(); err != nil {
			t.Fatalf("%s: %v", r.Operation, err)
		}
		r.Message = "invalid read body"
		if r.Operation == Reply {
			r.ParentID = "bad"
		}
		if err := r.Normalize(); err == nil {
			t.Fatalf("accepted bad %s arguments", r.Operation)
		}
	}
}

func TestReactionRequestBoundaries(t *testing.T) {
	parent := strings.Repeat("a", 32)
	valid := Request{Version: 1, RequestID: "reaction-1", Operation: ReactionAdd, Target: &Target{Kind: "room", Name: "#Room"}, ParentID: parent, Reaction: "checking"}
	if err := valid.Normalize(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Request){
		"direct target":    func(r *Request) { r.Target = &Target{Kind: "direct", Name: "alice"} },
		"ordinary message": func(r *Request) { r.Message = "body" },
		"invalid parent":   func(r *Request) { r.ParentID = "parent" },
		"invalid reaction": func(r *Request) { r.Reaction = "two words" },
		"read selector":    func(r *Request) { r.Limit = 1 },
	} {
		t.Run(name, func(t *testing.T) {
			r := valid
			if valid.Target != nil {
				target := *valid.Target
				r.Target = &target
			}
			mutate(&r)
			if err := r.Normalize(); err == nil {
				t.Fatal("invalid reaction request accepted")
			}
		})
	}
}

func TestContextRequestBoundsAndFingerprintDefaults(t *testing.T) {
	valid := Request{Version: 1, RequestID: "context-1", Operation: ContextRead, Target: &Target{Kind: "room", Name: "#Room"}, ParentID: strings.Repeat("a", 32)}
	if err := valid.Normalize(); err != nil {
		t.Fatal(err)
	}
	if got, err := valid.Fingerprint(); err != nil || got == "" {
		t.Fatalf("context fingerprint: %q %v", got, err)
	}
	for name, mutate := range map[string]func(*Request){
		"bad limit":      func(r *Request) { r.Limit = 1001 },
		"bad max bytes":  func(r *Request) { r.MaxBytes = MaxContextBytes + 1 },
		"small max":      func(r *Request) { r.MaxBytes = MinContextBytes - 1 },
		"missing parent": func(r *Request) { r.ParentID = "" },
		"direct target":  func(r *Request) { r.Target = &Target{Kind: "direct", Name: "alice"} },
	} {
		t.Run(name, func(t *testing.T) {
			r := valid
			target := *valid.Target
			r.Target = &target
			mutate(&r)
			if err := r.Normalize(); err == nil {
				t.Fatal("invalid context request accepted")
			}
		})
	}
}

func TestConversationReadSelectors(t *testing.T) {
	parent := strings.Repeat("a", 32)
	for _, op := range []Operation{HistoryRead, Search, Watch} {
		base := Request{Version: 1, RequestID: "conversation-1", Operation: op, Target: &Target{Kind: "room", Name: "#Room"}, ParentID: parent, ThreadMode: "thread", Limit: 50}
		if op == Search {
			base.Query = "body"
		}
		if err := base.Normalize(); err != nil {
			t.Fatalf("%s valid selector: %v", op, err)
		}
		for _, mode := range []string{"replies", "invalid"} {
			r := base
			r.ThreadMode = mode
			if mode == "invalid" {
				if err := r.Normalize(); err == nil {
					t.Fatalf("%s accepted invalid conversation mode", op)
				}
			} else if err := r.Normalize(); err != nil {
				t.Fatalf("%s valid replies selector: %v", op, err)
			}
		}
		for name, mutate := range map[string]func(*Request){
			"missing parent":      func(r *Request) { r.ParentID = "" },
			"uppercase parent":    func(r *Request) { r.ParentID = strings.Repeat("A", 32) },
			"direct target":       func(r *Request) { r.Target = &Target{Kind: "direct", Name: "alice"} },
			"mode without parent": func(r *Request) { r.ParentID = "" },
		} {
			t.Run(string(op)+"/"+name, func(t *testing.T) {
				r := base
				mutate(&r)
				if err := r.Normalize(); err == nil {
					t.Fatal("invalid conversation selector accepted")
				}
			})
		}
	}
	ordinary := Request{Version: 1, RequestID: "ordinary", Operation: HistoryRead, Target: &Target{Kind: "room", Name: "#Room"}, Limit: 50}
	if err := ordinary.Normalize(); err != nil {
		t.Fatal(err)
	}
	if ordinary.ParentID != "" || ordinary.ThreadMode != "" {
		t.Fatal("ordinary history unexpectedly gained selector fields")
	}
	for _, op := range []Operation{HistoryRead, Search, Watch, ContextRead} {
		r := ordinary
		r.Operation = op
		r.ParentID = parent
		r.ThreadMode = "thread"
		if op == Search {
			r.Query = "body"
		}
		if op == ContextRead {
			r.Limit = 0
		}
		if op == ContextRead {
			if err := r.Normalize(); err == nil {
				t.Fatal("context accepted thread mode")
			}
		}
	}
}

func FuzzDecode(f *testing.F) {
	f.Add([]byte(`{"version":1,"request_id":"r","operation":"receipt.get","lookup_id":"a"}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		r, err := Decode(data)
		if err == nil {
			if _, err := r.Fingerprint(); err != nil {
				t.Fatal(err)
			}
		}
	})
}
