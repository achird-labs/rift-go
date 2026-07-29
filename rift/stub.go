package rift

// StubSource is anything that can produce a wire Stub: the fluent builder, or a Stub the caller
// assembled directly.
type StubSource interface {
	BuildStub() Stub
}

func (s Stub) BuildStub() Stub { return s }

// fieldMatcher pairs a request field with how it is compared.
type fieldMatcher struct {
	field   string
	matcher Matcher
}

// StubBuilder builds one stub: the predicates that select requests, and the response cycle
// served when they match.
type StubBuilder struct {
	fields []fieldMatcher
	// raw predicates are appended after the grouped field predicates, preserving the order
	// the caller added them.
	raw   []Predicate
	stub  Stub
	errs  []error
	built bool
}

// On starts a stub matching an exact method and path.
func On(method, path string) *StubBuilder {
	return &StubBuilder{fields: []fieldMatcher{
		{field: "method", matcher: Equals(method)},
		{field: "path", matcher: Equals(path)},
	}}
}

// OnGet, OnPost, OnPut, OnPatch, OnDelete and OnHead start a stub for the named method.
func OnGet(path string) *StubBuilder    { return On("GET", path) }
func OnPost(path string) *StubBuilder   { return On("POST", path) }
func OnPut(path string) *StubBuilder    { return On("PUT", path) }
func OnPatch(path string) *StubBuilder  { return On("PATCH", path) }
func OnDelete(path string) *StubBuilder { return On("DELETE", path) }
func OnHead(path string) *StubBuilder   { return On("HEAD", path) }

// OnAny starts a stub with no predicates — a catch-all that matches every request.
func OnAny() *StubBuilder { return &StubBuilder{} }

// WithMethod constrains the request method.
func (b *StubBuilder) WithMethod(m Matcher) *StubBuilder { return b.withField("method", m) }

// WithPath constrains the request path.
func (b *StubBuilder) WithPath(m Matcher) *StubBuilder { return b.withField("path", m) }

// WithBody constrains the request body.
func (b *StubBuilder) WithBody(m Matcher) *StubBuilder { return b.withField("body", m) }

// WithHeader constrains one request header.
func (b *StubBuilder) WithHeader(name string, m Matcher) *StubBuilder {
	return b.withNested("headers", name, m)
}

// WithQuery constrains one query parameter.
func (b *StubBuilder) WithQuery(name string, m Matcher) *StubBuilder {
	return b.withNested("query", name, m)
}

// WithPredicate appends a predicate directly — the escape hatch for composites (And/Or/Not),
// injected predicates, and selector-scoped predicates.
func (b *StubBuilder) WithPredicate(ps ...Predicate) *StubBuilder {
	b.raw = append(b.raw, ps...)
	return b
}

func (b *StubBuilder) withField(field string, m Matcher) *StubBuilder {
	b.fields = append(b.fields, fieldMatcher{field: field, matcher: m})
	return b
}

// withNested handles headers/query, where the matcher applies to one key inside an object
// field. The wire shape nests the key under the field: {"equals":{"headers":{"Accept":"json"}}}.
func (b *StubBuilder) withNested(field, key string, m Matcher) *StubBuilder {
	inner := m
	inner.value = map[string]JSON{key: m.value}
	b.fields = append(b.fields, fieldMatcher{field: field, matcher: inner})
	return b
}

// Return appends a response to the cycle. Call it repeatedly to cycle through responses:
//
//	OnGet("/x").Return(OKText("first")).Return(OKText("second"))
func (b *StubBuilder) Return(rs ...ResponseSource) *StubBuilder {
	for _, r := range rs {
		b.stub.Responses = append(b.stub.Responses, r.BuildResponse())
	}
	return b
}

// WithID sets an explicit stub id, so it can be addressed for update/delete later.
func (b *StubBuilder) WithID(id string) *StubBuilder {
	b.stub.ID = id
	return b
}

// InScenario attaches the stub to a named scenario FSM.
func (b *StubBuilder) InScenario(name string) *StubBuilder {
	b.stub.ScenarioName = name
	return b
}

// RequireState gates the stub on the scenario being in state s.
func (b *StubBuilder) RequireState(s string) *StubBuilder {
	b.stub.RequiredScenarioState = s
	return b
}

// SetState advances the scenario to state s when the stub matches.
func (b *StubBuilder) SetState(s string) *StubBuilder {
	b.stub.NewScenarioState = s
	return b
}

// InSpace scopes the stub to a named space, so parallel flows sharing a port stay isolated.
func (b *StubBuilder) InSpace(space string) *StubBuilder {
	b.stub.Space = space
	return b
}

// WithRoutePattern sets the route pattern used for grouping in stub analysis.
func (b *StubBuilder) WithRoutePattern(p string) *StubBuilder {
	b.stub.RoutePattern = p
	return b
}

// Build assembles the wire stub. Field matchers sharing an operator *and* its parameters are
// merged into one predicate — {"equals":{"method":"GET","path":"/x"}} rather than two separate
// equals predicates — which is the shape the engine and the corpus fixtures use.
func (b *StubBuilder) Build() Stub {
	s := b.stub

	type groupKey struct {
		op     string
		params matcherParams
	}
	var order []groupKey
	groups := map[groupKey]*Predicate{}

	for _, fm := range b.fields {
		k := groupKey{op: fm.matcher.op, params: fm.matcher.params()}
		pred, seen := groups[k]
		if !seen {
			pred = &Predicate{}
			k.params.apply(pred)
			groups[k] = pred
			order = append(order, k)
		}
		mergeField(pred, fm.matcher.op, fm.field, fm.matcher.value)
	}

	preds := make([]Predicate, 0, len(order)+len(b.raw))
	for _, k := range order {
		preds = append(preds, *groups[k])
	}
	preds = append(preds, b.raw...)
	if len(preds) > 0 {
		s.Predicates = preds
	}
	return s
}

func (b *StubBuilder) BuildStub() Stub { return b.Build() }

// mergeField writes field→value into pred under op, merging object-valued fields (headers,
// query) rather than overwriting them, so two WithHeader calls produce one headers map.
func mergeField(pred *Predicate, op, field string, value JSON) {
	nested, isNested := value.(map[string]JSON)
	if !isNested {
		setField(pred, op, field, value)
		return
	}
	existing := existingField(pred, op, field)
	if prev, ok := existing.(map[string]JSON); ok {
		for k, v := range nested {
			prev[k] = v
		}
		return
	}
	// Copy so two stubs built from a shared matcher never alias the same map.
	merged := make(map[string]JSON, len(nested))
	for k, v := range nested {
		merged[k] = v
	}
	setField(pred, op, field, merged)
}

// existingField reads back a field already written under op, or nil.
func existingField(pred *Predicate, op, field string) JSON {
	var m FieldMatch
	switch op {
	case "equals":
		m = pred.Equals
	case "deepEquals":
		m = pred.DeepEquals
	case "contains":
		m = pred.Contains
	case "startsWith":
		m = pred.StartsWith
	case "endsWith":
		m = pred.EndsWith
	case "matches":
		m = pred.Matches
	case "exists":
		m = pred.Exists
	default:
		return nil
	}
	if m == nil {
		return nil
	}
	return m[field]
}
