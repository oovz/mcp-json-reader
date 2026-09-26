package app

import (
	"bufio"
	"bytes"
	"encoding/json/jsontext"
	"errors"
	"io"
)

var errStdioFrameTooLarge = errors.New("inbound JSON-RPC frame exceeded the configured maximum line length")
var errInvalidStdioFrame = errors.New("inbound JSON-RPC frame must contain one complete JSON object")

// boundedStdioReader checks a complete physical frame before exposing any of it
// to the SDK decoder. Both the retained frame and decoder input are bounded.
type boundedStdioReader struct {
	source  *bufio.Reader
	closer  io.Closer
	limit   int
	frame   []byte
	pending []byte
	err     error
}

func newBoundedStdioReader(source io.ReadCloser, limit int) *boundedStdioReader {
	return &boundedStdioReader{source: bufio.NewReader(source), closer: source, limit: limit}
}

func (reader *boundedStdioReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if len(reader.pending) == 0 {
		if reader.err != nil {
			return 0, reader.err
		}
		if err := reader.readFrame(); err != nil {
			reader.err = err
			return 0, err
		}
	}
	n := copy(p, reader.pending)
	reader.pending = reader.pending[n:]
	return n, nil
}

func (reader *boundedStdioReader) readFrame() error {
	reader.frame = reader.frame[:0]
	for {
		fragment, err := reader.source.ReadSlice('\n')
		if len(fragment) > reader.limit-len(reader.frame) {
			return errStdioFrameTooLarge
		}
		reader.frame = append(reader.frame, fragment...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		if len(reader.frame) == 0 && errors.Is(err, io.EOF) {
			return io.EOF
		}
		value := bytes.TrimSpace(reader.frame)
		// Tool argument validation reports duplicate properties as structured
		// errors. Framing preserves those bytes for the argument validator.
		if len(value) == 0 || value[0] != '{' || !jsontext.Value(reader.frame).IsValid(jsontext.AllowDuplicateNames(true)) {
			return errInvalidStdioFrame
		}
		reader.pending = reader.frame
		// The SDK checks the byte following the object for CR/LF. Normalize
		// trailing JSON whitespace only after charging the original full frame.
		end := len(bytes.TrimRight(reader.frame, " \t\r\n"))
		if end < len(reader.frame) {
			reader.frame[end] = '\n'
			reader.pending = reader.frame[:end+1]
		}
		return nil
	}
}

func (reader *boundedStdioReader) Close() error {
	if reader.closer == nil {
		return nil
	}
	return reader.closer.Close()
}

type noCloseWriter struct{ io.Writer }

func (noCloseWriter) Close() error { return nil }
