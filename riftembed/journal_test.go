package riftembed_test

import (
	"fmt"
	"testing"

	"github.com/achird-labs/rift-go/rift"
)

func TestJournalRecordsStatusAndLatency(t *testing.T) {
	eng := startEngine(t)
	ctx := t.Context()
	port, err := eng.CreateImposter(ctx, rift.NewImposter("j").Record().
		Stub(rift.OnAny().Return(rift.Status(418))))
	if err != nil {
		t.Fatalf("CreateImposter: %v", err)
	}
	if _, status := httpGet(t, fmt.Sprintf("http://127.0.0.1:%d/tea", port)); status != 418 {
		t.Fatalf("status = %d, want 418", status)
	}
	recs, err := eng.Recorded(ctx, port)
	if err != nil {
		t.Fatalf("Recorded: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("journal = %+v, want one entry", recs)
	}
	if recs[0].Status != 418 {
		t.Errorf("Status = %d, want 418", recs[0].Status)
	}
	if recs[0].LatencyMs == nil {
		t.Error("LatencyMs is absent; engine 0.18.0 records it")
	}
}
