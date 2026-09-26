package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/oovz/mcp-json-reader/v3/internal/core"
	"github.com/oovz/mcp-json-reader/v3/internal/query"
	"github.com/oovz/mcp-json-reader/v3/internal/stream"
)

type interruptedReader struct {
	cancel context.CancelFunc
	data   string
	err    error
}

func (reader interruptedReader) Read(buffer []byte) (int, error) {
	if reader.cancel != nil {
		reader.cancel()
	}
	return copy(buffer, reader.data), reader.err
}

func TestFramedContextErrors(t *testing.T) {
	plan, err := query.Compile(core.QueryJSONPath, "$[*]")
	if err != nil {
		t.Fatal(err)
	}
	limits := core.DefaultLimits()
	operations := []struct {
		name string
		run  func(context.Context, io.Reader, core.Format) *core.AppError
	}{
		{"execute", func(ctx context.Context, reader io.Reader, format core.Format) *core.AppError {
			page, err := Execute(ctx, reader, format, plan, limits, PageOptions{})
			if err != nil && len(page.Items) != 0 {
				t.Error("failed execution returned a partial page")
			}
			return err
		}},
		{"validate", func(ctx context.Context, reader io.Reader, format core.Format) *core.AppError {
			_, err := stream.ValidateSource(ctx, reader, format, limits)
			return err
		}},
	}
	for _, format := range []core.Format{core.FormatJSONL, core.FormatJSONSequence} {
		for _, operation := range operations {
			for _, afterRecord := range []bool{false, true} {
				for _, scenario := range []struct {
					name   string
					cancel bool
					data   bool
					err    error
					want   core.ErrorCode
				}{
					{"cancel-before-data", true, false, nil, core.CodeCancelled},
					{"cancel-with-data", true, true, nil, core.CodeCancelled},
					{"cancel-with-eof", true, false, io.EOF, core.CodeCancelled},
					{"wrapped-cancellation", false, false, fmt.Errorf("read: %w", context.Canceled), core.CodeCancelled},
					{"wrapped-deadline", false, false, fmt.Errorf("read: %w", context.DeadlineExceeded), core.CodeResourceLimit},
				} {
					t.Run(fmt.Sprintf("%s/%s/after-record=%t/%s", format, operation.name, afterRecord, scenario.name), func(t *testing.T) {
						ctx, cancel := context.WithCancel(t.Context())
						defer cancel()
						reader := interruptedReader{err: scenario.err}
						if scenario.cancel {
							reader.cancel = cancel
						}
						record := "0\n"
						if format == core.FormatJSONSequence {
							record = "\x1e0\n"
						}
						if scenario.data {
							reader.data = record
						}
						var input io.Reader = reader
						if afterRecord {
							prefix := record
							if format == core.FormatJSONSequence {
								prefix += "\x1e"
							}
							input = io.MultiReader(strings.NewReader(prefix), reader)
						}
						appErr := operation.run(ctx, input, format)
						if appErr == nil || appErr.Code != scenario.want {
							t.Fatalf("error=%#v, want %s", appErr, scenario.want)
						}
						cause := context.Canceled
						if scenario.want == core.CodeResourceLimit {
							cause = context.DeadlineExceeded
							if appErr.Limit == nil || appErr.Limit.Name != "max_scan_time" || appErr.Limit.Limit != limits.MaxScanTime.Milliseconds() {
								t.Fatalf("deadline limit=%#v", appErr.Limit)
							}
						}
						if !errors.Is(appErr, cause) {
							t.Fatalf("error lost cause %v: %#v", cause, appErr)
						}
					})
				}
			}
		}
	}
}
