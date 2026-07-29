package rift

// Matchers describe how one request field is compared. They are values, not builders, so they
// compose freely:
//
//	OnGet("/users").WithHeader("Accept", Contains("json"))
//
// A matcher carries its operator and any per-predicate parameters (case sensitivity, an
// `except` strip pattern). The stub builder groups fields that share an operator *and*
// parameters into a single wire predicate, which is what the engine expects:
//
//	{"equals": {"method": "GET", "path": "/users"}}

// Matcher is one field comparison: an operator, a value, and optional parameters.
type Matcher struct {
	op    string
	value JSON

	caseSensitive    *bool
	keyCaseSensitive *bool
	except           string
}

// Equals matches the field exactly.
func Equals(v JSON) Matcher { return Matcher{op: "equals", value: v} }

// DeepEquals matches the field structurally, requiring the whole object to correspond
// (no extra keys). Use it for query/header maps where Equals would allow extras.
func DeepEquals(v JSON) Matcher { return Matcher{op: "deepEquals", value: v} }

// Contains matches when the field contains the value as a substring.
func Contains(v JSON) Matcher { return Matcher{op: "contains", value: v} }

// StartsWith matches a prefix.
func StartsWith(v JSON) Matcher { return Matcher{op: "startsWith", value: v} }

// EndsWith matches a suffix.
func EndsWith(v JSON) Matcher { return Matcher{op: "endsWith", value: v} }

// Matches applies a regular expression to the field.
func Matches(pattern string) Matcher { return Matcher{op: "matches", value: pattern} }

// Exists matches on the presence (true) or absence (false) of the field.
func Exists(present bool) Matcher { return Matcher{op: "exists", value: present} }

// CaseSensitive sets case sensitivity for this matcher's predicate. The engine default is
// case-insensitive for most string comparisons.
func (m Matcher) CaseSensitive(on bool) Matcher {
	m.caseSensitive = &on
	return m
}

// KeyCaseSensitive sets case sensitivity for object *keys* (headers, query parameters).
func (m Matcher) KeyCaseSensitive(on bool) Matcher {
	m.keyCaseSensitive = &on
	return m
}

// Except strips text matching this regular expression from the field before comparing.
func (m Matcher) Except(pattern string) Matcher {
	m.except = pattern
	return m
}

// params returns a comparable signature for grouping: fields sharing an operator can only be
// merged into one predicate when their parameters agree too.
func (m Matcher) params() matcherParams {
	p := matcherParams{except: m.except}
	if m.caseSensitive != nil {
		p.caseSensitive, p.hasCaseSensitive = *m.caseSensitive, true
	}
	if m.keyCaseSensitive != nil {
		p.keyCaseSensitive, p.hasKeyCaseSensitive = *m.keyCaseSensitive, true
	}
	return p
}

type matcherParams struct {
	caseSensitive       bool
	hasCaseSensitive    bool
	keyCaseSensitive    bool
	hasKeyCaseSensitive bool
	except              string
}

// apply writes the parameters onto a predicate being assembled.
func (p matcherParams) apply(pred *Predicate) {
	if p.hasCaseSensitive {
		v := p.caseSensitive
		pred.CaseSensitive = &v
	}
	if p.hasKeyCaseSensitive {
		v := p.keyCaseSensitive
		pred.KeyCaseSensitive = &v
	}
	if p.except != "" {
		pred.Except = p.except
	}
}

// setField writes value under the operator's field map on pred.
func setField(pred *Predicate, op, field string, value JSON) {
	ensure := func(m *FieldMatch) FieldMatch {
		if *m == nil {
			*m = FieldMatch{}
		}
		return *m
	}
	switch op {
	case "equals":
		ensure(&pred.Equals)[field] = value
	case "deepEquals":
		ensure(&pred.DeepEquals)[field] = value
	case "contains":
		ensure(&pred.Contains)[field] = value
	case "startsWith":
		ensure(&pred.StartsWith)[field] = value
	case "endsWith":
		ensure(&pred.EndsWith)[field] = value
	case "matches":
		ensure(&pred.Matches)[field] = value
	case "exists":
		if pred.Exists == nil {
			pred.Exists = map[string]bool{}
		}
		// Exists takes a bool; anything else is a caller error caught at build time.
		b, _ := value.(bool)
		pred.Exists[field] = b
	}
}

// --- composite predicates ---

// Not inverts a predicate.
func Not(p Predicate) Predicate { return Predicate{Not: &p} }

// And requires every predicate to match.
func And(ps ...Predicate) Predicate { return Predicate{And: ps} }

// Or requires at least one predicate to match.
func Or(ps ...Predicate) Predicate { return Predicate{Or: ps} }

// Inject builds a predicate evaluated by a JavaScript function. The engine must be started with
// injection enabled (--allowInjection) for this to be accepted over the admin API; the embedded
// C ABI accepts it unconditionally, matching how it accepts inject stubs.
func Inject(js string) Predicate { return Predicate{Inject: js} }

// PredicateOn builds a single-field predicate directly, for cases where the stub builder's
// fluent form does not fit — a predicate nested inside And/Or/Not, for instance.
//
//	Or(PredicateOn("path", Equals("/a")), PredicateOn("path", Equals("/b")))
func PredicateOn(field string, m Matcher) Predicate {
	var p Predicate
	setField(&p, m.op, field, m.value)
	m.params().apply(&p)
	return p
}

// WithXPath narrows a predicate to an XPath selection over the request body.
func (p Predicate) WithXPath(selector string, ns map[string]string) Predicate {
	p.XPath = &XPathSelector{Selector: selector, NS: ns}
	return p
}

// WithJSONPath narrows a predicate to a JSONPath selection over the request body.
func (p Predicate) WithJSONPath(selector string) Predicate {
	p.JSONPath = &JSONPathSelector{Selector: selector}
	return p
}
