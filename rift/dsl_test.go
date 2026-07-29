package rift

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

// jsonEqual compares two wire documents structurally: key order is irrelevant, per the
// conformance replay contract.
func jsonEqual(t *testing.T, got, want []byte) bool {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("unmarshal got: %v", err)
	}
	if err := json.Unmarshal(want, &w); err != nil {
		t.Fatalf("unmarshal want: %v", err)
	}
	return reflect.DeepEqual(g, w)
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := ToJSON(v)
	if err != nil {
		t.Fatalf("ToJSON: %v", err)
	}
	return b
}

func TestReadmeGrammarBuildsExpectedWire(t *testing.T) {
	imp := NewImposter("users").Record().
		Stub(OnGet("/api/users/1").
			WithHeader("Accept", Contains("json")).
			Return(OKJSON(map[string]JSON{"id": 1}))).
		Build()

	want := []byte(`{
	  "name": "users",
	  "recordRequests": true,
	  "stubs": [{
	    "predicates": [
	      {"equals": {"method": "GET", "path": "/api/users/1"}},
	      {"contains": {"headers": {"Accept": "json"}}}
	    ],
	    "responses": [{
	      "is": {
	        "statusCode": 200,
	        "headers": {"Content-Type": "application/json"},
	        "body": {"id": 1}
	      }
	    }]
	  }]
	}`)

	got := mustJSON(t, imp)
	if !jsonEqual(t, got, want) {
		t.Errorf("wire mismatch\ngot:  %s\nwant: %s", got, want)
	}
}

// Fields sharing an operator must collapse into one predicate — the shape the engine and the
// corpus fixtures use. Two separate `equals` predicates would AND identically but would fail
// the DSL-expressibility deep-equal against a fixture.
func TestSameOperatorFieldsMergeIntoOnePredicate(t *testing.T) {
	s := OnGet("/x").Build()
	if len(s.Predicates) != 1 {
		t.Fatalf("want 1 predicate, got %d: %+v", len(s.Predicates), s.Predicates)
	}
	if got := s.Predicates[0].Equals["method"]; got != "GET" {
		t.Errorf("method = %v", got)
	}
	if got := s.Predicates[0].Equals["path"]; got != "/x" {
		t.Errorf("path = %v", got)
	}
}

// Differing matcher parameters must NOT be merged: caseSensitive is a property of the
// predicate, not of the field, so merging would silently change the other field's semantics.
func TestDifferingParamsSplitPredicates(t *testing.T) {
	s := OnAny().
		WithPath(Equals("/x")).
		WithBody(Equals("y").CaseSensitive(true)).
		Build()

	if len(s.Predicates) != 2 {
		t.Fatalf("want 2 predicates, got %d: %+v", len(s.Predicates), s.Predicates)
	}
	var sensitive, insensitive int
	for _, p := range s.Predicates {
		if p.CaseSensitive != nil && *p.CaseSensitive {
			sensitive++
		} else {
			insensitive++
		}
	}
	if sensitive != 1 || insensitive != 1 {
		t.Errorf("want one sensitive + one insensitive, got %d/%d", sensitive, insensitive)
	}
}

func TestRepeatedHeadersMergeIntoOneMap(t *testing.T) {
	s := OnGet("/x").
		WithHeader("Accept", Contains("json")).
		WithHeader("X-Trace", Contains("abc")).
		Build()

	var containsPred *Predicate
	for i := range s.Predicates {
		if s.Predicates[i].Contains != nil {
			containsPred = &s.Predicates[i]
		}
	}
	if containsPred == nil {
		t.Fatal("no contains predicate")
	}
	headers, ok := containsPred.Contains["headers"].(map[string]JSON)
	if !ok {
		t.Fatalf("headers not an object: %T", containsPred.Contains["headers"])
	}
	if len(headers) != 2 || headers["Accept"] != "json" || headers["X-Trace"] != "abc" {
		t.Errorf("headers = %+v", headers)
	}
}

// Two stubs built from the same matcher value must not alias a shared map.
func TestSharedMatcherDoesNotAlias(t *testing.T) {
	m := Contains("json")
	a := OnGet("/a").WithHeader("Accept", m).Build()
	b := OnGet("/b").WithHeader("X-Other", m).Build()

	findHeaders := func(s Stub) map[string]JSON {
		for _, p := range s.Predicates {
			if p.Contains != nil {
				h, _ := p.Contains["headers"].(map[string]JSON)
				return h
			}
		}
		return nil
	}
	ha, hb := findHeaders(a), findHeaders(b)
	if _, leaked := ha["X-Other"]; leaked {
		t.Errorf("stub a leaked stub b's header: %+v", ha)
	}
	if _, leaked := hb["Accept"]; leaked {
		t.Errorf("stub b leaked stub a's header: %+v", hb)
	}
}

func TestResponseCycling(t *testing.T) {
	s := OnGet("/x").
		Return(OKText("first")).
		Return(OKText("second")).
		Build()
	if len(s.Responses) != 2 {
		t.Fatalf("want 2 responses, got %d", len(s.Responses))
	}
	if s.Responses[0].Is.Body != "first" || s.Responses[1].Is.Body != "second" {
		t.Errorf("cycle order wrong: %+v", s.Responses)
	}
}

func TestBehaviorsWireShape(t *testing.T) {
	r := OKText("x").After(250 * time.Millisecond).Repeat(3).Build()
	got := mustJSON(t, r)
	want := []byte(`{"is":{"statusCode":200,"headers":{"Content-Type":"text/plain"},"body":"x"},
	                 "_behaviors":{"wait":250,"repeat":3}}`)
	if !jsonEqual(t, got, want) {
		t.Errorf("behaviors wire mismatch\ngot:  %s\nwant: %s", got, want)
	}
}

func TestScenarioWireKeysAreSnakeCase(t *testing.T) {
	s := OnGet("/x").InScenario("retry").RequireState("start").SetState("done").Build()
	got := mustJSON(t, s)
	var m map[string]any
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"required_scenario_state", "new_scenario_state"} {
		if _, ok := m[k]; !ok {
			t.Errorf("missing snake_case key %q in %s", k, got)
		}
	}
	if m["scenarioName"] != "retry" {
		t.Errorf("scenarioName = %v", m["scenarioName"])
	}
}

// The escape-hatch contract: keys the model does not declare survive a round-trip.
func TestUnknownKeysRoundTrip(t *testing.T) {
	src := []byte(`{
	  "port": 4501,
	  "protocol": "http",
	  "someFutureKey": {"nested": [1, 2, 3]},
	  "stubs": [{
	    "name": "stub names are not modelled but must survive",
	    "predicates": [{"equals": {"method": "GET"}, "futureParam": true}],
	    "responses": [{"is": {"statusCode": 200, "futureField": "kept"}}]
	  }]
	}`)

	imp, err := ImposterFromJSON(src)
	if err != nil {
		t.Fatalf("ImposterFromJSON: %v", err)
	}
	got := mustJSON(t, imp)
	if !jsonEqual(t, got, src) {
		t.Errorf("round-trip lost data\ngot:  %s\nwant: %s", got, src)
	}
}

// A declared field must win over a colliding Extra entry, so an escape-hatch value can never
// silently overwrite typed state.
func TestDeclaredFieldsWinOverExtra(t *testing.T) {
	imp := Imposter{Port: 4501, Extra: map[string]JSON{"port": 9999}}
	got := mustJSON(t, imp)
	var m map[string]any
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatal(err)
	}
	if m["port"] != float64(4501) {
		t.Errorf("Extra overwrote a declared field: port = %v", m["port"])
	}
}

func TestStatusCodeAcceptsStringAndNumber(t *testing.T) {
	for _, src := range [][]byte{
		[]byte(`{"is":{"statusCode":200}}`),
		[]byte(`{"is":{"statusCode":"200"}}`),
	} {
		r, err := ResponseFromJSON(src)
		if err != nil {
			t.Fatalf("ResponseFromJSON(%s): %v", src, err)
		}
		if !jsonEqual(t, mustJSON(t, r), src) {
			t.Errorf("statusCode round-trip changed representation for %s", src)
		}
	}
}

func TestCompositePredicates(t *testing.T) {
	s := OnAny().WithPredicate(
		Or(PredicateOn("path", Equals("/a")), PredicateOn("path", Equals("/b"))),
		Not(PredicateOn("method", Equals("DELETE"))),
	).Build()

	got := mustJSON(t, s)
	want := []byte(`{"predicates":[
	  {"or":[{"equals":{"path":"/a"}},{"equals":{"path":"/b"}}]},
	  {"not":{"equals":{"method":"DELETE"}}}
	]}`)
	if !jsonEqual(t, got, want) {
		t.Errorf("composite mismatch\ngot:  %s\nwant: %s", got, want)
	}
}

func TestProxyBuilder(t *testing.T) {
	s := OnAny().Return(Proxy("http://upstream:8080").Once().InjectHeader("X-Via", "rift")).Build()
	got := mustJSON(t, s)
	want := []byte(`{"responses":[{"proxy":{
	  "to":"http://upstream:8080","mode":"proxyOnce","injectHeaders":{"X-Via":"rift"}}}]}`)
	if !jsonEqual(t, got, want) {
		t.Errorf("proxy mismatch\ngot:  %s\nwant: %s", got, want)
	}
}

func TestFaultResponse(t *testing.T) {
	got := mustJSON(t, Fault("CONNECTION_RESET_BY_PEER").Build())
	if !jsonEqual(t, got, []byte(`{"fault":"CONNECTION_RESET_BY_PEER"}`)) {
		t.Errorf("fault = %s", got)
	}
}
