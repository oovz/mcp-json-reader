package stream

import (
	"context"
	"strings"
	"testing"

	"github.com/oovz/mcp-json-reader/v2/internal/core"
)

func TestValidateSourceTreatsJSONLAsAVirtualArray(t *testing.T) {
	input := "{\"id\":1}\r\n{\"id\":2}\n42"
	summary, err := ValidateSource(context.Background(), strings.NewReader(input), core.FormatJSONL, core.DefaultLimits())
	if err != nil {
		t.Fatalf("ValidateSource error: %v", err)
	}
	if summary.RootKind != RootArray || summary.Records != 3 {
		t.Fatalf("summary = %#v, want virtual array with 3 records", summary)
	}
}

func TestValidateSourceRejectsBlankJSONLRecord(t *testing.T) {
	_, err := ValidateSource(context.Background(), strings.NewReader("{\"id\":1}\n\n{\"id\":2}\n"), core.FormatJSONL, core.DefaultLimits())
	if err == nil || err.Code != core.CodeFormatMismatch {
		t.Fatalf("error = %#v, want FORMAT_MISMATCH", err)
	}
	if err.Location == nil || err.Location.RecordIndex == nil || *err.Location.RecordIndex != 1 {
		t.Fatalf("location = %#v, want record 1", err.Location)
	}
}

func TestValidateSourceDiagnosesPrettyJSONPassedAsJSONL(t *testing.T) {
	input := "{\n  \"id\": 1,\n  \"name\": \"A\"\n}\n"
	_, err := ValidateSource(context.Background(), strings.NewReader(input), core.FormatJSONL, core.DefaultLimits())
	if err == nil || err.Code != core.CodeFormatMismatch {
		t.Fatalf("error = %#v, want FORMAT_MISMATCH", err)
	}
	if len(err.LikelyFormats) != 1 || err.LikelyFormats[0] != core.FormatJSON {
		t.Fatalf("likely formats = %#v, want json", err.LikelyFormats)
	}
}

func TestValidateSourceRequiresWhitespaceAfterJSONSequenceNumber(t *testing.T) {
	withoutLF := string([]byte{0x1e}) + "42"
	_, err := ValidateSource(context.Background(), strings.NewReader(withoutLF), core.FormatJSONSequence, core.DefaultLimits())
	if err == nil || err.Code != core.CodeSyntax {
		t.Fatalf("error = %#v, want SYNTAX_ERROR", err)
	}
	withLF := withoutLF + "\n"
	if _, err := ValidateSource(context.Background(), strings.NewReader(withLF), core.FormatJSONSequence, core.DefaultLimits()); err != nil {
		t.Fatalf("number followed by LF error: %v", err)
	}
}

func TestValidateSourceAcceptsAnEmptyJSONSequence(t *testing.T) {
	summary, err := ValidateSource(context.Background(), strings.NewReader(""), core.FormatJSONSequence, core.DefaultLimits())
	if err != nil {
		t.Fatalf("ValidateSource empty JSON sequence error: %v", err)
	}
	if summary.RootKind != RootArray || summary.Records != 0 {
		t.Fatalf("summary = %#v, want empty virtual array", summary)
	}
}

func TestValidateSourceAbortsMalformedJSONSequenceRecord(t *testing.T) {
	input := string([]byte{0x1e}) + "{\"id\":1}\n" + string([]byte{0x1e}) + "{bad}\n"
	_, err := ValidateSource(context.Background(), strings.NewReader(input), core.FormatJSONSequence, core.DefaultLimits())
	if err == nil || err.Code != core.CodeSyntax {
		t.Fatalf("error = %#v, want SYNTAX_ERROR", err)
	}
	if err.Location == nil || err.Location.RecordIndex == nil || *err.Location.RecordIndex != 1 {
		t.Fatalf("location = %#v, want record 1", err.Location)
	}
}

func TestValidateSourceRejectsEmptyJSONSequenceRecord(t *testing.T) {
	input := string([]byte{0x1e}) + "{\"id\":1}\n" + string([]byte{0x1e, 0x1e}) + "{\"id\":2}\n"
	_, err := ValidateSource(context.Background(), strings.NewReader(input), core.FormatJSONSequence, core.DefaultLimits())
	if err == nil || err.Code != core.CodeSyntax || err.Location == nil || err.Location.RecordIndex == nil || *err.Location.RecordIndex != 1 {
		t.Fatalf("error = %#v, want record 1 SYNTAX_ERROR", err)
	}
}
