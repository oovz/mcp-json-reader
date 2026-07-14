package query

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/oovz/mcp-json-reader/v2/internal/core"
)

func TestJSONPathFilterSelectsScalarMemberComparisons(t *testing.T) {
	root := decodeCandidate(t, `{"orders":[{"id":1,"total":100,"active":true},{"id":2,"total":100.01,"active":true},{"id":3,"total":200,"active":false}]}`)
	plan, err := CompileJSONPath(`$.orders[?@.total > 100 && @.active == true].id`)
	if err != nil {
		t.Fatalf("CompileJSONPath error: %v", err)
	}
	matches, selectErr := plan.SelectDOM(root, nil)
	if selectErr != nil {
		t.Fatalf("SelectDOM error: %v", selectErr)
	}
	if len(matches) != 1 || matches[0].Path.Pointer() != "/orders/1/id" || matches[0].Value != json.Number("2") {
		t.Fatalf("matches = %#v, want order 2 id", matches)
	}
}

func TestJSONPathFilterUsesExactJSONNumberComparison(t *testing.T) {
	root := decodeCandidate(t, `[{"n":9007199254740992},{"n":9007199254740993}]`)
	plan, err := CompileJSONPath(`$[?@.n > 9007199254740992].n`)
	if err != nil {
		t.Fatalf("CompileJSONPath error: %v", err)
	}
	matches, selectErr := plan.SelectDOM(root, nil)
	if selectErr != nil {
		t.Fatalf("SelectDOM error: %v", selectErr)
	}
	if len(matches) != 1 || matches[0].Value != json.Number("9007199254740993") {
		t.Fatalf("matches = %#v, want exact larger integer", matches)
	}
}

func TestJSONPathFilterSupportsExistenceNegationAndGrouping(t *testing.T) {
	root := decodeCandidate(t, `[{"id":1,"deleted":true},{"id":2},{"id":3,"deleted":false}]`)
	plan, err := CompileJSONPath(`$[?(!@.deleted || @.deleted == false)].id`)
	if err != nil {
		t.Fatalf("CompileJSONPath error: %v", err)
	}
	matches, selectErr := plan.SelectDOM(root, nil)
	if selectErr != nil {
		t.Fatalf("SelectDOM error: %v", selectErr)
	}
	if len(matches) != 2 || matches[0].Path.Pointer() != "/1/id" || matches[1].Path.Pointer() != "/2/id" {
		t.Fatalf("matches = %#v, want ids 2 and 3", matches)
	}
}

func TestCompileJSONPathRejectsMalformedFilter(t *testing.T) {
	for _, expression := range []string{"$[?@.total >]", "$[?@.a &&]", "$[?(@.a]"} {
		_, err := CompileJSONPath(expression)
		if err == nil || err.Code != core.CodeQuerySyntax {
			t.Fatalf("CompileJSONPath(%q) error = %#v, want QUERY_SYNTAX_ERROR", expression, err)
		}
	}
}

func TestJSONPathFilterAllowsDollarSignInsideStringLiteral(t *testing.T) {
	root := decodeCandidate(t, `[{"currency":"$USD","id":1},{"currency":"EUR","id":2}]`)
	plan, err := CompileJSONPath(`$[?@.currency == "$USD"].id`)
	if err != nil {
		t.Fatalf("CompileJSONPath error: %v", err)
	}
	matches, selectErr := plan.SelectDOM(root, nil)
	if selectErr != nil || len(matches) != 1 || matches[0].Value != json.Number("1") {
		t.Fatalf("matches = %#v, error = %v, want USD id", matches, selectErr)
	}
}

func TestJSONPathFilterOrderingTypeMismatchesFollowRFC9535(t *testing.T) {
	root := decodeCandidate(t, `[{"value":false},{"value":true},{"value":null},{"value":"2"},{"value":2}]`)
	tests := []struct {
		expression string
		wantPaths  string
	}{
		{`$[?@.value < true]`, ""},
		{`$[?@.value <= true]`, "/1"},
		{`$[?@.value >= false]`, "/0"},
		{`$[?@.value <= null]`, "/2"},
		{`$[?@.value < "3"]`, "/3"},
		{`$[?@.value < 3]`, "/4"},
	}
	for _, test := range tests {
		plan, err := CompileJSONPath(test.expression)
		if err != nil {
			t.Fatalf("CompileJSONPath(%q) error: %v", test.expression, err)
		}
		matches, selectErr := plan.SelectDOM(root, nil)
		if selectErr != nil {
			t.Fatalf("SelectDOM(%q) error = %v", test.expression, selectErr)
		}
		var paths []string
		for _, match := range matches {
			paths = append(paths, match.Path.Pointer())
		}
		if got := strings.Join(paths, ","); got != test.wantPaths {
			t.Fatalf("SelectDOM(%q) paths = %q, want %q", test.expression, got, test.wantPaths)
		}
	}
}

func TestFilterCandidateVisitorCanStopWithoutBuildingRemainingMatches(t *testing.T) {
	root := decodeCandidate(t, `[{"values":[1,2,3]}]`)
	plan, err := CompileJSONPath(`$[?@.values].values[*]`)
	if err != nil {
		t.Fatal(err)
	}
	visits := 0
	visitErr := plan.VisitFilterCandidate(root.([]any)[0], core.Path{core.IndexSegment(0)}, nil, func(DOMMatch) bool {
		visits++
		return false
	})
	if visitErr != nil {
		t.Fatalf("VisitFilterCandidate error: %v", visitErr)
	}
	if visits != 1 {
		t.Fatalf("visits = %d, want exactly one", visits)
	}
}

func TestFilterCandidateVisitorHonorsScanDeadline(t *testing.T) {
	plan, compileErr := CompileJSONPath("$[?@.active]")
	if compileErr != nil {
		t.Fatal(compileErr)
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	checkpoint := func() *core.AppError {
		if err := ctx.Err(); err != nil {
			return &core.AppError{Code: core.CodeResourceLimit, Message: "max_scan_time exceeded", Limit: core.ScanTimeLimitDetail(time.Second)}
		}
		return nil
	}
	visitErr := plan.VisitFilterCandidate(map[string]any{"active": true}, core.Path{core.IndexSegment(0)}, checkpoint, func(DOMMatch) bool { return true })
	if visitErr == nil || visitErr.Code != core.CodeResourceLimit || visitErr.Limit == nil || visitErr.Limit.Limit == 0 {
		t.Fatalf("VisitFilterCandidate deadline error = %#v, want populated max_scan_time limit", visitErr)
	}
}

func TestCompileJSONPathRejectsNonCanonicalIndexes(t *testing.T) {
	for _, expression := range []string{"$[+1]", "$[01:2]", "$[?@[01] == 1]"} {
		_, err := CompileJSONPath(expression)
		if err == nil || (err.Code != core.CodeQuerySyntax && err.Code != core.CodeUnsupportedQueryFeature) {
			t.Fatalf("CompileJSONPath(%q) error = %#v, want query rejection", expression, err)
		}
	}
}

func decodeCandidate(t *testing.T, input string) any {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(input))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		t.Fatalf("Decode candidate error: %v", err)
	}
	return value
}
