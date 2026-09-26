package query

import (
	"testing"

	"github.com/oovz/mcp-json-reader/v3/internal/core"
)

func TestReviewRegressionIntegerDomain(t *testing.T) {
	for _, expression := range []string{
		`$[9007199254740992]`,
		`$[9007199254740992:]`,
		`$[:9007199254740992]`,
		`$[::9007199254740992]`,
		`$[?@[9007199254740992]]`,
	} {
		t.Run(expression, func(t *testing.T) {
			_, err := Compile(core.QueryJSONPath, expression)
			if err == nil || err.Code != core.CodeQuerySyntax {
				t.Errorf("got %v, want QUERY_SYNTAX_ERROR for integer outside RFC domain", err)
			}
		})
	}
}
