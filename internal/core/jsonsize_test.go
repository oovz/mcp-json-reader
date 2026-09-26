package core

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestJSONSizeMatchesEncodingBeforeAllocation(t *testing.T) {
	for _, value := range []any{nil, true, false, json.Number("1e999"), "<>&\n\"\\\u2028\u2029", string([]byte{255}), []any(nil), map[string]any(nil), []any{}, map[string]any{}, map[string]any{"/a~<\n": []any{json.Number("9007199254740993"), "😀"}}} {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		size, err := JSONSize(value, int64(len(encoded)))
		if err != nil || size != int64(len(encoded)) {
			t.Fatalf("%#v: size=%d error=%v, encoded=%s", value, size, err, encoded)
		}
		if _, err := JSONSize(value, int64(len(encoded)-1)); !errors.Is(err, ErrJSONSize) {
			t.Fatalf("expected cap rejection: %v", err)
		}
	}
}

func TestPointerPreflightMatchesEncoding(t *testing.T) {
	for _, path := range []Path{nil, {PropertySegment("/~<&\n\"\\😀"), IndexSegment(123)}, {PropertySegment("")}} {
		encoded, err := json.Marshal(path.Pointer())
		if err != nil {
			t.Fatal(err)
		}
		if got := path.PointerJSONSize(); got != int64(len(encoded)) {
			t.Fatalf("size=%d want=%d path=%s", got, len(encoded), encoded)
		}
	}
}

func TestStrictUnicodeStrings(t *testing.T) {
	for _, raw := range []string{`"\uD800"`, `"\uDC00"`, `"\uD800x"`, `"\uD800\u0041"`, `"\uDC00\uD800"`} {
		var value string
		if err := UnmarshalStrictString([]byte(raw), &value); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	var value string
	if err := UnmarshalStrictString([]byte(`"\uD83D\uDE00"`), &value); err != nil || value != "😀" {
		t.Fatalf("value=%q error=%v", value, err)
	}
}
