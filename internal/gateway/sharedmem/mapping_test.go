package sharedmem

import (
	"errors"
	"strings"
	"testing"
)

func TestOpenExistingRejectsInvalidNameAndSize(t *testing.T) {
	for _, name := range []string{"", "x", "/", "/.", "/..", "/nested/name", "/with space", "/" + strings.Repeat("a", maxNameBytes)} {
		if _, err := OpenExisting(name, 1); !errors.Is(err, ErrInvalid) {
			t.Errorf("OpenExisting(%q) error = %v, want ErrInvalid", name, err)
		}
	}
	for _, size := range []int{0, -1, MaxMappingBytes + 1} {
		if _, err := OpenExisting("/bounded", size); !errors.Is(err, ErrInvalid) {
			t.Errorf("OpenExisting size %d error = %v, want ErrInvalid", size, err)
		}
	}
}

func TestNilMappingAccessAndClose(t *testing.T) {
	var m *Mapping
	if m.Bytes() != nil {
		t.Fatal("nil mapping returned bytes")
	}
	if err := m.Close(); err != nil {
		t.Fatal("nil mapping close:", err)
	}
}
