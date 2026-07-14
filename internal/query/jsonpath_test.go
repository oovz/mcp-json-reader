package query

import (
	"strings"
	"testing"

	"github.com/oovz/mcp-json-reader/v2/internal/core"
)

func TestCompileJSONPathMatchesForwardStreamingSelectors(t *testing.T) {
	tests := []struct {
		expression string
		path       core.Path
		want       bool
	}{
		{"$", core.Path{}, true},
		{"$.orders[0].id", core.Path{core.PropertySegment("orders"), core.IndexSegment(0), core.PropertySegment("id")}, true},
		{"$['a b'][*]", core.Path{core.PropertySegment("a b"), core.IndexSegment(3)}, true},
		{"$['a,b']", core.Path{core.PropertySegment("a,b")}, true},
		{"$.orders[1:6:2]", core.Path{core.PropertySegment("orders"), core.IndexSegment(5)}, true},
		{"$.orders[1:6:2]", core.Path{core.PropertySegment("orders"), core.IndexSegment(4)}, false},
		{"$..id", core.Path{core.PropertySegment("orders"), core.IndexSegment(2), core.PropertySegment("id")}, true},
		{"$..*", core.Path{core.PropertySegment("orders"), core.IndexSegment(2)}, true},
	}
	for _, test := range tests {
		plan, err := CompileJSONPath(test.expression)
		if err != nil {
			t.Fatalf("CompileJSONPath(%q) error: %v", test.expression, err)
		}
		if got := plan.Matches(test.path); got != test.want {
			t.Fatalf("CompileJSONPath(%q).Matches(%q) = %v, want %v", test.expression, test.path.Pointer(), got, test.want)
		}
	}
}

func TestCompileJSONPathRejectsNonStreamingProfileFeatures(t *testing.T) {
	for _, expression := range []string{
		"$[-1]",
		"$[::-1]",
		"$[2,0]",
		"$['a','b']",
		`$["a","b"]`,
		"$.items[?length(@) > 0]",
		"$.items[?$.limit < @.value]",
		"$..*..id",
		"$.items[?@.active]..id",
		"$..item.id",
	} {
		_, err := CompileJSONPath(expression)
		if err == nil || err.Code != core.CodeUnsupportedQueryFeature {
			t.Fatalf("CompileJSONPath(%q) error = %#v, want UNSUPPORTED_QUERY_FEATURE", expression, err)
		}
	}
}

func TestCompileJSONPathCapsFilterComplexity(t *testing.T) {
	expressions := []string{
		"$[?" + strings.Repeat("@.active == true || ", 65) + "@.active == true]",
		"$[?" + strings.Repeat("!", 129) + "@.active]",
		"$[?" + strings.Repeat("(", 65) + "@.active" + strings.Repeat(")", 65) + "]",
		"$[?@" + strings.Repeat(".a", 65) + "]",
	}
	for _, expression := range expressions {
		if _, err := CompileJSONPath(expression); err == nil || err.Code != core.CodeUnsupportedQueryFeature {
			t.Fatalf("CompileJSONPath oversized filter error = %#v, want UNSUPPORTED_QUERY_FEATURE", err)
		}
	}
}

func TestJSONPathMatchesDoesNotAllocatePerStreamedNode(t *testing.T) {
	plan, err := CompileJSONPath("$.a.b.c")
	if err != nil {
		t.Fatal(err)
	}
	path := core.Path{core.PropertySegment("a"), core.PropertySegment("b"), core.PropertySegment("c")}
	if allocations := testing.AllocsPerRun(100, func() { _ = plan.Matches(path) }); allocations != 0 {
		t.Fatalf("Matches allocations = %v, want zero per streamed node", allocations)
	}
}

func TestCompileJSONPathCapsSelectorComplexity(t *testing.T) {
	expression := "$" + strings.Repeat(".*", 65)
	_, err := CompileJSONPath(expression)
	if err == nil || err.Code != core.CodeUnsupportedQueryFeature {
		t.Fatalf("CompileJSONPath selector cap error = %#v, want UNSUPPORTED_QUERY_FEATURE", err)
	}
}

func TestCompileJSONPathRejectsMalformedExpressions(t *testing.T) {
	for _, expression := range []string{"", "orders[*]", "$.orders[", "$...id", "$['unterminated]", "$['a''b']", `$["a""b"]`} {
		_, err := CompileJSONPath(expression)
		if err == nil || err.Code != core.CodeQuerySyntax {
			t.Fatalf("CompileJSONPath(%q) error = %#v, want QUERY_SYNTAX_ERROR", expression, err)
		}
	}
}
