package query

import (
	"testing"

	"github.com/oovz/mcp-json-reader/v3/internal/core"
)

func TestJSONPathRejectsInvalidNegation(t *testing.T) {
	for _, expression := range []string{
		"$[? !1 == 2]", "$[? !!@.id]", "$[? ! ! (@.id)]",
		"$[? !@.id == 1]", "$[? !true]", "$[? !null == null]",
		"$[? !]", "$[? != @.id]",
	} {
		t.Run(expression, func(t *testing.T) {
			if _, err := CompileJSONPath(expression); err == nil || err.Code != core.CodeQuerySyntax {
				t.Fatalf("error=%v, want QUERY_SYNTAX_ERROR", err)
			}
		})
	}
}

func TestJSONPathRejectsInvalidWhitespace(t *testing.T) {
	for _, expression := range []string{
		" $.id", "$.id ", "$ ", "$. id", "$[?@. id]",
		"$\v.id", "$\f.id", "$[\v0]", "$[0:\f1]", "$[?\v@.id]",
		"$[?@.a[\v0]]", "$[?@.a\v.id]",
	} {
		t.Run(expression, func(t *testing.T) {
			if _, err := CompileJSONPath(expression); err == nil || err.Code != core.CodeQuerySyntax {
				t.Fatalf("error=%v, want QUERY_SYNTAX_ERROR", err)
			}
		})
	}
}
