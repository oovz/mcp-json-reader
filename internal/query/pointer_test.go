package query

import (
	"testing"

	"github.com/oovz/mcp-json-reader/v2/internal/core"
)

func TestCompilePointerMatchesRootPropertiesAndCanonicalArrayIndexes(t *testing.T) {
	tests := []struct {
		expression string
		path       core.Path
		want       bool
	}{
		{"", core.Path{}, true},
		{"/orders/0/a~1b~0c", core.Path{core.PropertySegment("orders"), core.IndexSegment(0), core.PropertySegment("a/b~c")}, true},
		{"/01", core.Path{core.IndexSegment(1)}, false},
		{"/01", core.Path{core.PropertySegment("01")}, true},
		{"/-", core.Path{core.IndexSegment(0)}, false},
		{"/-", core.Path{core.PropertySegment("-")}, true},
	}
	for _, test := range tests {
		plan, err := CompilePointer(test.expression)
		if err != nil {
			t.Fatalf("CompilePointer(%q) error: %v", test.expression, err)
		}
		if got := plan.Matches(test.path); got != test.want {
			t.Fatalf("CompilePointer(%q).Matches(%q) = %v, want %v", test.expression, test.path.Pointer(), got, test.want)
		}
	}
}

func TestCompilePointerAcceptsURIFragmentRepresentation(t *testing.T) {
	plan, err := CompilePointer("#/a%20b/c~1d")
	if err != nil {
		t.Fatalf("CompilePointer error: %v", err)
	}
	path := core.Path{core.PropertySegment("a b"), core.PropertySegment("c/d")}
	if !plan.Matches(path) {
		t.Fatalf("fragment pointer did not match %q", path.Pointer())
	}
}

func TestCompilePointerRejectsInvalidEscapes(t *testing.T) {
	for _, expression := range []string{"orders/0", "/a~2b", "/trailing~", "#/%zz"} {
		_, err := CompilePointer(expression)
		if err == nil || err.Code != core.CodeQuerySyntax {
			t.Fatalf("CompilePointer(%q) error = %#v, want QUERY_SYNTAX_ERROR", expression, err)
		}
	}
}
