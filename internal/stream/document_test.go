package stream

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/oovz/mcp-json-reader/v2/internal/core"
)

func TestValidateDocumentAcceptsEveryStandardJSONRootType(t *testing.T) {
	tests := map[string]RootKind{
		`{"id":1}`: RootObject,
		`[1,2]`:    RootArray,
		`"value"`:  RootString,
		`42`:       RootNumber,
		`true`:     RootBoolean,
		`null`:     RootNull,
	}
	for input, want := range tests {
		info, err := ValidateDocument(context.Background(), strings.NewReader(input), core.DefaultLimits())
		if err != nil {
			t.Fatalf("ValidateDocument(%s) error: %v", input, err)
		}
		if info.RootKind != want {
			t.Fatalf("ValidateDocument(%s) root = %q, want %q", input, info.RootKind, want)
		}
	}
}

func TestValidateDocumentRejectsSecondTopLevelValueAsFormatMismatch(t *testing.T) {
	_, err := ValidateDocument(context.Background(), strings.NewReader("{\"id\":1}\n{\"id\":2}"), core.DefaultLimits())
	if err == nil {
		t.Fatal("ValidateDocument succeeded, want FORMAT_MISMATCH")
	}
	if err.Code != core.CodeFormatMismatch {
		t.Fatalf("error code = %q, want %q", err.Code, core.CodeFormatMismatch)
	}
	if err.ExpectedFormat != core.FormatJSON || len(err.LikelyFormats) != 1 || err.LikelyFormats[0] != core.FormatJSONL {
		t.Fatalf("format diagnosis = %#v, want json -> jsonl", err)
	}
	if err.Retry == nil || err.Retry.Format != core.FormatJSONL {
		t.Fatalf("retry = %#v, want jsonl", err.Retry)
	}
}

func TestValidateDocumentRejectsDuplicateNamesWithPath(t *testing.T) {
	_, err := ValidateDocument(context.Background(), strings.NewReader(`{"a":1,"a":2}`), core.DefaultLimits())
	if err == nil || err.Code != core.CodeSyntax {
		t.Fatalf("error = %#v, want SYNTAX_ERROR", err)
	}
	if err.Location == nil || err.Location.Path != "/a" {
		t.Fatalf("location = %#v, want /a", err.Location)
	}
}

func TestValidateDocumentIdentifiesLikelyJSONCSyntax(t *testing.T) {
	tests := []struct {
		name   string
		source strings.Reader
	}{
		{name: "line comment", source: *strings.NewReader("{\n// current user\n\"name\":\"Alice\"\n}")},
		{name: "block comment", source: *strings.NewReader("{/* current user */\"name\":\"Alice\"}")},
		{name: "comment before root", source: *strings.NewReader("// generated\nnull")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ValidateDocument(context.Background(), &test.source, core.DefaultLimits())
			if err == nil || err.Code != core.CodeUnsupportedSyntax {
				t.Fatalf("error = %#v, want UNSUPPORTED_SYNTAX", err)
			}
			if !strings.Contains(err.Hint, "JSONC") {
				t.Fatalf("hint = %q, want JSONC", err.Hint)
			}
		})
	}
}

func TestValidateDocumentDistinguishesCommentsFromSlashes(t *testing.T) {
	if _, err := ValidateDocument(context.Background(), strings.NewReader(`{"path":"//server/share/*"}`), core.DefaultLimits()); err != nil {
		t.Fatalf("slashes inside a string were classified as comments: %v", err)
	}
	_, err := ValidateDocument(context.Background(), strings.NewReader(`{"value":/}`), core.DefaultLimits())
	if err == nil || err.Code != core.CodeSyntax {
		t.Fatalf("lone slash error = %#v, want SYNTAX_ERROR", err)
	}

	_, err = ValidateDocument(context.Background(), &singleByteReader{data: []byte("{\"id\":1,// split\n\"name\":2}")}, core.DefaultLimits())
	if err == nil || err.Code != core.CodeUnsupportedSyntax {
		t.Fatalf("split comment marker error = %#v, want UNSUPPORTED_SYNTAX", err)
	}
}

type singleByteReader struct {
	data []byte
}

func (reader *singleByteReader) Read(buffer []byte) (int, error) {
	if len(reader.data) == 0 {
		return 0, io.EOF
	}
	buffer[0] = reader.data[0]
	reader.data = reader.data[1:]
	return 1, nil
}

func TestValidateDocumentBoundsRetainedObjectKeyBytes(t *testing.T) {
	limits := core.DefaultLimits()
	limits.MaxObjectKeyBytes = 5
	_, err := ValidateDocument(context.Background(), strings.NewReader(`{"abc":1,"def":2}`), limits)
	if err == nil || err.Code != core.CodeResourceLimit || err.Limit == nil || err.Limit.Name != "max_object_key_bytes" {
		t.Fatalf("error = %#v, want max_object_key_bytes", err)
	}
}

func TestValidateDocumentBoundsKeysAcrossActiveObjectsAndReleasesSiblingBudget(t *testing.T) {
	limits := core.DefaultLimits()
	limits.MaxObjectKeyBytes = 8
	limits.MaxActiveKeyBytes = 3

	_, err := ValidateDocument(context.Background(), strings.NewReader(`{"aa":{"bb":1}}`), limits)
	if err == nil || err.Code != core.CodeResourceLimit || err.Limit == nil || err.Limit.Name != "max_active_key_bytes" {
		t.Fatalf("nested validation error = %#v, want max_active_key_bytes", err)
	}

	limits.MaxActiveKeyBytes = 2
	if _, err := ValidateDocument(context.Background(), strings.NewReader(`[{"aa":1},{"bb":2}]`), limits); err != nil {
		t.Fatalf("sequential sibling objects retained each other's key budget: %v", err)
	}
}
