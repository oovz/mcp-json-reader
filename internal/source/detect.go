package source

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/oovz/mcp-json-reader/v2/internal/core"
)

type Detection struct {
	Basis      string `json:"basis"`
	Confidence string `json:"confidence"`
}

func DetectFormat(path string, sample []byte, maxRecords int) (core.Format, Detection) {
	if len(sample) > 0 && sample[0] == 0x1e {
		return core.FormatJSONSequence, Detection{Basis: "record-separator", Confidence: "high"}
	}
	extension := strings.ToLower(filepath.Ext(path))
	if extension == ".jsonl" || extension == ".ndjson" {
		return core.FormatJSONL, Detection{Basis: "extension", Confidence: "high"}
	}
	if independentlyValidLines(sample, maxRecords) >= 2 {
		return core.FormatJSONL, Detection{Basis: "record-sample", Confidence: "high"}
	}
	return core.FormatJSON, Detection{Basis: "default-json", Confidence: "medium"}
}

func independentlyValidLines(sample []byte, maxRecords int) int {
	if maxRecords <= 0 {
		return 0
	}
	valid := 0
	for len(sample) > 0 && valid < maxRecords {
		line := sample
		if newline := bytes.IndexByte(sample, '\n'); newline >= 0 {
			line = sample[:newline]
			sample = sample[newline+1:]
		} else {
			sample = nil
		}
		line = bytes.TrimSuffix(line, []byte{'\r'})
		if len(line) == 0 || !json.Valid(line) {
			return valid
		}
		valid++
	}
	return valid
}
