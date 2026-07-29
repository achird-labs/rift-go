// Package rifttest wires Rift into Go's testing package.
//
// The shape it optimises for is the common one: a test needs a mock API, wants it torn down
// automatically, and wants a failed assertion to say what actually arrived rather than just
// "want 1, got 0".
//
//	func TestUserLookup(t *testing.T) {
//	    users := rifttest.Imposter(t, rift.NewImposter("users").Record().
//	        Stub(rift.OnGet("/api/users/1").Return(rift.OKJSON(`{"id":1}`))))
//
//	    callSUT(t, users.BaseURL())
//
//	    rifttest.AssertReceived(t, users, rift.OnGet("/api/users/1"), rift.Once())
//	}
//
// # One engine per test binary
//
// Starting an engine costs a runtime and its threads. Doing that per test would dominate the
// runtime of a large suite, so the engine is created once, lazily, and shared. Isolation comes
// from each test getting its own imposter on its own port, destroyed at t.Cleanup — not from a
// fresh engine.
package rifttest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/achird-labs/rift-go/rift"
	"github.com/achird-labs/rift-go/riftembed"
)

// Options configure the shared engine. They take effect on the first call that creates it;
// later calls reuse the existing engine and ignore their options.
type Options struct {
	// Client supplies an engine instead of starting one. Use it to run a suite against a
	// remote engine, or a spawned binary, without changing the tests.
	Client rift.Client

	// LibraryPath is an explicit native library path for the default embedded engine.
	LibraryPath string

	// SkipIfUnavailable skips tests instead of failing them when no engine can be started.
	// Useful for a suite that should stay green on a machine with no native library.
	SkipIfUnavailable bool
}

var (
	sharedMu   sync.Mutex
	sharedEng  rift.Client
	sharedErr  error
	sharedOnce bool
)

// Engine returns the shared engine, starting it on first use.
//
// The default is an in-process engine, which needs no binary and no port, and which the test
// binary's exit reclaims. Supply Options.Client to run the same tests against another transport.
func Engine(t *testing.T, opts ...Options) rift.Client {
	t.Helper()
	var o Options
	if len(opts) > 0 {
		o = opts[0]
	}

	sharedMu.Lock()
	defer sharedMu.Unlock()

	if !sharedOnce {
		sharedOnce = true
		if o.Client != nil {
			sharedEng = o.Client
		} else {
			sharedEng, sharedErr = riftembed.Start(riftembed.Options{LibraryPath: o.LibraryPath})
		}
	}
	if sharedErr != nil {
		if o.SkipIfUnavailable {
			t.Skipf("rifttest: no engine available: %v", sharedErr)
		}
		t.Fatalf("rifttest: could not start an engine: %v\n"+
			"  fix: install the native library (see riftembed.LibraryPath), or pass\n"+
			"       rifttest.Options{Client: ...} to use a remote or spawned engine", sharedErr)
	}
	return sharedEng
}

// Close shuts down the shared engine. Call it from TestMain when the engine owns a child
// process or a remote connection that should not outlive the suite:
//
//	func TestMain(m *testing.M) {
//	    code := m.Run()
//	    rifttest.Close()
//	    os.Exit(code)
//	}
//
// The default in-process engine does not require this — the binary's exit releases it.
func Close() error {
	sharedMu.Lock()
	defer sharedMu.Unlock()
	if sharedEng == nil {
		return nil
	}
	err := sharedEng.Close()
	sharedEng, sharedOnce, sharedErr = nil, false, nil
	return err
}

// Handle is a live imposter scoped to one test.
type Handle struct {
	client   rift.Client
	port     uint16
	protocol string
	name     string
	t        *testing.T
}

// Imposter creates an imposter on the shared engine and destroys it when the test ends.
//
// Recording is enabled automatically unless the definition already sets it: an imposter you
// cannot assert against is rarely what a test wants, and forgetting Record() produces a
// confusing empty journal rather than an error.
func Imposter(t *testing.T, src rift.ImposterSource, opts ...Options) *Handle {
	t.Helper()
	client := Engine(t, opts...)

	def := src.BuildImposter()
	if !def.RecordRequests {
		def.RecordRequests = true
	}
	protocol := def.Protocol
	if protocol == "" {
		protocol = "http"
	}

	ctx := t.Context()
	port, err := client.CreateImposter(ctx, def)
	if err != nil {
		t.Fatalf("rifttest: create imposter %q: %v", def.Name, err)
	}

	h := &Handle{client: client, port: port, protocol: protocol, name: def.Name, t: t}
	t.Cleanup(func() {
		// A fresh context: the test's own context is already cancelled by the time cleanups
		// run, and teardown must still happen or the next test inherits a live port.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if err := client.DeleteImposter(ctx, port); err != nil &&
			!errors.Is(err, rift.ErrImposterNotFound) && !errors.Is(err, rift.ErrClosed) {
			t.Errorf("rifttest: delete imposter %d: %v", port, err)
		}
	})
	return h
}

// Port is the imposter's listening port.
func (h *Handle) Port() uint16 { return h.port }

// BaseURL is the imposter's base URL, e.g. "http://localhost:4545". Point the system under
// test at this.
func (h *Handle) BaseURL() string { return rift.BaseURLFor(h.client, h.protocol, h.port) }

// URL joins path onto the imposter's base URL.
func (h *Handle) URL(path string) string {
	if path != "" && !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return h.BaseURL() + path
}

// Client is the engine this imposter lives on, for operations Handle does not wrap.
func (h *Handle) Client() rift.Client { return h.client }

// Recorded returns the imposter's request journal.
func (h *Handle) Recorded() []rift.RecordedRequest {
	h.t.Helper()
	recs, err := h.client.Recorded(h.t.Context(), h.port)
	if err != nil {
		h.t.Fatalf("rifttest: read journal for imposter %d: %v", h.port, err)
	}
	return recs
}

// ClearRecorded empties the journal, so a later assertion counts only what follows.
func (h *Handle) ClearRecorded() {
	h.t.Helper()
	if err := h.client.ClearRecorded(h.t.Context(), h.port); err != nil {
		h.t.Fatalf("rifttest: clear journal for imposter %d: %v", h.port, err)
	}
}

// AddStub appends a stub to a live imposter.
//
// Note the ordering contract: the engine serves the first stub whose predicates match, so a stub
// appended behind an existing catch-all is unreachable. Use AddStubAt to put one in front.
func (h *Handle) AddStub(src rift.StubSource) {
	h.t.Helper()
	h.AddStubAt(-1, src)
}

// AddStubAt inserts a stub at index. A negative index appends.
func (h *Handle) AddStubAt(index int, src rift.StubSource) {
	h.t.Helper()
	if err := h.client.AddStub(h.t.Context(), h.port, src, index); err != nil {
		h.t.Fatalf("rifttest: add stub to imposter %d: %v", h.port, err)
	}
}

// SetScenarioState forces a scenario on this imposter into a state.
func (h *Handle) SetScenarioState(scenario, state string) {
	h.t.Helper()
	if err := h.client.SetScenarioState(h.t.Context(), h.port, scenario, state, ""); err != nil {
		h.t.Fatalf("rifttest: set scenario %q to %q: %v", scenario, state, err)
	}
}

// AssertReceived fails the test unless the number of recorded requests matching the stub's
// predicates satisfies want.
//
// Matching happens in the engine, through the same predicate evaluator that serves requests, so
// an assertion means exactly what an equivalent stub would have matched — including xpath,
// jsonpath and inject predicates, which a client-side reimplementation could not honour.
//
// On failure it prints what did arrive, because "expected 1, got 0" without the journal sends
// people to add print statements that the journal already contains.
func AssertReceived(t *testing.T, h *Handle, match rift.StubSource, want rift.CountMatcher) {
	t.Helper()

	predicates := match.BuildStub().Predicates
	res, err := h.client.Verify(t.Context(), h.port, rift.VerifyRequest{
		Predicates:     predicates,
		IncludeClosest: true,
	})
	if err != nil {
		t.Fatalf("rifttest: verify against imposter %d: %v", h.port, err)
	}
	if want.Satisfied(res.Matched) {
		return
	}

	t.Errorf("rifttest: imposter %q (port %d): expected %s matching request(s), got %d of %d recorded\n%s",
		h.name, h.port, want, res.Matched, res.Total, renderFailure(predicates, res, h.Recorded()))
}

// AssertNotReceived fails the test if any recorded request matches.
func AssertNotReceived(t *testing.T, h *Handle, match rift.StubSource) {
	t.Helper()
	AssertReceived(t, h, match, rift.Never())
}

// renderFailure builds the diagnostic block: what was asked for, what the engine considered
// closest, and the journal.
func renderFailure(predicates []rift.Predicate, res rift.VerifyResult, journal []rift.RecordedRequest) string {
	var b strings.Builder

	b.WriteString("\n  expected a request matching:\n")
	for _, p := range predicates {
		raw, err := rift.ToJSON(p)
		if err != nil {
			fmt.Fprintf(&b, "    <unprintable predicate: %v>\n", err)
			continue
		}
		fmt.Fprintf(&b, "    %s\n", raw)
	}

	// The engine reports which clauses the nearest request failed and what it actually carried.
	// That pair is the whole diagnostic: it turns "nothing matched" into "this request was close,
	// and the path differed".
	if res.Closest != nil {
		fmt.Fprintf(&b, "\n  closest recorded request:\n    %s\n", summarise(res.Closest.Request))
		if len(res.Closest.FailedPredicates) > 0 {
			b.WriteString("\n  it failed these clauses:\n")
			for _, fp := range res.Closest.FailedPredicates {
				raw, err := rift.ToJSON(fp.Predicate)
				if err != nil {
					continue
				}
				fmt.Fprintf(&b, "    wanted %s\n      actual: %v\n", raw, fp.Actual)
			}
		}
	}

	switch {
	case len(journal) == 0:
		b.WriteString("\n  the journal is empty — the imposter received nothing.\n" +
			"    check the system under test is pointed at Handle.BaseURL().\n")
	default:
		b.WriteString("\n  journal:\n")
		const maxShown = 20
		for i, r := range journal {
			if i == maxShown {
				fmt.Fprintf(&b, "    ... and %d more\n", len(journal)-maxShown)
				break
			}
			fmt.Fprintf(&b, "    %s\n", summarise(r))
		}
	}
	return b.String()
}

// summarise renders one recorded request on a line, truncating a long body.
func summarise(r rift.RecordedRequest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s", r.Method, r.Path)
	if len(r.Query) > 0 {
		fmt.Fprintf(&b, " query=%v", r.Query)
	}
	if len(r.Headers) > 0 {
		fmt.Fprintf(&b, " headers=%v", r.Headers)
	}
	if r.Body != nil {
		body := fmt.Sprintf("%v", r.Body)
		const maxBody = 200
		if len(body) > maxBody {
			body = body[:maxBody] + "…"
		}
		fmt.Fprintf(&b, " body=%s", body)
	}
	return b.String()
}
