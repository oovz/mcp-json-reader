package engine

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/oovz/mcp-json-reader/v3/internal/core"
	"github.com/oovz/mcp-json-reader/v3/internal/query"
)

func TestMaximumInteroperableSliceStepExecutes(t *testing.T) {
	page, err := reviewExecute(t, `[[0,1,2]]`, `$[?@][1::9007199254740991]`)
	if err != nil || len(page.Items) != 1 || string(page.Items[0].Value) != "1" {
		t.Fatalf("page=%#v error=%v", page, err)
	}
}

func FuzzExecuteAcceptedPlans(f *testing.F) {
	for _, seed := range [][2]string{
		{`[[0,1]]`, `$[?@][1::9223372036854775807]`},
		{`[[0,1]]`, `$[?@][1::9007199254740991]`},
		{`[{}]`, `$[?@.missing == @.other]`},
		{`[{"s":"\uD800"}]`, `$[*]`},
		{`{"a":[1,2]}`, `$.*[*]`},
	} {
		f.Add(seed[0], seed[1])
	}
	f.Fuzz(func(t *testing.T, data, expression string) {
		if len(data) > 4096 || len(expression) > 256 {
			t.Skip()
		}
		plan, err := query.Compile(core.QueryJSONPath, expression)
		if err != nil {
			return
		}
		limits := core.DefaultLimits()
		limits.MaxScalarBytes = 4096
		limits.MaxDepth = 32
		limits.MaxObjectMembers = 128
		limits.MaxObjectKeyBytes = 4096
		limits.MaxActiveKeyBytes = 8192
		limits.MaxCandidateBytes = 8192
		limits.MaxResultBytes = 8192
		limits.MaxItems = 16
		limits.MaxScanTime = 50 * time.Millisecond
		page, executeErr := Execute(context.Background(), strings.NewReader(data), core.FormatJSON, plan, limits, PageOptions{})
		if executeErr != nil {
			return
		}
		encoded, marshalErr := json.Marshal(page.Items)
		if marshalErr != nil || len(encoded) > 8192 {
			t.Fatalf("invalid bounded result: len=%d error=%v", len(encoded), marshalErr)
		}
		for _, item := range page.Items {
			if !json.Valid(item.Value) {
				t.Fatalf("invalid JSON result %q", item.Value)
			}
		}
	})
}

func BenchmarkRejectedLongPath(b *testing.B) {
	data := `{"` + strings.Repeat("k", 10<<10) + `":[` + strings.Repeat("0,", 999) + `0]}`
	limits := core.DefaultLimits()
	limits.MaxResultBytes = 1024
	plan, err := query.Compile(core.QueryJSONPath, `$.*[*]`)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := Execute(context.Background(), strings.NewReader(data), core.FormatJSON, plan, limits, PageOptions{})
		if err == nil || err.Code != core.CodeResourceLimit {
			b.Fatal(err)
		}
	}
}

func BenchmarkPaginationPositions(b *testing.B) {
	data := `[` + strings.Repeat(`{"id":1},`, 9999) + `{"id":1}]`
	plan, err := query.Compile(core.QueryJSONPath, `$[*].id`)
	if err != nil {
		b.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		skip int64
	}{{"first", 0}, {"middle", 5000}, {"last", 9900}} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_, err := Execute(context.Background(), strings.NewReader(data), core.FormatJSON, plan, core.DefaultLimits(), PageOptions{Skip: tc.skip, MaxItems: 100})
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// Total allocation traffic bounds the live allocations caused by this execution.
// A wide ceiling avoids coupling the regression to a particular allocator while
// catching the old roughly 10-MiB path amplification for a 1-KiB result budget.
func TestRejectedLongPathAllocationCeiling(t *testing.T) {
	data := `{"` + strings.Repeat("k", 10<<10) + `":[` + strings.Repeat("0,", 999) + `0]}`
	limits := core.DefaultLimits()
	limits.MaxResultBytes = 1024
	plan, err := query.Compile(core.QueryJSONPath, `$.*[*]`)
	if err != nil {
		t.Fatal(err)
	}
	execute := func() {
		page, appErr := Execute(context.Background(), strings.NewReader(data), core.FormatJSON, plan, limits, PageOptions{})
		if appErr == nil || appErr.Code != core.CodeResourceLimit || len(page.Items) != 0 {
			t.Fatalf("page=%#v error=%v", page, appErr)
		}
	}
	execute() // Warm runtime and parser initialization before measuring.
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for range 10 {
		execute()
	}
	runtime.ReadMemStats(&after)
	average := (after.TotalAlloc - before.TotalAlloc) / 10
	t.Logf("long-path rejection allocated %d bytes/op", average)
	if average > 1<<20 {
		t.Fatalf("early rejection allocated %d bytes/op; ceiling is 1 MiB", average)
	}
}
