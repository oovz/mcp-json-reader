package stream

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/oovz/mcp-json-reader/v2/internal/core"
)

func FuzzValidateDocument(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte(`null`),
		[]byte(`{"name":"value"}`),
		[]byte(`[1,true,"text"]`),
		[]byte(`{"duplicate":1,"duplicate":2}`),
		[]byte(`{"unfinished":`),
		{0xff, 0xfe, 0xfd},
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		limits := core.DefaultLimits()
		limits.MaxScanTime = time.Second
		limits.MaxScalarBytes = 64 << 10
		limits.MaxNumberBytes = 1 << 10
		limits.MaxObjectKeyBytes = 64 << 10
		limits.MaxActiveKeyBytes = 64 << 10
		_, _ = ValidateDocument(context.Background(), bytes.NewReader(data), limits)
	})
}

func FuzzRecordFramers(f *testing.F) {
	f.Add(byte(0), []byte("{}\n[]\n"))
	f.Add(byte(0), []byte("unterminated"))
	f.Add(byte(1), []byte("\x1e{}\n\x1e[]\n"))
	f.Add(byte(1), []byte{})
	f.Add(byte(1), []byte("not-a-sequence"))

	f.Fuzz(func(t *testing.T, format byte, data []byte) {
		limits := core.DefaultLimits()
		limits.MaxRecordBytes = 64 << 10
		var next func() (*RecordReader, error)
		if format%2 == 0 {
			framer := NewJSONLFramer(bytes.NewReader(data), limits)
			next = framer.Next
		} else {
			framer := NewJSONSequenceFramer(bytes.NewReader(data), limits)
			next = framer.Next
		}

		for recordCount := 0; recordCount <= len(data)+1; recordCount++ {
			record, err := next()
			if err != nil {
				if !errors.Is(err, io.EOF) {
					var appErr *core.AppError
					_ = errors.As(err, &appErr)
				}
				return
			}
			if _, err = io.Copy(io.Discard, record); err != nil {
				return
			}
		}
		t.Fatal("framer did not terminate within a data-size-derived record bound")
	})
}
