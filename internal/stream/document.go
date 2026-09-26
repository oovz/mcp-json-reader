package stream

import (
	"context"
	"encoding/json"
	"io"

	"github.com/oovz/mcp-json-reader/v3/internal/core"
)

type RootKind string

const (
	RootObject  RootKind = "object"
	RootArray   RootKind = "array"
	RootString  RootKind = "string"
	RootNumber  RootKind = "number"
	RootBoolean RootKind = "boolean"
	RootNull    RootKind = "null"
)

type DocumentInfo struct {
	RootKind RootKind
}

// ValidateDocument validates one value through the shared bounded walker.
func ValidateDocument(ctx context.Context, source io.Reader, limits core.Limits) (DocumentInfo, *core.AppError) {
	if err := limits.Validate(); err != nil {
		return DocumentInfo{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, limits.MaxScanTime)
	defer cancel()
	walker := NewWalker(ctx, source, limits, nil)
	kind, err := walker.Walk(nil)
	if err != nil {
		return DocumentInfo{}, err
	}
	if err := walker.Finish(); err != nil {
		return DocumentInfo{}, err
	}
	return DocumentInfo{RootKind: kind}, nil
}

func scalarRootKind(token json.Token) RootKind {
	switch token.(type) {
	case nil:
		return RootNull
	case string:
		return RootString
	case json.Number:
		return RootNumber
	case bool:
		return RootBoolean
	default:
		return RootNumber
	}
}
