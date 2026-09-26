package stream

import (
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"io"

	"github.com/oovz/mcp-json-reader/v3/internal/core"
)

// Visitor consumes validated structural events. Token includes delimiter
// punctuation (colon and comma) so captures can be encoded incrementally.
// A visitor may stop after a lookahead match by returning true from Done.
type Visitor interface {
	Begin(core.Path) *core.AppError
	Token(json.Token) *core.AppError
	End(context.Context, core.Path) *core.AppError
	Done() bool
}

// Walker is the single structural parser used for validation and execution.
// A nil visitor performs validation with the same lexical and object budgets.
type Walker struct {
	ctx            context.Context
	decoder        *jsontext.Decoder
	limits         core.Limits
	visitor        Visitor
	activeKeyBytes int64
}

func NewWalker(ctx context.Context, source io.Reader, limits core.Limits, visitor Visitor) *Walker {
	decoder := jsontext.NewDecoder(NewGuardReader(CheckedReader(ctx, source), limits))
	return &Walker{ctx: ctx, decoder: decoder, limits: limits, visitor: visitor}
}

func (walker *Walker) done() bool { return walker.visitor != nil && walker.visitor.Done() }

func (walker *Walker) emit(token json.Token) *core.AppError {
	if walker.visitor == nil {
		return nil
	}
	return walker.visitor.Token(token)
}

func (walker *Walker) Walk(path core.Path) (RootKind, *core.AppError) {
	if err := walker.ctx.Err(); err != nil {
		return "", core.ContextError(err, walker.limits.MaxScanTime)
	}
	token, err := walker.readToken()
	if err != nil {
		return "", walker.decodeError(err, path)
	}
	if walker.visitor != nil {
		if beginErr := walker.visitor.Begin(path); beginErr != nil {
			return "", beginErr
		}
		if walker.done() {
			return scalarRootKind(token), nil
		}
	}
	if emitErr := walker.emit(token); emitErr != nil {
		return "", emitErr
	}
	kind := scalarRootKind(token)
	if delimiter, composite := token.(json.Delim); composite {
		switch delimiter {
		case '{':
			kind = RootObject
			var keyBytes int64
			defer func() { walker.activeKeyBytes -= keyBytes }()
			first := true
			for walker.decoder.PeekKind() != '}' {
				keyToken, keyErr := walker.readToken()
				if keyErr != nil {
					return "", walker.decodeError(keyErr, path)
				}
				key, ok := keyToken.(string)
				if !ok {
					return "", walker.syntax("object member name must be a string", path)
				}
				memberPath := path.Append(core.PropertySegment(key))
				if next := keyBytes + int64(len(key)); next > walker.limits.MaxObjectKeyBytes {
					return "", walker.keyLimit("max_object_key_bytes", walker.limits.MaxObjectKeyBytes, next, memberPath)
				}
				if next := walker.activeKeyBytes + int64(len(key)); next > walker.limits.MaxActiveKeyBytes {
					return "", walker.keyLimit("max_active_key_bytes", walker.limits.MaxActiveKeyBytes, next, memberPath)
				}
				keyBytes += int64(len(key))
				walker.activeKeyBytes += int64(len(key))
				if !first {
					if emitErr := walker.emit(json.Delim(',')); emitErr != nil {
						return "", emitErr
					}
				}
				first = false
				if emitErr := walker.emit(key); emitErr != nil {
					return "", emitErr
				}
				if emitErr := walker.emit(json.Delim(':')); emitErr != nil {
					return "", emitErr
				}
				if _, childErr := walker.Walk(memberPath); childErr != nil {
					return "", childErr
				}
				if walker.done() {
					return kind, nil
				}
			}
		case '[':
			kind = RootArray
			var index int64
			for walker.decoder.PeekKind() != ']' {
				if index > 0 {
					if emitErr := walker.emit(json.Delim(',')); emitErr != nil {
						return "", emitErr
					}
				}
				if _, childErr := walker.Walk(path.Append(core.IndexSegment(index))); childErr != nil {
					return "", childErr
				}
				if walker.done() {
					return kind, nil
				}
				index++
			}
		default:
			return "", walker.syntax("unexpected closing delimiter", path)
		}
		closeToken, closeErr := walker.readToken()
		if closeErr != nil {
			return "", walker.decodeError(closeErr, path)
		}
		if emitErr := walker.emit(closeToken); emitErr != nil {
			return "", emitErr
		}
	}
	if walker.visitor != nil {
		if endErr := walker.visitor.End(walker.ctx, path); endErr != nil {
			return "", endErr
		}
	}
	return kind, nil
}

// Finish verifies that one complete document consumed the entire input.
func (walker *Walker) Finish() *core.AppError {
	if err := walker.ctx.Err(); err != nil {
		return core.ContextError(err, walker.limits.MaxScanTime)
	}
	if _, err := walker.readToken(); err == nil {
		return &core.AppError{Code: core.CodeFormatMismatch, Message: "expected one JSON document, but found another top-level value", ExpectedFormat: core.FormatJSON, LikelyFormats: []core.Format{core.FormatJSONL}, Retry: &core.Retry{Format: core.FormatJSONL}, Location: walker.location(nil)}
	} else if !errors.Is(err, io.EOF) {
		return walker.decodeError(err, nil)
	}
	return nil
}

func (walker *Walker) location(path core.Path) *core.Location {
	return &core.Location{ByteOffset: core.Int64(walker.decoder.InputOffset()), Path: path.DiagnosticPointer()}
}

func (walker *Walker) syntax(message string, path core.Path) *core.AppError {
	return &core.AppError{Code: core.CodeSyntax, Message: message, Location: walker.location(path)}
}

func (walker *Walker) keyLimit(name string, limit, value int64, path core.Path) *core.AppError {
	return &core.AppError{Code: core.CodeResourceLimit, Message: name + " exceeded", Location: walker.location(path), Limit: &core.LimitDetail{Name: name, Limit: limit, Value: value}}
}

func (walker *Walker) decodeError(err error, path core.Path) *core.AppError {
	var appErr *core.AppError
	if errors.As(err, &appErr) {
		if appErr.Location != nil && appErr.Location.Path == "" {
			appErr.Location.Path = path.DiagnosticPointer()
		}
		return appErr
	}
	if contextErr := walker.ctx.Err(); contextErr != nil {
		return core.ContextError(contextErr, walker.limits.MaxScanTime)
	}
	location := walker.location(path)
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return &core.AppError{Code: core.CodeSyntax, Message: "unexpected end of JSON input", Location: location, Cause: io.ErrUnexpectedEOF}
	}
	var syntaxErr *jsontext.SyntacticError
	if errors.As(err, &syntaxErr) {
		location.ByteOffset = core.Int64(syntaxErr.ByteOffset)
		if len(syntaxErr.JSONPointer) <= 1024 {
			location.Path = string(syntaxErr.JSONPointer)
		}
		message := "invalid JSON syntax or Unicode encoding"
		if errors.Is(err, jsontext.ErrDuplicateName) {
			message = "duplicate object member name"
		}
		return &core.AppError{Code: core.CodeSyntax, Message: message, Location: location, Cause: err}
	}
	return &core.AppError{Code: core.CodeIO, Message: "cannot read JSON source", Location: location, Cause: err}
}

// Convert immediately: jsontext tokens borrow decoder storage until its next read.
// String and number conversions produce owned text, preserving numeric spelling.
func (walker *Walker) readToken() (json.Token, error) {
	token, err := walker.decoder.ReadToken()
	if err != nil {
		return nil, err
	}
	switch token.Kind() {
	case '{', '}', '[', ']':
		return json.Delim(token.Kind()), nil
	case 'n':
		return nil, nil
	case 't':
		return true, nil
	case 'f':
		return false, nil
	case '"':
		return token.String(), nil
	case '0':
		return json.Number(token.String()), nil
	default:
		return nil, errors.New("unexpected JSON token kind")
	}
}

func CheckedReader(ctx context.Context, source io.Reader) io.Reader {
	return &contextReader{ctx: ctx, source: source}
}

type contextReader struct {
	ctx    context.Context
	source io.Reader
}

func (reader *contextReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	count, err := reader.source.Read(buffer)
	if err == nil {
		err = reader.ctx.Err()
	}
	return count, err
}
