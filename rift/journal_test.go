package rift_test

import (
	"encoding/json"
	"testing"

	"github.com/achird-labs/rift-go/rift"
)

func TestRecordedRequestStatusAndLatency(t *testing.T) {
	t.Run("recorded by engine 0.18.0", func(t *testing.T) {
		var r rift.RecordedRequest
		doc := `{"method":"GET","path":"/x","timestamp":"2026-09-23T17:08:00Z","status":201,"latencyMs":0}`
		if err := json.Unmarshal([]byte(doc), &r); err != nil {
			t.Fatal(err)
		}
		if r.Status != 201 {
			t.Errorf("Status = %d, want 201", r.Status)
		}
		// 0 ms is a real reading, not an absent one.
		if r.LatencyMs == nil || *r.LatencyMs != 0 {
			t.Errorf("LatencyMs = %v, want a present 0", r.LatencyMs)
		}
		if _, ok := r.Extra["status"]; ok {
			t.Error("status landed in Extra instead of the typed field")
		}
		wireEqualDoc(t, r, doc)
	})

	t.Run("recorded by an older engine", func(t *testing.T) {
		var r rift.RecordedRequest
		doc := `{"method":"GET","path":"/x"}`
		if err := json.Unmarshal([]byte(doc), &r); err != nil {
			t.Fatal(err)
		}
		if r.Status != 0 || r.LatencyMs != nil {
			t.Errorf("Status = %d, LatencyMs = %v; want both absent", r.Status, r.LatencyMs)
		}
		wireEqualDoc(t, r, doc)
	})
}
