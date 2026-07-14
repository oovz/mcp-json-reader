package stream

import (
	"bufio"
	"errors"
	"io"

	"github.com/oovz/mcp-json-reader/v2/internal/core"
)

const recordBufferSize = 32 << 10

// RecordReader exposes exactly one framed record. The delimiter is consumed by
// the framer and is never returned to the JSON decoder.
type RecordReader struct {
	reader    *bufio.Reader
	delimiter byte
	index     int64
	limit     int64
	read      int64
	lastByte  byte
	hasByte   bool
	sample    []byte
	firstByte byte
	hasFirst  bool

	pending []byte
	nextErr error
	done    bool

	onDelimiter func()
	onEOF       func()
}

func (record *RecordReader) Index() int64 { return record.index }

func (record *RecordReader) BytesRead() int64 { return record.read }

func (record *RecordReader) EndsWithJSONWhitespace() bool {
	if !record.hasByte {
		return false
	}
	switch record.lastByte {
	case ' ', '\t', '\r', '\n':
		return true
	default:
		return false
	}
}

func (record *RecordReader) Sample() string { return string(record.sample) }

func (record *RecordReader) StartsWithNumber() bool {
	return record.hasFirst && (record.firstByte == '-' || record.firstByte >= '0' && record.firstByte <= '9')
}

func (record *RecordReader) Read(buffer []byte) (int, error) {
	if len(buffer) == 0 {
		return 0, nil
	}
	for len(record.pending) == 0 {
		if record.nextErr != nil {
			err := record.nextErr
			record.nextErr = nil
			return 0, err
		}
		if record.done {
			return 0, io.EOF
		}
		record.fill()
	}

	count := copy(buffer, record.pending)
	record.pending = record.pending[count:]
	return count, nil
}

func (record *RecordReader) fill() {
	fragment, err := record.reader.ReadSlice(record.delimiter)
	if err == nil {
		fragment = fragment[:len(fragment)-1]
		record.done = true
		if record.onDelimiter != nil {
			record.onDelimiter()
		}
	} else if errors.Is(err, io.EOF) {
		record.done = true
		if record.onEOF != nil {
			record.onEOF()
		}
	} else if !errors.Is(err, bufio.ErrBufferFull) {
		record.done = true
		record.nextErr = err
	}

	remaining := record.limit - record.read
	if int64(len(fragment)) > remaining {
		if remaining > 0 {
			fragment = fragment[:remaining]
		} else {
			fragment = nil
		}
		record.read = record.limit
		record.done = true
		record.nextErr = &core.AppError{
			Code:    core.CodeResourceLimit,
			Message: "max_record_bytes exceeded",
			Location: &core.Location{
				ByteOffset:  core.Int64(record.limit),
				RecordIndex: core.Int64(record.index),
			},
			Limit: &core.LimitDetail{Name: "max_record_bytes", Limit: record.limit, Value: record.limit + 1},
		}
	} else {
		record.read += int64(len(fragment))
	}
	if len(fragment) > 0 {
		record.lastByte = fragment[len(fragment)-1]
		record.hasByte = true
		if !record.hasFirst {
			for _, value := range fragment {
				switch value {
				case ' ', '\t', '\r', '\n':
					continue
				default:
					record.firstByte = value
					record.hasFirst = true
				}
				break
			}
		}
		if len(record.sample) < 256 {
			remainingSample := 256 - len(record.sample)
			if len(fragment) < remainingSample {
				remainingSample = len(fragment)
			}
			record.sample = append(record.sample, fragment[:remainingSample]...)
		}
	}
	record.pending = fragment
}

type JSONLFramer struct {
	reader    *bufio.Reader
	limit     int64
	nextIndex int64
	current   *RecordReader
	exhausted bool
}

func NewJSONLFramer(source io.Reader, limits core.Limits) *JSONLFramer {
	return &JSONLFramer{reader: bufio.NewReaderSize(source, recordBufferSize), limit: limits.MaxRecordBytes}
}

func (framer *JSONLFramer) Next() (*RecordReader, error) {
	if framer.current != nil && !framer.current.done {
		return nil, &core.AppError{Code: core.CodeInternal, Message: "previous JSONL record was not fully consumed"}
	}
	if framer.exhausted {
		return nil, io.EOF
	}
	if _, err := framer.reader.Peek(1); err != nil {
		if errors.Is(err, io.EOF) {
			framer.exhausted = true
			return nil, io.EOF
		}
		return nil, err
	}

	record := &RecordReader{
		reader:    framer.reader,
		delimiter: '\n',
		index:     framer.nextIndex,
		limit:     framer.limit,
		onEOF:     func() { framer.exhausted = true },
	}
	framer.nextIndex++
	framer.current = record
	return record, nil
}

type JSONSequenceFramer struct {
	reader    *bufio.Reader
	limit     int64
	nextIndex int64
	current   *RecordReader
	started   bool
	pendingRS bool
	exhausted bool
}

func NewJSONSequenceFramer(source io.Reader, limits core.Limits) *JSONSequenceFramer {
	return &JSONSequenceFramer{reader: bufio.NewReaderSize(source, recordBufferSize), limit: limits.MaxRecordBytes}
}

func (framer *JSONSequenceFramer) Next() (*RecordReader, error) {
	if framer.current != nil && !framer.current.done {
		return nil, &core.AppError{Code: core.CodeInternal, Message: "previous JSON sequence record was not fully consumed"}
	}
	if framer.exhausted {
		return nil, io.EOF
	}

	if !framer.started {
		value, err := framer.reader.ReadByte()
		if errors.Is(err, io.EOF) {
			framer.exhausted = true
			return nil, io.EOF
		}
		if err != nil {
			return nil, err
		}
		if value != 0x1e {
			return nil, jsonSequenceMismatch()
		}
		framer.started = true
	} else if framer.pendingRS {
		framer.pendingRS = false
	} else {
		return nil, io.EOF
	}

	record := &RecordReader{
		reader:      framer.reader,
		delimiter:   0x1e,
		index:       framer.nextIndex,
		limit:       framer.limit,
		onDelimiter: func() { framer.pendingRS = true },
		onEOF:       func() { framer.exhausted = true },
	}
	framer.nextIndex++
	framer.current = record
	return record, nil
}

func jsonSequenceMismatch() *core.AppError {
	return &core.AppError{
		Code:           core.CodeFormatMismatch,
		Message:        "expected a JSON sequence record separator at byte 0",
		ExpectedFormat: core.FormatJSONSequence,
		LikelyFormats:  []core.Format{core.FormatJSON, core.FormatJSONL},
		Location:       &core.Location{ByteOffset: core.Int64(0), Line: core.Int64(1), Column: core.Int64(1)},
	}
}
