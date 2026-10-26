package jsoninput

import "testing"

func TestValidateSharedBoundary(t *testing.T) {
	for _, test := range []struct {
		name string
		data string
		ok   bool
	}{
		{"scalar", `null`, true},
		{"duplicate", `{"x":1,"x":2}`, false},
		{"trailing", `{} {}`, false},
		{"surrogate", `"\ud800"`, false},
		{"nested", `[[[[[[[[[0]]]]]]]]]`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := Validate([]byte(test.data), 1024) == nil; got != test.ok {
				t.Fatalf("Validate(%q) ok=%v want %v", test.data, got, test.ok)
			}
		})
	}
}
