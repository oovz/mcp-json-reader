package engine

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/oovz/mcp-json-reader/v2/internal/core"
	"github.com/oovz/mcp-json-reader/v2/internal/query"
)

func TestExecuteStreamsPointerMatchWithoutMaterializingTheDocument(t *testing.T) {
	plan, compileErr := query.Compile(core.QueryPointer, "/orders/1/id")
	if compileErr != nil {
		t.Fatalf("Compile error: %v", compileErr)
	}
	input := `{"metadata":{"count":3},"orders":[{"id":1},{"id":9007199254740993},{"id":3}]}`
	page, err := Execute(context.Background(), strings.NewReader(input), core.FormatJSON, plan, core.DefaultLimits(), PageOptions{})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if page.More || len(page.Items) != 1 {
		t.Fatalf("page = %#v, want one complete result", page)
	}
	if page.Items[0].Path != "/orders/1/id" || string(page.Items[0].Value) != "9007199254740993" {
		t.Fatalf("item = %#v, want exact order id", page.Items[0])
	}
}

func TestFilterCapturePropagatesScanDeadlineIntoQueryEvaluation(t *testing.T) {
	plan, compileErr := query.Compile(core.QueryJSONPath, "$[?@.active]")
	if compileErr != nil {
		t.Fatal(compileErr)
	}
	limits := core.DefaultLimits()
	state := &executionState{plan: plan, limits: limits, maxItems: limits.MaxItems, maxResultBytes: limits.MaxResultBytes}
	captured := &capture{filter: true, path: core.Path{core.IndexSegment(0)}}
	_, _ = captured.buffer.WriteString(`{"active":true}`)
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	err := state.finishFilterCapture(ctx, captured)
	if err == nil || err.Code != core.CodeResourceLimit || err.Limit == nil || err.Limit.Name != "max_scan_time" || err.Limit.Limit == 0 {
		t.Fatalf("finishFilterCapture error = %#v, want populated max_scan_time limit", err)
	}
}

func TestExecutePreservesJSONPathPreorderAndPaginatesByMatch(t *testing.T) {
	plan, compileErr := query.Compile(core.QueryJSONPath, "$..*")
	if compileErr != nil {
		t.Fatalf("Compile error: %v", compileErr)
	}
	input := `{"a":{"b":1},"c":2}`
	first, err := Execute(context.Background(), strings.NewReader(input), core.FormatJSON, plan, core.DefaultLimits(), PageOptions{MaxItems: 2})
	if err != nil {
		t.Fatalf("first Execute error: %v", err)
	}
	if !first.More || len(first.Items) != 2 || first.Items[0].Path != "/a" || first.Items[1].Path != "/a/b" {
		t.Fatalf("first page = %#v, want /a then /a/b with continuation", first)
	}
	if string(first.Items[0].Value) != `{"b":1}` {
		t.Fatalf("parent capture = %s, want object", first.Items[0].Value)
	}
	second, secondErr := Execute(context.Background(), strings.NewReader(input), core.FormatJSON, plan, core.DefaultLimits(), PageOptions{Skip: 2, MaxItems: 2})
	if secondErr != nil {
		t.Fatalf("second Execute error: %v", secondErr)
	}
	if second.More || len(second.Items) != 1 || second.Items[0].Path != "/c" {
		t.Fatalf("second page = %#v, want /c complete", second)
	}
}

func TestExecuteStopsAfterTheLookaheadMatchCompletesThePage(t *testing.T) {
	plan, compileErr := query.Compile(core.QueryJSONPath, "$[*]")
	if compileErr != nil {
		t.Fatal(compileErr)
	}
	input := `[1,2,` + strings.Repeat(" ", 10_000) + `?]`
	reader := &oneByteCountingReader{source: strings.NewReader(input)}
	page, err := Execute(context.Background(), reader, core.FormatJSON, plan, core.DefaultLimits(), PageOptions{MaxItems: 1})
	if err != nil {
		t.Fatalf("Execute first page error: %v", err)
	}
	if !page.More || len(page.Items) != 1 || string(page.Items[0].Value) != "1" {
		t.Fatalf("first page = %#v, want one item and a continuation", page)
	}
	if reader.bytesRead >= len(input) {
		t.Fatalf("first page read %d bytes, want it to stop before the malformed tail", reader.bytesRead)
	}
	if _, continuationErr := Execute(context.Background(), strings.NewReader(input), core.FormatJSON, plan, core.DefaultLimits(), PageOptions{Skip: 1, MaxItems: 1}); continuationErr == nil || continuationErr.Code != core.CodeSyntax {
		t.Fatalf("continuation error = %#v, want the later SYNTAX_ERROR", continuationErr)
	}
}

func TestExecuteEvaluatesBoundedJSONPathFilterCandidates(t *testing.T) {
	plan, compileErr := query.Compile(core.QueryJSONPath, `$[?@.total > 100].id`)
	if compileErr != nil {
		t.Fatalf("Compile error: %v", compileErr)
	}
	input := `[{"id":1,"total":100},{"id":2,"total":100.01},{"id":3,"total":200}]`
	page, err := Execute(context.Background(), strings.NewReader(input), core.FormatJSON, plan, core.DefaultLimits(), PageOptions{})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if len(page.Items) != 2 || page.Items[0].Path != "/1/id" || page.Items[1].Path != "/2/id" {
		t.Fatalf("page = %#v, want ids 2 and 3", page)
	}
}

func TestExecuteEnforcesCandidateLimitForMatchedComposite(t *testing.T) {
	plan, compileErr := query.Compile(core.QueryPointer, "/a")
	if compileErr != nil {
		t.Fatalf("Compile error: %v", compileErr)
	}
	limits := core.DefaultLimits()
	limits.MaxCandidateBytes = 6
	_, err := Execute(context.Background(), strings.NewReader(`{"a":{"x":1}}`), core.FormatJSON, plan, limits, PageOptions{})
	if err == nil || err.Code != core.CodeResourceLimit || err.Limit == nil || err.Limit.Name != "max_candidate_bytes" {
		t.Fatalf("error = %#v, want max_candidate_bytes", err)
	}
}

func TestExecuteBoundsDuplicateDetectionKeyStorage(t *testing.T) {
	plan, compileErr := query.Compile(core.QueryPointer, "/missing")
	if compileErr != nil {
		t.Fatalf("Compile error: %v", compileErr)
	}
	limits := core.DefaultLimits()
	limits.MaxObjectKeyBytes = 5
	_, err := Execute(context.Background(), strings.NewReader(`{"abc":1,"def":2}`), core.FormatJSON, plan, limits, PageOptions{})
	if err == nil || err.Code != core.CodeResourceLimit || err.Limit == nil || err.Limit.Name != "max_object_key_bytes" {
		t.Fatalf("error = %#v, want max_object_key_bytes", err)
	}
}

func TestExecuteBoundsKeysAcrossActiveObjectsAndReleasesSiblingBudget(t *testing.T) {
	plan, compileErr := query.Compile(core.QueryPointer, "/missing")
	if compileErr != nil {
		t.Fatal(compileErr)
	}
	limits := core.DefaultLimits()
	limits.MaxObjectKeyBytes = 8
	limits.MaxActiveKeyBytes = 3

	_, err := Execute(context.Background(), strings.NewReader(`{"aa":{"bb":1}}`), core.FormatJSON, plan, limits, PageOptions{})
	if err == nil || err.Code != core.CodeResourceLimit || err.Limit == nil || err.Limit.Name != "max_active_key_bytes" {
		t.Fatalf("nested execution error = %#v, want max_active_key_bytes", err)
	}

	limits.MaxActiveKeyBytes = 2
	if _, err := Execute(context.Background(), strings.NewReader(`[{"aa":1},{"bb":2}]`), core.FormatJSON, plan, limits, PageOptions{}); err != nil {
		t.Fatalf("sequential sibling objects retained each other's key budget: %v", err)
	}
}

func TestExecuteTreatsJSONLAndJSONSequenceAsVirtualArrays(t *testing.T) {
	tests := []struct {
		name       string
		format     core.Format
		input      string
		language   core.QueryLanguage
		expression string
	}{
		{"jsonl pointer", core.FormatJSONL, "{\"id\":1}\n{\"id\":2}\n", core.QueryPointer, "/1/id"},
		{"json sequence jsonpath", core.FormatJSONSequence, string([]byte{0x1e}) + "{\"id\":1}\n" + string([]byte{0x1e}) + "{\"id\":2}\n", core.QueryJSONPath, "$[1].id"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan, compileErr := query.Compile(test.language, test.expression)
			if compileErr != nil {
				t.Fatalf("Compile error: %v", compileErr)
			}
			page, err := Execute(context.Background(), strings.NewReader(test.input), test.format, plan, core.DefaultLimits(), PageOptions{})
			if err != nil {
				t.Fatalf("Execute error: %v", err)
			}
			if len(page.Items) != 1 || page.Items[0].Path != "/1/id" || string(page.Items[0].Value) != "2" {
				t.Fatalf("page = %#v, want /1/id = 2", page)
			}
		})
	}
}

func TestExecuteTreatsAnEmptyJSONSequenceAsAnEmptyVirtualArray(t *testing.T) {
	plan, compileErr := query.Compile(core.QueryPointer, "")
	if compileErr != nil {
		t.Fatal(compileErr)
	}
	page, err := Execute(context.Background(), strings.NewReader(""), core.FormatJSONSequence, plan, core.DefaultLimits(), PageOptions{})
	if err != nil {
		t.Fatalf("Execute empty JSON sequence error: %v", err)
	}
	if page.More || len(page.Items) != 1 || page.Items[0].Path != "" || string(page.Items[0].Value) != "[]" {
		t.Fatalf("page = %#v, want one empty virtual-array root", page)
	}
}

func TestExecuteRejectsJSONSequenceNumberAfterLongLeadingWhitespaceWithoutTerminator(t *testing.T) {
	plan, compileErr := query.Compile(core.QueryPointer, "/0")
	if compileErr != nil {
		t.Fatal(compileErr)
	}
	input := string([]byte{0x1e}) + strings.Repeat(" ", 257) + "42"
	_, err := Execute(context.Background(), strings.NewReader(input), core.FormatJSONSequence, plan, core.DefaultLimits(), PageOptions{})
	if err == nil || err.Code != core.CodeSyntax {
		t.Fatalf("Execute error = %#v, want SYNTAX_ERROR", err)
	}
}

type oneByteCountingReader struct {
	source    io.Reader
	bytesRead int
}

func (reader *oneByteCountingReader) Read(buffer []byte) (int, error) {
	if len(buffer) > 1 {
		buffer = buffer[:1]
	}
	count, err := reader.source.Read(buffer)
	reader.bytesRead += count
	return count, err
}
