package rift_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/achird-labs/rift-go/rift"
)

// docs/dsl.md shows the array form reaching the engine through Extra; pin that it serialises.
func TestBehaviorsArrayFormTravelsThroughExtra(t *testing.T) {
	resp := rift.OKText("x").Build()
	resp.Extra = map[string]rift.JSON{"behaviors": []rift.JSON{
		map[string]rift.JSON{"decorate": "d"},
		map[string]rift.JSON{"shellTransform": "s"},
	}}
	raw, err := rift.ToJSON(resp)
	if err != nil {
		t.Fatalf("ToJSON: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	want := []any{map[string]any{"decorate": "d"}, map[string]any{"shellTransform": "s"}}
	if !reflect.DeepEqual(got["behaviors"], want) {
		t.Errorf("behaviors = %v, want %v (in %s)", got["behaviors"], want, raw)
	}
	if _, ok := got["_behaviors"]; ok {
		t.Errorf("the object form was written too: %s", raw)
	}
}
