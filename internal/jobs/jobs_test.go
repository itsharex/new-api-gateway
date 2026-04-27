package jobs

import (
	"encoding/json"
	"testing"
)

func TestTraceCapturedJobJSON(t *testing.T) {
	job := TraceCapturedJob{TraceID: "trace_1", RoutePattern: "/v1/chat/completions", CaptureMode: "raw_and_normalized"}
	data, err := json.Marshal(job)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	if string(data) == "{}" {
		t.Fatal("unexpected empty JSON")
	}
}
