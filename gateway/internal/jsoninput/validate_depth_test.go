package jsoninput

import (
	"strings"
	"testing"
)

func TestExplicitDynamicJSONDepthBound(t *testing.T) {
	raw := []byte(strings.Repeat("[", 16) + "0" + strings.Repeat("]", 16))
	if ValidateDepth(raw, 1024, 16) != nil {
		t.Fatal("sixteen-level input refused")
	}
	if Validate(raw, 1024) == nil || ValidateDepth(raw, 1024, 17) == nil || ValidateDepth(raw, 1024, 0) == nil {
		t.Fatal("nesting bound weakened")
	}
	deeper := []byte("[" + string(raw) + "]")
	if ValidateDepth(deeper, 1024, 16) == nil {
		t.Fatal("seventeenth level accepted")
	}
}
