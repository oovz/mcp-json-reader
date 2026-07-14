package source

import (
	"testing"

	"github.com/oovz/mcp-json-reader/v2/internal/core"
)

func TestDetectFormatUsesDeterministicBoundedRules(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		sample []byte
		want   core.Format
		basis  string
	}{
		{"record separator", "data.bin", append([]byte{0x1e}, []byte("{\"id\":1}\n")...), core.FormatJSONSequence, "record-separator"},
		{"jsonl extension", "data.ndjson", []byte("{\"id\":1}\n"), core.FormatJSONL, "extension"},
		{"multiple valid lines", "data.txt", []byte("{\"id\":1}\n{\"id\":2}\n"), core.FormatJSONL, "record-sample"},
		{"ambiguous one line", "data.txt", []byte("{\"id\":1}\n"), core.FormatJSON, "default-json"},
		{"pretty JSON", "data.txt", []byte("{\n  \"id\": 1\n}\n"), core.FormatJSON, "default-json"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			format, detection := DetectFormat(test.path, test.sample, 32)
			if format != test.want || detection.Basis != test.basis {
				t.Fatalf("DetectFormat = %q %#v, want %q basis %q", format, detection, test.want, test.basis)
			}
		})
	}
}
