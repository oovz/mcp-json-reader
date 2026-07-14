package core

import "testing"

func TestParseFormatAcceptsCanonicalNames(t *testing.T) {
	tests := map[string]Format{
		"auto":     FormatAuto,
		"json":     FormatJSON,
		"jsonl":    FormatJSONL,
		"json-seq": FormatJSONSequence,
	}
	for input, want := range tests {
		t.Run(input, func(t *testing.T) {
			got, err := ParseFormat(input)
			if err != nil {
				t.Fatalf("ParseFormat(%q) error: %v", input, err)
			}
			if got != want {
				t.Fatalf("ParseFormat(%q) = %q, want %q", input, got, want)
			}
		})
	}
}

func TestParseFormatRejectsUnsupportedName(t *testing.T) {
	_, err := ParseFormat("ndjson")
	if err == nil {
		t.Fatal("ParseFormat(ndjson) succeeded, want an error")
	}
	if err.Code != CodeUnsupportedFormat {
		t.Fatalf("error code = %q, want %q", err.Code, CodeUnsupportedFormat)
	}
}
