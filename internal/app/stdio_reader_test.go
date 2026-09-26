package app

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
)

type chunkedReadCloser struct {
	data  []byte
	chunk int
}

func (reader *chunkedReadCloser) Read(p []byte) (int, error) {
	if len(reader.data) == 0 {
		return 0, io.EOF
	}
	count := reader.chunk
	if count > len(p) {
		count = len(p)
	}
	if count > len(reader.data) {
		count = len(reader.data)
	}
	copy(p[:count], reader.data[:count])
	reader.data = reader.data[count:]
	return count, nil
}

func (reader *chunkedReadCloser) Close() error { return nil }

func TestBoundedStdioReaderPreservesFramesAcrossReadChunking(t *testing.T) {
	for _, chunk := range []int{1, 2, 32, 4096} {
		t.Run(fmt.Sprintf("chunk-%d", chunk), func(t *testing.T) {
			input := []byte("{\"id\":1}\n{\"id\":2}\n")
			reader := newBoundedStdioReader(&chunkedReadCloser{data: input, chunk: chunk}, 1024)
			decoder := json.NewDecoder(reader)
			var first, second map[string]int
			if err := decoder.Decode(&first); err != nil {
				t.Fatalf("first frame: %v", err)
			}
			if err := decoder.Decode(&second); err != nil {
				t.Fatalf("second frame: %v", err)
			}
			if first["id"] != 1 || second["id"] != 2 {
				t.Fatalf("frames=%v,%v", first, second)
			}
		})
	}
}

func TestBoundedStdioReaderAcceptsMixedLFAndCRLFFrames(t *testing.T) {
	for _, separators := range []struct {
		name          string
		first, second string
	}{
		{name: "lf-lf", first: "\n", second: "\n"},
		{name: "lf-crlf", first: "\n", second: "\r\n"},
		{name: "crlf-lf", first: "\r\n", second: "\n"},
		{name: "crlf-crlf", first: "\r\n", second: "\r\n"},
	} {
		t.Run(separators.name, func(t *testing.T) {
			input := "{\"id\":1}" + separators.first + "{\"id\":2}" + separators.second
			reader := newBoundedStdioReader(io.NopCloser(strings.NewReader(input)), 1024)
			decoder := json.NewDecoder(reader)
			for want := 1; want <= 2; want++ {
				var value map[string]int
				if err := decoder.Decode(&value); err != nil || value["id"] != want {
					t.Fatalf("frame=%v error=%v", value, err)
				}
			}
		})
	}
}

func TestBoundedStdioReaderBehavesAsOneStream(t *testing.T) {
	reader := newBoundedStdioReader(io.NopCloser(strings.NewReader("{\"value\":123}\n{\"value\":456}\n")), 1024)
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("stream read: %v", err)
	}
	if got := string(data); got != "{\"value\":123}\n{\"value\":456}\n" {
		t.Fatalf("stream=%q", got)
	}
}

func TestBoundedStdioReaderRejectsFramePastLimitBeforeDecode(t *testing.T) {
	frame := `{"jsonrpc":"2.0","method":"notifications/progress","params":{"padding":"1234567890"}}` + "\n"
	reader := newBoundedStdioReader(io.NopCloser(strings.NewReader(frame)), len(frame)-1)
	var value any
	if err := json.NewDecoder(reader).Decode(&value); err == nil || !strings.Contains(err.Error(), "maximum line length") {
		t.Fatalf("oversized frame error=%v", err)
	}
}

func TestBoundedStdioReaderEnforcesLMinusOneLAndLPlusOne(t *testing.T) {
	const limit = 128
	prefix := `{"jsonrpc":"2.0","method":"notifications/progress","params":{"padding":"`
	suffix := `"}}` + "\n"
	for _, delta := range []int{-1, 0, 1} {
		target := limit + delta
		padding := target - len(prefix) - len(suffix)
		if padding < 0 {
			t.Fatalf("test frame is larger than boundary: %d", padding)
		}
		frame := prefix + strings.Repeat("x", padding) + suffix
		reader := newBoundedStdioReader(io.NopCloser(strings.NewReader(frame)), limit)
		var value any
		err := json.NewDecoder(reader).Decode(&value)
		if delta <= 0 && err != nil {
			t.Fatalf("L%+d frame rejected: %v", delta, err)
		}
		if delta > 0 && (err == nil || !strings.Contains(err.Error(), "maximum line length")) {
			t.Fatalf("L+1 frame error=%v", err)
		}
	}
}

func TestBoundedStdioReaderReportsTruncatedFrame(t *testing.T) {
	reader := newBoundedStdioReader(io.NopCloser(strings.NewReader(`{"jsonrpc":"2.0"`)), 1024)
	var value any
	if err := json.NewDecoder(reader).Decode(&value); err == nil {
		t.Fatal("truncated frame unexpectedly decoded")
	}
}

func TestBoundedStdioReaderAcceptsFinalFrameWithoutNewline(t *testing.T) {
	reader := newBoundedStdioReader(io.NopCloser(strings.NewReader(`{"id":1}`)), 1024)
	var value map[string]int
	if err := json.NewDecoder(reader).Decode(&value); err != nil || value["id"] != 1 {
		t.Fatalf("frame=%v error=%v", value, err)
	}
}

func TestBoundedStdioReaderRejectsInvalidPhysicalFramesBeforeDecode(t *testing.T) {
	for _, input := range []string{
		"{\n\"id\":1}\n",
		"{\"id\":1} {\"id\":2}\n",
		"\n{\"id\":1}\n",
		"{\"id\":1}\r{\"id\":2}\n",
		"[{\"id\":1}]\n",
	} {
		for _, chunk := range []int{1, 4096} {
			reader := newBoundedStdioReader(&chunkedReadCloser{data: []byte(input), chunk: chunk}, 1024)
			var value any
			if err := json.NewDecoder(reader).Decode(&value); err == nil {
				t.Fatalf("decoded invalid physical frame %q with chunk %d: %v", input, chunk, value)
			}
		}
	}
}

func TestBoundedStdioReaderRejectsOversizedTrailingWhitespaceBeforeDecode(t *testing.T) {
	const limit = 8192
	input := "{\"id\":1}" + strings.Repeat(" ", limit) + "\n"
	reader := newBoundedStdioReader(io.NopCloser(strings.NewReader(input)), limit)
	var value any
	if err := json.NewDecoder(reader).Decode(&value); err == nil {
		t.Fatalf("decoded a value before checking its entire frame: %v", value)
	}
}
