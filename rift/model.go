// Package rift is the official Go SDK for Rift — a high-performance, Mountebank-compatible
// HTTP/HTTPS mock server.
//
// The package provides a typed wire model for the Rift/Mountebank imposter grammar, chainable
// builders that produce it, and transports that carry it to an engine (remote admin API, a
// spawned binary, or — via the companion riftembed package — an in-process engine over the C ABI).
//
// # Wire fidelity
//
// The model types use the EXACT wire keys the engine speaks (mostly camelCase — statusCode,
// caseSensitive, recordRequests — with a few snake_case keys the engine keeps, e.g.
// required_scenario_state). Every open structure carries an Extra map that round-trips
// unknown-but-valid fields untouched, so a config built by a future engine survives
// FromJSON → ToJSON unchanged.
package rift

import "encoding/json"

// JSON is an arbitrary JSON value: string, float64, bool, nil, []any, or map[string]any.
// It is the escape hatch used wherever the grammar accepts free-form JSON.
type JSON = any

// ImpostersConfig is the config-file / bulk envelope: {"imposters":[...]}.
type ImpostersConfig struct {
	Imposters []Imposter `json:"imposters"`

	// Extra preserves unknown top-level keys across a round-trip.
	Extra map[string]JSON `json:"-"`
}

// Imposter is a single mock server bound to a port.
type Imposter struct {
	// Port is the explicit listening port. Respected verbatim; omit (0) for an
	// engine-assigned port.
	Port            uint16          `json:"port,omitempty"`
	Protocol        string          `json:"protocol,omitempty"`
	Host            string          `json:"host,omitempty"`
	Name            string          `json:"name,omitempty"`
	Stubs           []Stub          `json:"stubs,omitempty"`
	RecordRequests  bool            `json:"recordRequests,omitempty"`
	RecordMatches   bool            `json:"recordMatches,omitempty"`
	DefaultResponse *IsResponse     `json:"defaultResponse,omitempty"`
	DefaultForward  string          `json:"defaultForward,omitempty"`
	AllowCORS       bool            `json:"allowCORS,omitempty"`
	MutualAuth      bool            `json:"mutualAuth,omitempty"`
	StrictBehaviors bool            `json:"strictBehaviors,omitempty"`
	Cert            string          `json:"cert,omitempty"`
	Key             string          `json:"key,omitempty"`
	Rift            *RiftImposter   `json:"_rift,omitempty"`
	Extra           map[string]JSON `json:"-"`
}

// Stub is a predicate set paired with the responses to serve when it matches.
type Stub struct {
	Predicates []Predicate    `json:"predicates,omitempty"`
	Responses  []StubResponse `json:"responses,omitempty"`
	ID         string         `json:"id,omitempty"`

	ScenarioName string `json:"scenarioName,omitempty"`
	// RequiredScenarioState and NewScenarioState gate and advance the scenario FSM.
	// These wire keys are snake_case (WireMock-compatible) — not a typo.
	RequiredScenarioState string `json:"required_scenario_state,omitempty"`
	NewScenarioState      string `json:"new_scenario_state,omitempty"`
	RoutePattern          string `json:"route_pattern,omitempty"`
	Space                 string `json:"space,omitempty"`
	RecordedFrom          string `json:"recorded_from,omitempty"`

	// Verify is an engine-ignored verification annotation, preserved across round-trip.
	// The conformance corpus carries expected transcripts here.
	Verify JSON `json:"_verify,omitempty"`

	Extra map[string]JSON `json:"-"`
}

// FieldMatch is a field → matcher map, e.g. {"method":"GET","path":"/x"}.
type FieldMatch map[string]JSON

// Predicate selects requests. Exactly one operator key is normally set, alongside optional
// matcher parameters (CaseSensitive, Except, ...) and selectors (XPath, JSONPath).
type Predicate struct {
	Equals     FieldMatch      `json:"equals,omitempty"`
	DeepEquals FieldMatch      `json:"deepEquals,omitempty"`
	Contains   FieldMatch      `json:"contains,omitempty"`
	StartsWith FieldMatch      `json:"startsWith,omitempty"`
	EndsWith   FieldMatch      `json:"endsWith,omitempty"`
	Matches    FieldMatch      `json:"matches,omitempty"`
	Exists     map[string]bool `json:"exists,omitempty"`

	Not *Predicate  `json:"not,omitempty"`
	And []Predicate `json:"and,omitempty"`
	Or  []Predicate `json:"or,omitempty"`

	Inject string `json:"inject,omitempty"`

	// Matcher parameters, flat alongside the operator.
	CaseSensitive    *bool  `json:"caseSensitive,omitempty"`
	KeyCaseSensitive *bool  `json:"keyCaseSensitive,omitempty"`
	Except           string `json:"except,omitempty"`

	XPath    *XPathSelector    `json:"xpath,omitempty"`
	JSONPath *JSONPathSelector `json:"jsonpath,omitempty"`

	Extra map[string]JSON `json:"-"`
}

// XPathSelector narrows a predicate to an XPath selection, with optional namespace bindings.
type XPathSelector struct {
	Selector string            `json:"selector"`
	NS       map[string]string `json:"ns,omitempty"`
}

// JSONPathSelector narrows a predicate to a JSONPath selection.
type JSONPathSelector struct {
	Selector string `json:"selector"`
}

// IsResponse is a canned response.
type IsResponse struct {
	// StatusCode is number-or-string on the wire: Mountebank serializes a string but accepts
	// a number, and both must round-trip. Use JSON so neither is lost.
	StatusCode JSON            `json:"statusCode,omitempty"`
	Headers    Headers         `json:"headers,omitempty"`
	Body       JSON            `json:"body,omitempty"`
	Mode       string          `json:"_mode,omitempty"`
	Extra      map[string]JSON `json:"-"`
}

// Headers maps a header name to a single value or a list of values.
type Headers map[string]JSON

// ProxyResponse forwards the request upstream, optionally recording it.
type ProxyResponse struct {
	To                  string            `json:"to"`
	Mode                string            `json:"mode,omitempty"`
	PredicateGenerators []JSON            `json:"predicateGenerators,omitempty"`
	AddWaitBehavior     bool              `json:"addWaitBehavior,omitempty"`
	AddDecorateBehavior string            `json:"addDecorateBehavior,omitempty"`
	InjectHeaders       map[string]string `json:"injectHeaders,omitempty"`
	PathRewrite         *PathRewrite      `json:"pathRewrite,omitempty"`
	Key                 string            `json:"key,omitempty"`
	Cert                string            `json:"cert,omitempty"`
	Extra               map[string]JSON   `json:"-"`
}

// PathRewrite rewrites the request path before proxying upstream.
type PathRewrite struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// StubResponse is one element of a stub's response cycle. Exactly one of Is, Proxy, Inject or
// Fault is normally set; the flat form (StatusCode/Headers/Body at the top level, no Is wrapper)
// is also accepted by the engine and round-trips.
type StubResponse struct {
	Is     *IsResponse    `json:"is,omitempty"`
	Proxy  *ProxyResponse `json:"proxy,omitempty"`
	Inject string         `json:"inject,omitempty"`
	Fault  string         `json:"fault,omitempty"`

	Behaviors *Behaviors    `json:"_behaviors,omitempty"`
	Rift      *RiftResponse `json:"_rift,omitempty"`

	// Flat form: statusCode/headers/body without the `is` wrapper.
	StatusCode JSON    `json:"statusCode,omitempty"`
	Headers    Headers `json:"headers,omitempty"`
	Body       JSON    `json:"body,omitempty"`

	Extra map[string]JSON `json:"-"`
}

// Behaviors post-process a response.
type Behaviors struct {
	// Wait is a number of milliseconds, a template string, or {"min":n,"max":n}.
	Wait           JSON            `json:"wait,omitempty"`
	Repeat         int             `json:"repeat,omitempty"`
	Decorate       string          `json:"decorate,omitempty"`
	ShellTransform JSON            `json:"shellTransform,omitempty"`
	Copy           JSON            `json:"copy,omitempty"`
	Lookup         JSON            `json:"lookup,omitempty"`
	Extra          map[string]JSON `json:"-"`
}

// RiftImposter is the `_rift` extension namespace on an imposter. Shapes are deliberately open:
// the SDK preserves them verbatim rather than modelling every sub-feature.
type RiftImposter struct {
	FlowState    JSON            `json:"flowState,omitempty"`
	Metrics      JSON            `json:"metrics,omitempty"`
	Proxy        JSON            `json:"proxy,omitempty"`
	ScriptEngine JSON            `json:"scriptEngine,omitempty"`
	Scripts      map[string]JSON `json:"scripts,omitempty"`
	Extra        map[string]JSON `json:"-"`
}

// RiftResponse is the `_rift` extension namespace on a response.
type RiftResponse struct {
	Fault     JSON            `json:"fault,omitempty"`
	Script    JSON            `json:"script,omitempty"`
	Templated bool            `json:"templated,omitempty"`
	Extra     map[string]JSON `json:"-"`
}

// RecordedRequest is a single request recorded by an imposter.
type RecordedRequest struct {
	RequestFrom string  `json:"request_from,omitempty"`
	Method      string  `json:"method"`
	Path        string  `json:"path"`
	Query       Headers `json:"query,omitempty"`
	Headers     Headers `json:"headers,omitempty"`
	Body        JSON    `json:"body,omitempty"`
	// Mode is "binary" when the engine base64-encoded a non-UTF-8 body; absent for text.
	Mode      string          `json:"_mode,omitempty"`
	Timestamp string          `json:"timestamp,omitempty"`
	Extra     map[string]JSON `json:"-"`
}

// InterceptRule routes a TLS-MITM-intercepted request. Host (exact match) or Predicates
// (AND-ed over the decrypted request) select which requests Action applies to; a rule with
// neither is a catch-all.
type InterceptRule struct {
	Host       string          `json:"host,omitempty"`
	Predicates []Predicate     `json:"predicates,omitempty"`
	Action     InterceptAction `json:"action"`
	Extra      map[string]JSON `json:"-"`
}

// InterceptAction is either a canned response or a plaintext re-proxy to a local imposter.
type InterceptAction struct {
	Serve   *IsResponse      `json:"serve,omitempty"`
	Forward *InterceptTarget `json:"forward,omitempty"`
}

// InterceptTarget names the local imposter port an intercepted request is forwarded to.
type InterceptTarget struct {
	Port uint16 `json:"port"`
}

// compile-time proof that the model marshals through encoding/json.
var (
	_ json.Marshaler   = (*Imposter)(nil)
	_ json.Unmarshaler = (*Imposter)(nil)
)
