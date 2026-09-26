package mcpserver

import (
	"encoding/json"
	"testing"
)

func TestReviewRegressionRejectUndeclaredCaseVariant(t *testing.T) {
	_, err := decodeOpenArguments(json.RawMessage(`{"path":"safe.json","PATH":"other.json"}`))
	if err == nil {
		t.Fatal("accepted undeclared PATH property despite additionalProperties:false")
	}
}

func TestReviewPolicyRejectDuplicateArguments(t *testing.T) {
	_, err := decodeOpenArguments(json.RawMessage(`{"path":"safe.json","path":"other.json"}`))
	if err == nil {
		t.Fatal("accepted duplicate tool argument names")
	}
}
