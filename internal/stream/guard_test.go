package stream

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/oovz/mcp-json-reader/v2/internal/core"
)

func TestGuardReaderStopsOversizedStringBeforeReturningTheExcessByte(t *testing.T) {
	limits := core.DefaultLimits()
	limits.MaxScalarBytes = 4
	limits.MaxNumberBytes = 4

	guard := NewGuardReader(strings.NewReader(`"abcde"`), limits)
	read, err := io.ReadAll(guard)
	if err == nil {
		t.Fatal("ReadAll succeeded, want a resource limit error")
	}
	var appErr *core.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("error type = %T, want *core.AppError", err)
	}
	if appErr.Code != core.CodeResourceLimit {
		t.Fatalf("error code = %q, want %q", appErr.Code, core.CodeResourceLimit)
	}
	if appErr.Limit == nil || appErr.Limit.Name != "max_scalar_bytes" {
		t.Fatalf("limit = %#v, want max_scalar_bytes", appErr.Limit)
	}
	if strings.Contains(string(read), "e") {
		t.Fatalf("guard returned the byte that exceeded the limit: %q", read)
	}
}

func TestGuardReaderEnforcesNumberDepthAndMemberLimits(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		configure func(*core.Limits)
		limitName string
		excess    string
	}{
		{
			name:  "number",
			input: `12345`,
			configure: func(limits *core.Limits) {
				limits.MaxNumberBytes = 4
			},
			limitName: "max_number_bytes",
			excess:    "5",
		},
		{
			name:  "depth",
			input: `[[[]]]`,
			configure: func(limits *core.Limits) {
				limits.MaxDepth = 2
			},
			limitName: "max_depth",
			excess:    "[[[",
		},
		{
			name:  "object members",
			input: `{"a":1,"b":2}`,
			configure: func(limits *core.Limits) {
				limits.MaxObjectMembers = 1
			},
			limitName: "max_object_members",
			excess:    `"b":`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			limits := core.DefaultLimits()
			test.configure(&limits)
			read, err := io.ReadAll(NewGuardReader(strings.NewReader(test.input), limits))
			var appErr *core.AppError
			if !errors.As(err, &appErr) || appErr.Code != core.CodeResourceLimit {
				t.Fatalf("error = %#v, want RESOURCE_LIMIT_EXCEEDED", err)
			}
			if appErr.Limit == nil || appErr.Limit.Name != test.limitName {
				t.Fatalf("limit = %#v, want %s", appErr.Limit, test.limitName)
			}
			if strings.Contains(string(read), test.excess) {
				t.Fatalf("guard exposed the limit-crossing byte: %q", read)
			}
		})
	}
}

func TestGuardReaderRejectsInvalidUTF8AcrossSmallReads(t *testing.T) {
	input := &oneByteReader{reader: bytes.NewReader([]byte{'"', 0xe2, 0x28, 0xa1, '"'})}
	_, err := io.ReadAll(NewGuardReader(input, core.DefaultLimits()))
	var appErr *core.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("error type = %T, want *core.AppError", err)
	}
	if appErr.Code != core.CodeSyntax {
		t.Fatalf("error code = %q, want %q", appErr.Code, core.CodeSyntax)
	}
	if appErr.Location == nil || appErr.Location.ByteOffset == nil || *appErr.Location.ByteOffset != 2 {
		t.Fatalf("location = %#v, want byte offset 2", appErr.Location)
	}
}

func TestGuardReaderIgnoresStructuralBytesInsideStrings(t *testing.T) {
	input := `{"text":"{{{:::}}}","value":[1]}`
	limits := core.DefaultLimits()
	limits.MaxDepth = 2
	limits.MaxObjectMembers = 2
	got, err := io.ReadAll(NewGuardReader(&oneByteReader{reader: strings.NewReader(input)}, limits))
	if err != nil {
		t.Fatalf("ReadAll error: %v", err)
	}
	if string(got) != input {
		t.Fatalf("ReadAll = %q, want %q", got, input)
	}
}

type oneByteReader struct {
	reader io.Reader
}

func (reader *oneByteReader) Read(buffer []byte) (int, error) {
	if len(buffer) > 1 {
		buffer = buffer[:1]
	}
	return reader.reader.Read(buffer)
}
