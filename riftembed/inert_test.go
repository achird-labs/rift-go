package riftembed_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/achird-labs/rift-go/rift"
)

// The docs say the engine reports these keys as ignored; pin that against the real engine.
func TestEngineReportsInertKeys(t *testing.T) {
	eng := startEngine(t)
	ctx := t.Context()
	//nolint:staticcheck // the deprecated builder is what is under test
	port, err := eng.CreateImposter(ctx, rift.NewImposter("inert").RecordMatches().
		WithRift(&rift.RiftImposter{Metrics: map[string]rift.JSON{"port": 1}, Proxy: map[string]rift.JSON{}}).
		Stub(rift.OnGet("/x").Return(rift.OK())))
	if err != nil {
		t.Fatalf("CreateImposter: %v", err)
	}
	raw, err := eng.StubWarnings(ctx, port)
	if err != nil {
		t.Fatalf("StubWarnings: %v", err)
	}
	var warnings []struct {
		WarningType string `json:"warningType"`
		Message     string `json:"message"`
	}
	if err := json.Unmarshal(raw, &warnings); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	for _, key := range []string{"`recordMatches`", "`_rift.metrics`", "`_rift.proxy`"} {
		found := false
		for _, w := range warnings {
			if w.WarningType == "config_key_ignored" && strings.Contains(w.Message, key) {
				found = true
			}
		}
		if !found {
			t.Errorf("no config_key_ignored warning for %s in %s", key, raw)
		}
	}
}
