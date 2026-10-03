package main

import (
	"strings"
	"testing"
)

func TestMCPReservedCapacityAndRelease(t *testing.T) {
	a := mcpAdapter{slots: make(chan struct{}, 4), waits: make(chan struct{}, 2)}
	releases := []func(){}
	for i := 0; i < 4; i++ {
		release, err := a.reserve(i < 2)
		if err != nil {
			t.Fatal(err)
		}
		releases = append(releases, release)
		if i == 1 {
			if _, err := a.reserve(true); err == nil || !strings.Contains(err.Error(), "two waits") {
				t.Fatal("wait capacity", err)
			}
		}
	}
	if _, err := a.reserve(false); err == nil || !strings.Contains(err.Error(), "four calls") {
		t.Fatal("total capacity", err)
	}
	for _, release := range releases {
		release()
	}
	if len(a.slots) != 0 || len(a.waits) != 0 {
		t.Fatal("reservation leaked")
	}
	// A wait rejected by the total cap must give its wait reservation back.
	for range 4 {
		r, err := a.reserve(false)
		if err != nil {
			t.Fatal(err)
		}
		defer r()
	}
	if _, err := a.reserve(true); err == nil || len(a.waits) != 0 {
		t.Fatal("failed admission leaked wait permit", err)
	}
}
