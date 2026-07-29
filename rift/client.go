package rift

import "context"

// Client is the engine surface every transport implements: the remote admin API
// (Connect), a managed child process (Spawn), and the in-process engine
// (riftembed.Engine).
//
// One interface across all three is what lets a test suite — including the SDK conformance
// corpus — run identically against an embedded engine and a remote one. If a capability cannot
// be offered on some transport it does not belong here; it belongs on the concrete type.
//
// Every method takes a context. The remote and spawn transports honour cancellation fully. The
// embedded transport checks the context before it calls into native code but cannot interrupt a
// downcall already in flight — documented rather than pretended otherwise.
type Client interface {
	// CreateImposter creates an imposter and returns the port it bound.
	CreateImposter(ctx context.Context, src ImposterSource) (uint16, error)
	// DeleteImposter removes one imposter and frees its port.
	DeleteImposter(ctx context.Context, port uint16) error
	// DeleteAll removes every imposter.
	DeleteAll(ctx context.Context) error

	// ReplaceStubs replaces every stub on an imposter.
	ReplaceStubs(ctx context.Context, port uint16, stubs []Stub) error
	// AddStub inserts a stub at index, or appends when index is negative.
	AddStub(ctx context.Context, port uint16, src StubSource, index int) error

	// Recorded returns the imposter's request journal.
	Recorded(ctx context.Context, port uint16) ([]RecordedRequest, error)
	// ClearRecorded empties the request journal.
	ClearRecorded(ctx context.Context, port uint16) error
	// Verify counts journal entries matching a predicate set, engine-side.
	Verify(ctx context.Context, port uint16, req VerifyRequest) (VerifyResult, error)

	// SetScenarioState forces a scenario into a state.
	SetScenarioState(ctx context.Context, port uint16, scenario, state, flowID string) error
	// ResetScenarios returns every scenario to its start state.
	ResetScenarios(ctx context.Context, port uint16, flowID string) error

	// Close releases the transport's resources: the HTTP client's idle connections, the child
	// process, or the native handle and library.
	Close() error
}

// BaseURLFor returns the URL an imposter created through c listens at. Transports that front
// imposters on a different host than localhost override this via HostFor.
func BaseURLFor(c Client, protocol string, port uint16) string {
	if h, ok := c.(interface{ HostFor(uint16) string }); ok {
		if protocol == "" {
			protocol = "http"
		}
		return protocol + "://" + h.HostFor(port)
	}
	return BaseURL(protocol, port)
}
