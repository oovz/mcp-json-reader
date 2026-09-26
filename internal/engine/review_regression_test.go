package engine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/oovz/mcp-json-reader/v3/internal/core"
	"github.com/oovz/mcp-json-reader/v3/internal/query"
)

// Regression coverage for the reviewed implementation defects.
func reviewExecute(t *testing.T, data, expression string) (Page, *core.AppError) {
	t.Helper()
	plan, err := query.Compile(core.QueryJSONPath, expression)
	if err != nil {
		t.Fatalf("compile %q: %v", expression, err)
	}
	return Execute(context.Background(), strings.NewReader(data), core.FormatJSON,
		plan, core.DefaultLimits(), PageOptions{})
}

func TestReviewRegressionMissingComparisons(t *testing.T) {
	for _, expression := range []string{
		`$[?@.missing == @.other]`,
		`$[?@.missing != 1]`,
		`$[?@.missing <= @.other]`,
	} {
		t.Run(expression, func(t *testing.T) {
			page, err := reviewExecute(t, `[{}]`, expression)
			if err != nil {
				t.Fatal(err)
			}
			if len(page.Items) != 1 {
				t.Errorf("got %d matches, want 1", len(page.Items))
			}
		})
	}
}

func TestReviewRegressionContainerScalarSymmetry(t *testing.T) {
	for _, expression := range []string{`$[?@.v == null]`, `$[?null == @.v]`} {
		t.Run(expression, func(t *testing.T) {
			page, err := reviewExecute(t, `[{"v":{}}]`, expression)
			if err != nil {
				t.Fatalf("different JSON types should compare unequal: %v", err)
			}
			if len(page.Items) != 0 {
				t.Errorf("got %d matches, want 0", len(page.Items))
			}
		})
	}
}

func TestReviewRegressionSliceMustNotPanic(t *testing.T) {
	defer func() {
		if value := recover(); value != nil {
			t.Errorf("accepted query panicked: %v", value)
		}
	}()
	plan, err := query.Compile(core.QueryJSONPath, `$[?@][1::9223372036854775807]`)
	if err != nil {
		// Rejection is correct: this integer exceeds the RFC 9535 domain.
		if err.Code != core.CodeQuerySyntax {
			t.Errorf("out-of-domain integer: got %s, want QUERY_SYNTAX_ERROR", err.Code)
		}
		return
	}
	_, _ = Execute(context.Background(), strings.NewReader(`[[0,1]]`), core.FormatJSON,
		plan, core.DefaultLimits(), PageOptions{})
	t.Error("compiler accepted an out-of-domain JSONPath integer")
}

func TestReviewRegressionDescendantRejected(t *testing.T) {
	for _, expression := range []string{"$..*", "$..id"} {
		if _, err := query.Compile(core.QueryJSONPath, expression); err == nil || err.Code != core.CodeUnsupportedQueryFeature {
			t.Fatalf("%s: expected unsupported descendant selector, got %v", expression, err)
		}
	}
}

func TestReviewRegressionResultPathsAreBudgeted(t *testing.T) {
	data := `{"` + strings.Repeat("k", 10<<10) + `":[` + strings.Repeat("0,", 999) + `0]}`
	limits := core.DefaultLimits()
	limits.MaxResultBytes = 1024
	plan, err := query.Compile(core.QueryJSONPath, `$.*[*]`)
	if err != nil {
		t.Fatal(err)
	}
	page, executeErr := Execute(context.Background(), strings.NewReader(data),
		core.FormatJSON, plan, limits, PageOptions{})
	if executeErr == nil {
		encoded, marshalErr := json.Marshal(page)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		t.Fatalf("accepted %d items (%d encoded bytes) under a %d-byte cap",
			len(page.Items), len(encoded), limits.MaxResultBytes)
	}
	if executeErr.Code != core.CodeResourceLimit {
		t.Errorf("got %s, want RESOURCE_LIMIT_EXCEEDED", executeErr.Code)
	}
}

// Product-policy test: reject ill-formed Unicode rather than replace its content.
// This is the recommended strict-input contract, not an RFC 8259 grammar claim.
func TestReviewPolicyRejectUnpairedSurrogate(t *testing.T) {
	plan, err := query.Compile(core.QueryPointer, `/s`)
	if err != nil {
		t.Fatal(err)
	}
	page, executeErr := Execute(context.Background(), strings.NewReader(`{"s":"\uD800"}`),
		core.FormatJSON, plan, core.DefaultLimits(), PageOptions{})
	if executeErr == nil {
		t.Fatalf("ill-formed Unicode accepted and transformed: %#v", page.Items)
	}
	if executeErr.Code != core.CodeSyntax {
		t.Errorf("got %s, want SYNTAX_ERROR", executeErr.Code)
	}
}
