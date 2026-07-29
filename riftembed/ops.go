package riftembed

import (
	"encoding/json"
	"fmt"

	"github.com/achird-labs/rift-go/rift"
)

// This file is the engine's data plane: one Go method per C entry point, all sharing the
// withHandle discipline established in engine.go.

// --- imposters ---

// CreateImposter creates an imposter and returns the port it bound. A zero explicit port lets
// the engine assign one, which is what the returned value reports.
func (e *Engine) CreateImposter(src rift.ImposterSource) (uint16, error) {
	body, err := rift.ToJSON(src.BuildImposter())
	if err != nil {
		return 0, err
	}
	var port uint16
	err = e.withHandle(func(h uintptr) error {
		// 0 is never a live imposter port, so it doubles as the error sentinel.
		if port = e.sym.createImposter(h, string(body)); port == 0 {
			return e.lastError("create imposter")
		}
		return nil
	})
	return port, err
}

// DeleteImposter removes one imposter and frees its port.
func (e *Engine) DeleteImposter(port uint16) error {
	return e.withHandle(func(h uintptr) error {
		return e.checkRC("delete imposter", e.sym.deleteImposter(h, port))
	})
}

// DeleteAll removes every imposter.
func (e *Engine) DeleteAll() error {
	return e.withHandle(func(h uintptr) error {
		return e.checkRC("delete all imposters", e.sym.deleteAll(h))
	})
}

// ListOptions selects the projection returned by ListImposters and GetImposter.
type ListOptions struct {
	// Replayable returns full ImposterConfig documents rather than a summary.
	Replayable bool `json:"replayable,omitempty"`
	// RemoveProxies strips proxy responses from the projection.
	RemoveProxies bool `json:"removeProxies,omitempty"`
}

// ListImposters returns the engine's imposters. With Replayable set the result is
// {"imposters":[<ImposterConfig>...]}; otherwise a Mountebank-style summary.
func (e *Engine) ListImposters(opts ListOptions) (json.RawMessage, error) {
	body, err := json.Marshal(opts)
	if err != nil {
		return nil, err
	}
	var out json.RawMessage
	err = e.withHandle(func(h uintptr) error {
		out, err = e.takeJSON("list imposters", e.sym.listImposters(h, string(body)))
		return err
	})
	return out, err
}

// GetImposter returns one imposter's projection.
func (e *Engine) GetImposter(port uint16, opts ListOptions) (json.RawMessage, error) {
	body, err := json.Marshal(opts)
	if err != nil {
		return nil, err
	}
	var out json.RawMessage
	err = e.withHandle(func(h uintptr) error {
		out, err = e.takeJSON("get imposter", e.sym.getImposter(h, port, string(body)))
		return err
	})
	return out, err
}

// SetImposterEnabled enables or disables an imposter without deleting it.
func (e *Engine) SetImposterEnabled(port uint16, enabled bool) error {
	flag := int32(0)
	if enabled {
		flag = 1
	}
	return e.withHandle(func(h uintptr) error {
		return e.checkRC("set imposter enabled", e.sym.setImposterEnable(h, port, flag))
	})
}

// ApplyConfig applies a whole config document — the `{"imposters":[...]}` envelope — replacing
// the engine's current state. It returns the engine's reload report.
func (e *Engine) ApplyConfig(cfg rift.ImpostersConfig) (json.RawMessage, error) {
	body, err := rift.ToJSON(cfg)
	if err != nil {
		return nil, err
	}
	var out json.RawMessage
	err = e.withHandle(func(h uintptr) error {
		out, err = e.takeJSON("apply config", e.sym.applyConfig(h, string(body)))
		return err
	})
	return out, err
}

// --- stubs ---

// ReplaceStubs replaces every stub on an imposter.
func (e *Engine) ReplaceStubs(port uint16, stubs []rift.Stub) error {
	body, err := rift.ToJSON(stubs)
	if err != nil {
		return err
	}
	return e.withHandle(func(h uintptr) error {
		return e.checkRC("replace stubs", e.sym.replaceStubs(h, port, string(body)))
	})
}

// AddStub inserts a stub at index, or appends when index is negative.
func (e *Engine) AddStub(port uint16, src rift.StubSource, index int) error {
	body, err := rift.ToJSON(src.BuildStub())
	if err != nil {
		return err
	}
	return e.withHandle(func(h uintptr) error {
		return e.checkRC("add stub", e.sym.addStub(h, port, string(body), int32(index)))
	})
}

// StubRef addresses one stub, by index or by id.
type StubRef struct {
	Index *int   `json:"index,omitempty"`
	ID    string `json:"id,omitempty"`
}

// StubAt addresses a stub by position.
func StubAt(i int) StubRef { return StubRef{Index: &i} }

// StubByID addresses a stub by its explicit id.
func StubByID(id string) StubRef { return StubRef{ID: id} }

// GetStub returns one stub.
func (e *Engine) GetStub(port uint16, ref StubRef) (json.RawMessage, error) {
	refJSON, err := json.Marshal(ref)
	if err != nil {
		return nil, err
	}
	var out json.RawMessage
	err = e.withHandle(func(h uintptr) error {
		out, err = e.takeJSON("get stub", e.sym.getStub(h, port, string(refJSON)))
		return err
	})
	return out, err
}

// UpdateStub replaces one stub in place.
func (e *Engine) UpdateStub(port uint16, ref StubRef, src rift.StubSource) error {
	refJSON, err := json.Marshal(ref)
	if err != nil {
		return err
	}
	body, err := rift.ToJSON(src.BuildStub())
	if err != nil {
		return err
	}
	return e.withHandle(func(h uintptr) error {
		return e.checkRC("update stub", e.sym.updateStub(h, port, string(refJSON), string(body)))
	})
}

// DeleteStub removes one stub.
func (e *Engine) DeleteStub(port uint16, ref StubRef) error {
	refJSON, err := json.Marshal(ref)
	if err != nil {
		return err
	}
	return e.withHandle(func(h uintptr) error {
		return e.checkRC("delete stub", e.sym.deleteStub(h, port, string(refJSON)))
	})
}

// StubWarnings returns the engine's stub-overlap analysis: duplicate, shadowed and catch-all
// stubs. Computed on mutation and cached, so this is a cheap read.
func (e *Engine) StubWarnings(port uint16) (json.RawMessage, error) {
	var out json.RawMessage
	var err error
	err = e.withHandle(func(h uintptr) error {
		out, err = e.takeJSON("stub warnings", e.sym.stubWarnings(h, port))
		return err
	})
	return out, err
}

// --- recording and verification ---

// Recorded returns the imposter's request journal. The imposter must have been created with
// recording enabled (NewImposter(...).Record()) or the journal is empty.
func (e *Engine) Recorded(port uint16) ([]rift.RecordedRequest, error) {
	var raw json.RawMessage
	var err error
	if err = e.withHandle(func(h uintptr) error {
		raw, err = e.takeJSON("recorded requests", e.sym.recorded(h, port))
		return err
	}); err != nil {
		return nil, err
	}
	var out []rift.RecordedRequest
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("%w: decode recorded requests: %w", rift.ErrInvalidDefinition, err)
	}
	return out, nil
}

// ClearRecorded empties the request journal.
func (e *Engine) ClearRecorded(port uint16) error {
	return e.withHandle(func(h uintptr) error {
		return e.checkRC("clear recorded", e.sym.clearRecorded(h, port))
	})
}

// ClearProxyRecordings drops stubs recorded by proxy responses.
func (e *Engine) ClearProxyRecordings(port uint16) error {
	return e.withHandle(func(h uintptr) error {
		return e.checkRC("clear proxy recordings", e.sym.clearProxyRecordings(h, port))
	})
}

// Verify counts journal entries matching a predicate set, evaluated by the engine's own
// predicate engine rather than client-side. That matters for xpath and inject predicates,
// which are impractical to reimplement in the SDK.
func (e *Engine) Verify(port uint16, req rift.VerifyRequest) (rift.VerifyResult, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return rift.VerifyResult{}, err
	}
	var raw json.RawMessage
	if err := e.withHandle(func(h uintptr) error {
		raw, err = e.takeJSON("verify", e.sym.verify(h, port, string(body)))
		return err
	}); err != nil {
		return rift.VerifyResult{}, err
	}
	var out rift.VerifyResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return rift.VerifyResult{}, fmt.Errorf("%w: decode verify result: %w",
			rift.ErrInvalidDefinition, err)
	}
	return out, nil
}

// --- scenarios ---

// Scenarios returns the scenario states for an imposter, optionally scoped to a flow.
func (e *Engine) Scenarios(port uint16, flowID string) (json.RawMessage, error) {
	var out json.RawMessage
	var err error
	err = e.withHandle(func(h uintptr) error {
		out, err = e.takeJSON("scenarios", e.sym.scenarios(h, port, flowID))
		return err
	})
	return out, err
}

// SetScenarioState forces a scenario into a state.
func (e *Engine) SetScenarioState(port uint16, scenario, state, flowID string) error {
	return e.withHandle(func(h uintptr) error {
		return e.checkRC("set scenario state",
			e.sym.setScenarioState(h, port, scenario, state, flowID))
	})
}

// ResetScenarios returns every scenario to its start state.
func (e *Engine) ResetScenarios(port uint16, flowID string) error {
	return e.withHandle(func(h uintptr) error {
		return e.checkRC("reset scenarios", e.sym.resetScenarios(h, port, flowID))
	})
}

// --- spaces and flow state ---
//
// A space is a per-flow overlay on a shared imposter: parallel test shards can hit one port and
// stay isolated, partitioned by flow id.

// SpaceAddStub adds a stub scoped to one flow.
func (e *Engine) SpaceAddStub(port uint16, flowID string, src rift.StubSource) error {
	body, err := rift.ToJSON(src.BuildStub())
	if err != nil {
		return err
	}
	return e.withHandle(func(h uintptr) error {
		return e.checkRC("space add stub", e.sym.spaceAddStub(h, port, flowID, string(body)))
	})
}

// SpaceListStubs returns the stubs scoped to one flow.
func (e *Engine) SpaceListStubs(port uint16, flowID string) (json.RawMessage, error) {
	var out json.RawMessage
	var err error
	err = e.withHandle(func(h uintptr) error {
		out, err = e.takeJSON("space list stubs", e.sym.spaceListStubs(h, port, flowID))
		return err
	})
	return out, err
}

// SpaceDelete removes a flow's space and everything in it.
func (e *Engine) SpaceDelete(port uint16, flowID string) error {
	return e.withHandle(func(h uintptr) error {
		return e.checkRC("space delete", e.sym.spaceDelete(h, port, flowID))
	})
}

// SpaceRecorded returns the request journal scoped to one flow.
func (e *Engine) SpaceRecorded(port uint16, flowID string) ([]rift.RecordedRequest, error) {
	var raw json.RawMessage
	var err error
	if err = e.withHandle(func(h uintptr) error {
		raw, err = e.takeJSON("space recorded", e.sym.spaceRecorded(h, port, flowID))
		return err
	}); err != nil {
		return nil, err
	}
	var out []rift.RecordedRequest
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("%w: decode space recorded: %w", rift.ErrInvalidDefinition, err)
	}
	return out, nil
}

// FlowStateGet reads one key from a flow's state bag.
func (e *Engine) FlowStateGet(port uint16, flowID, key string) (json.RawMessage, error) {
	var out json.RawMessage
	var err error
	err = e.withHandle(func(h uintptr) error {
		out, err = e.takeJSON("flow state get", e.sym.flowStateGet(h, port, flowID, key))
		return err
	})
	return out, err
}

// FlowStatePut writes one key into a flow's state bag.
func (e *Engine) FlowStatePut(port uint16, flowID, key, value string) error {
	return e.withHandle(func(h uintptr) error {
		return e.checkRC("flow state put", e.sym.flowStatePut(h, port, flowID, key, value))
	})
}

// FlowStateDelete drops a flow's entire state bag.
func (e *Engine) FlowStateDelete(port uint16, flowID string) error {
	return e.withHandle(func(h uintptr) error {
		return e.checkRC("flow state delete", e.sym.flowStateDelete(h, port, flowID))
	})
}

// --- admin plane ---

// ServeOptions configures the in-process admin/metrics plane.
type ServeOptions struct {
	Port   uint16 `json:"port,omitempty"`
	Host   string `json:"host,omitempty"`
	APIKey string `json:"apiKey,omitempty"`
}

// ServeAdmin starts the admin API over this engine and returns the engine's description of the
// bound listener.
//
// Note: a blank or whitespace APIKey is rejected by current engines rather than silently
// enabling a gate that authenticates everyone. Leave it empty to run without an API key.
func (e *Engine) ServeAdmin(opts ServeOptions) (json.RawMessage, error) {
	body, err := json.Marshal(opts)
	if err != nil {
		return nil, err
	}
	var out json.RawMessage
	err = e.withHandle(func(h uintptr) error {
		out, err = e.takeJSON("serve admin", e.sym.serveAdmin(h, string(body)))
		return err
	})
	return out, err
}
