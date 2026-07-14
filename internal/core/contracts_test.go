package core

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDefaultLimitsArePositiveAndInternallyValid(t *testing.T) {
	limits := DefaultLimits()
	if err := limits.Validate(); err != nil {
		t.Fatalf("DefaultLimits().Validate() error: %v", err)
	}
	if limits.MaxNumberBytes > limits.MaxScalarBytes {
		t.Fatalf("MaxNumberBytes %d exceeds MaxScalarBytes %d", limits.MaxNumberBytes, limits.MaxScalarBytes)
	}
	if limits.MaxItems <= 0 || limits.MaxResultBytes <= 0 || limits.MaxScanTime <= 0 {
		t.Fatal("result and scan limits must be positive")
	}
}

func TestParseQueryLanguageAcceptsOnlyCanonicalNames(t *testing.T) {
	for input, want := range map[string]QueryLanguage{
		"pointer":  QueryPointer,
		"jsonpath": QueryJSONPath,
	} {
		got, err := ParseQueryLanguage(input)
		if err != nil {
			t.Fatalf("ParseQueryLanguage(%q) error: %v", input, err)
		}
		if got != want {
			t.Fatalf("ParseQueryLanguage(%q) = %q, want %q", input, got, want)
		}
	}
	if _, err := ParseQueryLanguage("jq"); err == nil || err.Code != CodeQuerySyntax {
		t.Fatalf("ParseQueryLanguage(jq) error = %#v, want %s", err, CodeQuerySyntax)
	}
}

func TestParseValidationModeDefaultsToProbe(t *testing.T) {
	for input, want := range map[string]ValidationMode{
		"":      ValidationProbe,
		"probe": ValidationProbe,
		"full":  ValidationFull,
	} {
		got, err := ParseValidationMode(input)
		if err != nil {
			t.Fatalf("ParseValidationMode(%q) error: %v", input, err)
		}
		if got != want {
			t.Fatalf("ParseValidationMode(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestAppErrorMarshalsStableStructuredFields(t *testing.T) {
	err := &AppError{
		Code:           CodeFormatMismatch,
		Message:        "expected one JSON document",
		ExpectedFormat: FormatJSON,
		LikelyFormats:  []Format{FormatJSONL},
		Location:       &Location{ByteOffset: Int64(184), Line: Int64(2), Column: Int64(1)},
		Retry:          &Retry{Format: FormatJSONL},
	}
	encoded, marshalErr := json.Marshal(err)
	if marshalErr != nil {
		t.Fatalf("json.Marshal(AppError) error: %v", marshalErr)
	}
	text := string(encoded)
	for _, want := range []string{
		`"code":"FORMAT_MISMATCH"`,
		`"expected_format":"json"`,
		`"likely_formats":["jsonl"]`,
		`"byte_offset":184`,
		`"line":2`,
		`"column":1`,
		`"retry":{"format":"jsonl"}`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("encoded error %s does not contain %s", text, want)
		}
	}
	if strings.Contains(text, "record_index") {
		t.Fatalf("encoded error %s contains unknown record_index", text)
	}
}
