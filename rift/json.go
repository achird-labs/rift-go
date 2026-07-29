package rift

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
)

// This file implements the escape-hatch contract: every open model struct carries an `Extra`
// map, and unknown-but-valid wire keys survive an Unmarshal → Marshal round-trip untouched.
//
// Go's encoding/json has no "inline the rest into a map" tag, so each open type gets a
// MarshalJSON/UnmarshalJSON pair built on the two helpers below. The `type shadow T` trick
// inside each method strips the methods from T, so json.Marshal(shadow) does the ordinary
// struct encoding instead of recursing into MarshalJSON forever.

// knownKeysCache memoises the json-tag key set per struct type.
var knownKeysCache sync.Map // reflect.Type -> map[string]struct{}

// knownKeys returns the set of wire keys a struct type declares via json tags.
// Fields tagged `json:"-"` (notably Extra itself) are not wire keys.
func knownKeys(t reflect.Type) map[string]struct{} {
	if cached, ok := knownKeysCache.Load(t); ok {
		return cached.(map[string]struct{})
	}
	keys := make(map[string]struct{}, t.NumField())
	for i := range t.NumField() {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		if name == "" {
			name = f.Name
		}
		keys[name] = struct{}{}
	}
	knownKeysCache.Store(t, keys)
	return keys
}

// marshalWithExtra encodes shadow (a struct value with no custom marshaller) and merges any
// extra keys that the struct does not already define. Declared fields always win: an Extra
// entry colliding with a known key is dropped rather than silently overwriting the typed value.
func marshalWithExtra(shadow any, extra map[string]JSON) ([]byte, error) {
	b, err := json.Marshal(shadow)
	if err != nil {
		return nil, err
	}
	if len(extra) == 0 {
		return b, nil
	}
	var merged map[string]json.RawMessage
	if err := json.Unmarshal(b, &merged); err != nil {
		return nil, err
	}
	if merged == nil {
		merged = make(map[string]json.RawMessage, len(extra))
	}
	for k, v := range extra {
		if _, declared := merged[k]; declared {
			continue
		}
		raw, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("marshal extra key %q: %w", k, err)
		}
		merged[k] = raw
	}
	return json.Marshal(merged)
}

// unmarshalWithExtra decodes data into shadow and captures every key shadow does not declare
// into *extra. A JSON null decodes to a zero value with no extras, not an error.
func unmarshalWithExtra(data []byte, shadow any, extra *map[string]JSON) error {
	if err := json.Unmarshal(data, shadow); err != nil {
		return err
	}
	var all map[string]JSON
	if err := json.Unmarshal(data, &all); err != nil {
		return err
	}
	if len(all) == 0 {
		*extra = nil
		return nil
	}
	known := knownKeys(reflect.TypeOf(shadow).Elem())
	rest := make(map[string]JSON)
	for k, v := range all {
		if _, isKnown := known[k]; !isKnown {
			rest[k] = v
		}
	}
	if len(rest) == 0 {
		rest = nil
	}
	*extra = rest
	return nil
}

// --- ImpostersConfig ---

func (c ImpostersConfig) MarshalJSON() ([]byte, error) {
	type shadow ImpostersConfig
	return marshalWithExtra(shadow(c), c.Extra)
}

func (c *ImpostersConfig) UnmarshalJSON(b []byte) error {
	type shadow ImpostersConfig
	var s shadow
	if err := unmarshalWithExtra(b, &s, &s.Extra); err != nil {
		return err
	}
	*c = ImpostersConfig(s)
	return nil
}

// --- Imposter ---

func (i Imposter) MarshalJSON() ([]byte, error) {
	type shadow Imposter
	return marshalWithExtra(shadow(i), i.Extra)
}

func (i *Imposter) UnmarshalJSON(b []byte) error {
	type shadow Imposter
	var s shadow
	if err := unmarshalWithExtra(b, &s, &s.Extra); err != nil {
		return err
	}
	*i = Imposter(s)
	return nil
}

// --- Stub ---

func (s Stub) MarshalJSON() ([]byte, error) {
	type shadow Stub
	return marshalWithExtra(shadow(s), s.Extra)
}

func (s *Stub) UnmarshalJSON(b []byte) error {
	type shadow Stub
	var sh shadow
	if err := unmarshalWithExtra(b, &sh, &sh.Extra); err != nil {
		return err
	}
	*s = Stub(sh)
	return nil
}

// --- Predicate ---

func (p Predicate) MarshalJSON() ([]byte, error) {
	type shadow Predicate
	return marshalWithExtra(shadow(p), p.Extra)
}

func (p *Predicate) UnmarshalJSON(b []byte) error {
	type shadow Predicate
	var s shadow
	if err := unmarshalWithExtra(b, &s, &s.Extra); err != nil {
		return err
	}
	*p = Predicate(s)
	return nil
}

// --- IsResponse ---

func (r IsResponse) MarshalJSON() ([]byte, error) {
	type shadow IsResponse
	return marshalWithExtra(shadow(r), r.Extra)
}

func (r *IsResponse) UnmarshalJSON(b []byte) error {
	type shadow IsResponse
	var s shadow
	if err := unmarshalWithExtra(b, &s, &s.Extra); err != nil {
		return err
	}
	*r = IsResponse(s)
	return nil
}

// --- ProxyResponse ---

func (p ProxyResponse) MarshalJSON() ([]byte, error) {
	type shadow ProxyResponse
	return marshalWithExtra(shadow(p), p.Extra)
}

func (p *ProxyResponse) UnmarshalJSON(b []byte) error {
	type shadow ProxyResponse
	var s shadow
	if err := unmarshalWithExtra(b, &s, &s.Extra); err != nil {
		return err
	}
	*p = ProxyResponse(s)
	return nil
}

// --- StubResponse ---

func (r StubResponse) MarshalJSON() ([]byte, error) {
	type shadow StubResponse
	return marshalWithExtra(shadow(r), r.Extra)
}

func (r *StubResponse) UnmarshalJSON(b []byte) error {
	type shadow StubResponse
	var s shadow
	if err := unmarshalWithExtra(b, &s, &s.Extra); err != nil {
		return err
	}
	*r = StubResponse(s)
	return nil
}

// --- Behaviors ---

func (b Behaviors) MarshalJSON() ([]byte, error) {
	type shadow Behaviors
	return marshalWithExtra(shadow(b), b.Extra)
}

func (b *Behaviors) UnmarshalJSON(data []byte) error {
	type shadow Behaviors
	var s shadow
	if err := unmarshalWithExtra(data, &s, &s.Extra); err != nil {
		return err
	}
	*b = Behaviors(s)
	return nil
}

// --- RiftImposter ---

func (r RiftImposter) MarshalJSON() ([]byte, error) {
	type shadow RiftImposter
	return marshalWithExtra(shadow(r), r.Extra)
}

func (r *RiftImposter) UnmarshalJSON(b []byte) error {
	type shadow RiftImposter
	var s shadow
	if err := unmarshalWithExtra(b, &s, &s.Extra); err != nil {
		return err
	}
	*r = RiftImposter(s)
	return nil
}

// --- RiftResponse ---

func (r RiftResponse) MarshalJSON() ([]byte, error) {
	type shadow RiftResponse
	return marshalWithExtra(shadow(r), r.Extra)
}

func (r *RiftResponse) UnmarshalJSON(b []byte) error {
	type shadow RiftResponse
	var s shadow
	if err := unmarshalWithExtra(b, &s, &s.Extra); err != nil {
		return err
	}
	*r = RiftResponse(s)
	return nil
}

// --- RecordedRequest ---

func (r RecordedRequest) MarshalJSON() ([]byte, error) {
	type shadow RecordedRequest
	return marshalWithExtra(shadow(r), r.Extra)
}

func (r *RecordedRequest) UnmarshalJSON(b []byte) error {
	type shadow RecordedRequest
	var s shadow
	if err := unmarshalWithExtra(b, &s, &s.Extra); err != nil {
		return err
	}
	*r = RecordedRequest(s)
	return nil
}

// --- InterceptRule ---

func (r InterceptRule) MarshalJSON() ([]byte, error) {
	type shadow InterceptRule
	return marshalWithExtra(shadow(r), r.Extra)
}

func (r *InterceptRule) UnmarshalJSON(b []byte) error {
	type shadow InterceptRule
	var s shadow
	if err := unmarshalWithExtra(b, &s, &s.Extra); err != nil {
		return err
	}
	*r = InterceptRule(s)
	return nil
}

// --- escape hatch ---

// ImposterFromJSON parses a raw imposter document into the typed model. Unknown keys are
// preserved, so ToJSON(ImposterFromJSON(x)) is faithful to x up to the omitted-vs-explicit-default
// normalizations rift-lint treats as equivalent.
//
// Use this when a config predates the DSL, is generated elsewhere, or exercises a corner of the
// grammar the builders do not yet cover.
func ImposterFromJSON(data []byte) (Imposter, error) {
	var imp Imposter
	if err := json.Unmarshal(data, &imp); err != nil {
		return Imposter{}, fmt.Errorf("%w: %w", ErrInvalidDefinition, err)
	}
	return imp, nil
}

// ImpostersFromJSON parses a bulk `{"imposters":[...]}` envelope.
func ImpostersFromJSON(data []byte) (ImpostersConfig, error) {
	var cfg ImpostersConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return ImpostersConfig{}, fmt.Errorf("%w: %w", ErrInvalidDefinition, err)
	}
	return cfg, nil
}

// ToJSON serializes any model value to its wire form.
func ToJSON(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidDefinition, err)
	}
	return b, nil
}
