package machipc

import (
	"strings"
	"testing"
)

func TestValidName(t *testing.T) {
	for _, name := range []string{"a", "A0", "a.b-c_d", "x" + strings.Repeat("a", 126)} {
		if !ValidName(name) {
			t.Errorf("ValidName(%q)=false", name)
		}
	}
	for _, name := range []string{"", ".a", "_a", "-a", "a/b", "a b", "a\x00b", "é", "x" + strings.Repeat("a", 127)} {
		if ValidName(name) {
			t.Errorf("ValidName(%q)=true", name)
		}
	}
}
