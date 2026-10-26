package core

import (
	"strings"
	"testing"
)

func TestDirectoryQueryCanonicalNicknameAndBoundaries(t *testing.T) {
	for _, test := range []struct {
		query, want string
		valid       bool
	}{
		{"", "", true}, {"MiXeD-Agent", "mixed-agent", true}, {"[]\\`_^{|}", "[]\\`_^{|}", true},
		{strings.Repeat("a", 30), strings.Repeat("a", 30), true},
		{strings.Repeat("a", 31), "", false}, {"*", "", false}, {"#room", "", false},
		{"agent,other", "", false}, {"a\r\nOPER", "", false}, {"1agent", "", false}, {"agent 🙂", "", false},
	} {
		r := Request{Version: 1, RequestID: "directory", Operation: Directory, Limit: 1, Query: test.query}
		err := r.Normalize()
		if (err == nil) != test.valid || err == nil && (r.Query != test.want || r.Target != nil) {
			t.Fatal(test, r, err)
		}
	}
	a := Request{Version: 1, RequestID: "a", Operation: Directory, Limit: 1, Query: "MiXeD"}
	b := a
	b.Query = "mixed"
	af, err := a.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	bf, err := b.Fingerprint()
	if err != nil || af != bf {
		t.Fatal("directory case changed normalized intent", af, bf, err)
	}
	b.Query = "other"
	bf, _ = b.Fingerprint()
	if af == bf {
		t.Fatal("directory nickname lost from intent")
	}
}
