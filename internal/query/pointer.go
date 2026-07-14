package query

import (
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/oovz/mcp-json-reader/v2/internal/core"
)

type PointerPlan struct {
	expression string
	tokens     []string
}

func CompilePointer(expression string) (*PointerPlan, *core.AppError) {
	original := expression
	if strings.HasPrefix(expression, "#") {
		decoded, err := url.PathUnescape(expression[1:])
		if err != nil || !utf8.ValidString(decoded) {
			return nil, pointerSyntaxError("invalid URI fragment encoding")
		}
		expression = decoded
	}
	if expression == "" {
		return &PointerPlan{expression: original}, nil
	}
	if expression[0] != '/' {
		return nil, pointerSyntaxError("a JSON Pointer must be empty or begin with '/'")
	}

	rawTokens := strings.Split(expression[1:], "/")
	tokens := make([]string, len(rawTokens))
	for index, raw := range rawTokens {
		decoded, err := decodePointerToken(raw)
		if err != nil {
			return nil, err
		}
		tokens[index] = decoded
	}
	return &PointerPlan{expression: original, tokens: tokens}, nil
}

func (plan *PointerPlan) Matches(path core.Path) bool {
	if len(path) != len(plan.tokens) {
		return false
	}
	for index, segment := range path {
		token := plan.tokens[index]
		if segment.Kind == core.SegmentIndex {
			if token != strconv.FormatInt(segment.Index, 10) {
				return false
			}
			continue
		}
		if token != segment.Name {
			return false
		}
	}
	return true
}

func (plan *PointerPlan) Expression() string { return plan.expression }

func decodePointerToken(raw string) (string, *core.AppError) {
	var decoded strings.Builder
	decoded.Grow(len(raw))
	for index := 0; index < len(raw); index++ {
		if raw[index] != '~' {
			decoded.WriteByte(raw[index])
			continue
		}
		if index+1 >= len(raw) {
			return "", pointerSyntaxError("a '~' escape must be followed by '0' or '1'")
		}
		index++
		switch raw[index] {
		case '0':
			decoded.WriteByte('~')
		case '1':
			decoded.WriteByte('/')
		default:
			return "", pointerSyntaxError("a '~' escape must be followed by '0' or '1'")
		}
	}
	return decoded.String(), nil
}

func pointerSyntaxError(message string) *core.AppError {
	return &core.AppError{Code: core.CodeQuerySyntax, Message: message}
}
