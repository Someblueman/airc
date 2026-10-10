package jsoninput

import (
	"strings"
	"testing"
)

func TestCallerNestingBound(t *testing.T) {
	deep := []byte(strings.Repeat("[", 9) + "0" + strings.Repeat("]", 9))
	if Validate(deep, 1024) == nil {
		t.Fatal("protocol depth expanded")
	}
	if err := validateDepth(deep, 1024, 10); err != nil {
		t.Fatal(err)
	}
	if validateDepth([]byte(strings.Repeat("[", 11)+"0"+strings.Repeat("]", 11)), 1024, 10) == nil {
		t.Fatal("configuration bound bypassed")
	}
	var dst struct {
		Value string `json:"value"`
	}
	for _, limit := range []int{0, 17} {
		if DecodeDepth([]byte(`{"value":"ok"}`), &dst, 1024, limit) == nil {
			t.Fatal("invalid bound accepted")
		}
	}
	for _, data := range []string{`{"value":"a","value":"b"}`, `{"Value":"a"}`, `{"value":null}`, `{"unknown":"a"}`} {
		if DecodeDepth([]byte(data), &dst, 1024, 10) == nil {
			t.Fatal("strict schema weakened", data)
		}
	}
}
