package service

import (
	"github.com/oovz/mcp-json-reader/v3/internal/core"
	"github.com/oovz/mcp-json-reader/v3/internal/query"
)

// readResultSize measures the exact default encoding/json representation of the
// closed ReadOutput contract. Contract tests compare it with actual encoders.
func readResultSize(output ReadOutput) int64 {
	size := int64(len(`{"language":,"query":,"items":[],"complete":,"stats":{"returned":,"skipped":}}`))
	size += core.JSONStringSize(string(output.Language)) + core.JSONStringSize(output.Query)
	if output.FileID != "" {
		size += int64(len(`,"file_id":`)) + core.JSONStringSize(output.FileID)
	}
	if output.NextCursor != "" {
		size += int64(len(`,"next_cursor":`)) + core.JSONStringSize(output.NextCursor)
	}
	if output.Complete {
		size += 4
	} else {
		size += 5
	}
	size += core.JSONIntegerSize(int64(output.Stats.Returned)) + core.JSONIntegerSize(output.Stats.Skipped)
	for index, item := range output.Items {
		if index > 0 {
			size++
		}
		size += int64(len(`{"path":,"value":}`)) + core.JSONStringSize(item.Path) + int64(len(item.Value))
	}
	return size
}

func (service *Service) resultReserve(fileID string, plan *query.Plan, skip int64, requestedItems int) int64 {
	maxItems := requestedItems
	if maxItems == 0 || maxItems > service.limits.MaxItems {
		maxItems = service.limits.MaxItems
	}
	// Cursor IDs are a fixed prefix followed by 12 random bytes in hex.
	output := ReadOutput{
		FileID: fileID, Language: plan.Language(), Query: plan.Expression(),
		NextCursor: "jc_000000000000000000000000", Complete: false,
		Stats: ReadStats{Returned: maxItems, Skipped: skip},
	}
	// The engine owns the two items-array brackets and all item bytes.
	return readResultSize(output) - 2
}
