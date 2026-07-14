package stream

import (
	"bufio"
	"io"

	"github.com/oovz/mcp-json-reader/v2/internal/core"
)

const guardReadChunk = 32 << 10

// GuardReader enforces lexical limits and recognizes unsupported comment
// markers before exposing a bounded chunk to the structural JSON decoder.
type GuardReader struct {
	source *bufio.Reader
	limits core.Limits

	offset int64
	line   int64
	column int64

	inString   bool
	escaped    bool
	stringSize int64
	inNumber   bool
	numberSize int64
	containers []guardContainer

	utf8Expected int
	utf8Min      byte
	utf8Max      byte
	terminal     error
}

type guardContainer struct {
	kind    byte
	members int64
}

func NewGuardReader(source io.Reader, limits core.Limits) *GuardReader {
	return &GuardReader{source: bufio.NewReaderSize(source, guardReadChunk), limits: limits, line: 1, column: 1}
}

func (reader *GuardReader) Read(buffer []byte) (int, error) {
	if reader.terminal != nil {
		return 0, reader.terminal
	}
	if len(buffer) == 0 {
		return 0, nil
	}
	if len(buffer) > guardReadChunk {
		buffer = buffer[:guardReadChunk]
	}

	count, sourceErr := reader.source.Read(buffer)
	for index, value := range buffer[:count] {
		if !reader.inString && value == '/' && reader.startsComment(buffer[:count], index) {
			reader.terminal = reader.unsupportedCommentError()
			if index == 0 {
				return 0, reader.terminal
			}
			return index, nil
		}
		if scanErr := reader.scanByte(value); scanErr != nil {
			reader.terminal = scanErr
			if index == 0 {
				return 0, scanErr
			}
			return index, nil
		}
		reader.advance(value)
	}
	if sourceErr == io.EOF && reader.utf8Expected != 0 {
		reader.terminal = reader.syntaxError("invalid UTF-8: truncated code point")
		if count == 0 {
			return 0, reader.terminal
		}
		return count, nil
	}
	return count, sourceErr
}

func (reader *GuardReader) startsComment(buffer []byte, index int) bool {
	if index+1 < len(buffer) {
		return buffer[index+1] == '/' || buffer[index+1] == '*'
	}
	next, err := reader.source.Peek(1)
	return err == nil && (next[0] == '/' || next[0] == '*')
}

func (reader *GuardReader) scanByte(value byte) error {
	if err := reader.scanUTF8(value); err != nil {
		return err
	}

	if !reader.inString {
		if reader.inNumber {
			if isNumberByte(value) {
				reader.numberSize++
				if reader.numberSize > reader.limits.MaxNumberBytes {
					return reader.limitError("max_number_bytes", reader.limits.MaxNumberBytes, reader.numberSize)
				}
				return nil
			}
			reader.inNumber = false
		}

		switch value {
		case '"':
			reader.inString = true
			reader.escaped = false
			reader.stringSize = 0
		case '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
			reader.inNumber = true
			reader.numberSize = 1
			if reader.numberSize > reader.limits.MaxNumberBytes {
				return reader.limitError("max_number_bytes", reader.limits.MaxNumberBytes, reader.numberSize)
			}
		case '{', '[':
			depth := len(reader.containers) + 1
			if depth > reader.limits.MaxDepth {
				return reader.limitError("max_depth", int64(reader.limits.MaxDepth), int64(depth))
			}
			reader.containers = append(reader.containers, guardContainer{kind: value})
		case '}', ']':
			if len(reader.containers) > 0 && matchingClose(reader.containers[len(reader.containers)-1].kind, value) {
				reader.containers = reader.containers[:len(reader.containers)-1]
			}
		case ':':
			if len(reader.containers) > 0 {
				container := &reader.containers[len(reader.containers)-1]
				if container.kind == '{' {
					container.members++
					if container.members > int64(reader.limits.MaxObjectMembers) {
						return reader.limitError("max_object_members", int64(reader.limits.MaxObjectMembers), container.members)
					}
				}
			}
		}
		return nil
	}

	if !reader.escaped && value == '"' {
		reader.inString = false
		return nil
	}
	reader.stringSize++
	if reader.stringSize > reader.limits.MaxScalarBytes {
		return reader.limitError("max_scalar_bytes", reader.limits.MaxScalarBytes, reader.stringSize)
	}
	if reader.escaped {
		reader.escaped = false
	} else if value == '\\' {
		reader.escaped = true
	}
	return nil
}

func (reader *GuardReader) scanUTF8(value byte) error {
	if reader.utf8Expected > 0 {
		if value < reader.utf8Min || value > reader.utf8Max {
			return reader.syntaxError("invalid UTF-8 encoding")
		}
		reader.utf8Expected--
		reader.utf8Min = 0x80
		reader.utf8Max = 0xbf
		return nil
	}

	switch {
	case value <= 0x7f:
		return nil
	case value >= 0xc2 && value <= 0xdf:
		reader.setUTF8Continuation(1, 0x80, 0xbf)
	case value == 0xe0:
		reader.setUTF8Continuation(2, 0xa0, 0xbf)
	case (value >= 0xe1 && value <= 0xec) || (value >= 0xee && value <= 0xef):
		reader.setUTF8Continuation(2, 0x80, 0xbf)
	case value == 0xed:
		reader.setUTF8Continuation(2, 0x80, 0x9f)
	case value == 0xf0:
		reader.setUTF8Continuation(3, 0x90, 0xbf)
	case value >= 0xf1 && value <= 0xf3:
		reader.setUTF8Continuation(3, 0x80, 0xbf)
	case value == 0xf4:
		reader.setUTF8Continuation(3, 0x80, 0x8f)
	default:
		return reader.syntaxError("invalid UTF-8 encoding")
	}
	return nil
}

func (reader *GuardReader) setUTF8Continuation(expected int, minimum, maximum byte) {
	reader.utf8Expected = expected
	reader.utf8Min = minimum
	reader.utf8Max = maximum
}

func isNumberByte(value byte) bool {
	return (value >= '0' && value <= '9') || value == '+' || value == '-' ||
		value == '.' || value == 'e' || value == 'E'
}

func matchingClose(open, close byte) bool {
	return (open == '{' && close == '}') || (open == '[' && close == ']')
}

func (reader *GuardReader) advance(value byte) {
	reader.offset++
	if value == '\n' {
		reader.line++
		reader.column = 1
		return
	}
	reader.column++
}

func (reader *GuardReader) limitError(name string, limit, value int64) *core.AppError {
	return &core.AppError{
		Code:    core.CodeResourceLimit,
		Message: name + " exceeded",
		Location: &core.Location{
			ByteOffset: core.Int64(reader.offset),
			Line:       core.Int64(reader.line),
			Column:     core.Int64(reader.column),
		},
		Limit: &core.LimitDetail{Name: name, Limit: limit, Value: value},
	}
}

func (reader *GuardReader) syntaxError(message string) *core.AppError {
	return &core.AppError{
		Code:    core.CodeSyntax,
		Message: message,
		Location: &core.Location{
			ByteOffset: core.Int64(reader.offset),
			Line:       core.Int64(reader.line),
			Column:     core.Int64(reader.column),
		},
	}
}

func (reader *GuardReader) unsupportedCommentError() *core.AppError {
	return &core.AppError{
		Code:    core.CodeUnsupportedSyntax,
		Message: "comments are not valid in standard JSON",
		Hint:    "This file may use JSONC syntax, which is not supported by this server version.",
		Location: &core.Location{
			ByteOffset: core.Int64(reader.offset),
			Line:       core.Int64(reader.line),
			Column:     core.Int64(reader.column),
		},
	}
}
