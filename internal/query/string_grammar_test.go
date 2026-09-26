package query

import (
	"testing"

	"github.com/oovz/mcp-json-reader/v3/internal/core"
)

func TestParseJSONPathRejectsCrossQuoteEscape(t *testing.T) {
	for _, expression := range []string{
		`$['\"']`,
		`$[?@['\"'] == 1]`,
		`$[?@.value == '\"']`,
	} {
		t.Run(expression, func(t *testing.T) {
			if _, err := Compile(core.QueryJSONPath, expression); err == nil || err.Code != core.CodeQuerySyntax {
				t.Fatalf("Compile(%q) error = %v, want QUERY_SYNTAX_ERROR", expression, err)
			}
		})
	}
}

func TestParseJSONPathKeepsValidSingleQuoteEscapes(t *testing.T) {
	for _, expression := range []string{
		`$['\'']`,
		`$['"']`,
		`$['\u0022']`,
		`$[?@.value == '\'']`,
		`$[?@.value == '"']`,
		`$[?@.value == '\u0022']`,
	} {
		t.Run(expression, func(t *testing.T) {
			if _, err := Compile(core.QueryJSONPath, expression); err != nil {
				t.Fatalf("Compile(%q) error = %v", expression, err)
			}
		})
	}
}
