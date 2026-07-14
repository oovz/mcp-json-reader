package stream

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/oovz/mcp-json-reader/v2/internal/core"
)

func TestJSONLFramerReturnsOneBoundedRecordAtATime(t *testing.T) {
	framer := NewJSONLFramer(strings.NewReader("{\"id\":1}\r\n{\"id\":2}\n42"), core.DefaultLimits())
	wants := []string{"{\"id\":1}\r", "{\"id\":2}", "42"}
	for index, want := range wants {
		record, err := framer.Next()
		if err != nil {
			t.Fatalf("Next(%d) error: %v", index, err)
		}
		got, readErr := io.ReadAll(record)
		if readErr != nil {
			t.Fatalf("ReadAll(%d) error: %v", index, readErr)
		}
		if string(got) != want {
			t.Fatalf("record %d = %q, want %q", index, got, want)
		}
		if record.Index() != int64(index) {
			t.Fatalf("record index = %d, want %d", record.Index(), index)
		}
	}
	if _, err := framer.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("final Next error = %v, want EOF", err)
	}
}

func TestJSONLFramerStopsBeforeRecordByteLimitIsExceeded(t *testing.T) {
	limits := core.DefaultLimits()
	limits.MaxRecordBytes = 4
	framer := NewJSONLFramer(strings.NewReader("12345\ntrue\n"), limits)
	record, err := framer.Next()
	if err != nil {
		t.Fatalf("Next error: %v", err)
	}
	read, readErr := io.ReadAll(record)
	var appErr *core.AppError
	if !errors.As(readErr, &appErr) || appErr.Code != core.CodeResourceLimit {
		t.Fatalf("ReadAll error = %#v, want RESOURCE_LIMIT_EXCEEDED", readErr)
	}
	if string(read) != "1234" {
		t.Fatalf("returned bytes = %q, want 1234", read)
	}
	if appErr.Limit == nil || appErr.Limit.Name != "max_record_bytes" {
		t.Fatalf("limit = %#v, want max_record_bytes", appErr.Limit)
	}
}

func TestJSONSequenceFramerRequiresInitialRecordSeparator(t *testing.T) {
	framer := NewJSONSequenceFramer(strings.NewReader(`{"id":1}`), core.DefaultLimits())
	_, err := framer.Next()
	var appErr *core.AppError
	if !errors.As(err, &appErr) || appErr.Code != core.CodeFormatMismatch {
		t.Fatalf("Next error = %#v, want FORMAT_MISMATCH", err)
	}
	if appErr.ExpectedFormat != core.FormatJSONSequence {
		t.Fatalf("expected format = %q, want json-seq", appErr.ExpectedFormat)
	}
}

func TestJSONSequenceFramerAcceptsAnEmptySequenceAndPropagatesReadErrors(t *testing.T) {
	framer := NewJSONSequenceFramer(strings.NewReader(""), core.DefaultLimits())
	if _, err := framer.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("empty sequence Next error = %v, want EOF", err)
	}

	sentinel := errors.New("read failed")
	framer = NewJSONSequenceFramer(errorReader{err: sentinel}, core.DefaultLimits())
	if _, err := framer.Next(); !errors.Is(err, sentinel) {
		t.Fatalf("initial read error = %v, want sentinel", err)
	}
}

func TestJSONSequenceFramerExposesAnEmptyRecordBetweenConsecutiveRS(t *testing.T) {
	input := string([]byte{0x1e}) + "{\"id\":1}\n" + string([]byte{0x1e, 0x1e}) + "42\n"
	framer := NewJSONSequenceFramer(strings.NewReader(input), core.DefaultLimits())
	for index, want := range []string{"{\"id\":1}\n", "", "42\n"} {
		record, err := framer.Next()
		if err != nil {
			t.Fatalf("Next(%d) error: %v", index, err)
		}
		got, readErr := io.ReadAll(record)
		if readErr != nil {
			t.Fatalf("ReadAll(%d) error: %v", index, readErr)
		}
		if string(got) != want {
			t.Fatalf("record %d = %q, want %q", index, got, want)
		}
	}
	if _, err := framer.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("final Next error = %v, want EOF", err)
	}
}

type errorReader struct {
	err error
}

func (reader errorReader) Read([]byte) (int, error) {
	return 0, reader.err
}
