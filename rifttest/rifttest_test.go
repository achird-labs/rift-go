package rifttest_test

import (
	"io"
	"net/http"
	"testing"

	"github.com/achird-labs/rift-go/rift"
	"github.com/achird-labs/rift-go/rifttest"
	"github.com/achird-labs/rift-go/riftembed"
)

// skipWithoutEngine keeps the suite green on a machine with no native library while still
// making these required in CI, which installs one.
func skipWithoutEngine(t *testing.T) {
	t.Helper()
	if _, err := riftembed.LibraryPath(); err != nil {
		t.Skipf("no native library found: %v", err)
	}
}

// The README's example, end to end.
func TestImposterServesAndAsserts(t *testing.T) {
	skipWithoutEngine(t)

	users := rifttest.Imposter(t, rift.NewImposter("users").
		Stub(rift.OnGet("/api/users/1").
			Return(rift.OKJSON(map[string]rift.JSON{"id": 1, "name": "Alice"}))))

	body, status := get(t, users.URL("/api/users/1"))
	if status != 200 {
		t.Fatalf("status = %d, want 200", status)
	}
	if body == "" {
		t.Error("empty body")
	}

	rifttest.AssertReceived(t, users, rift.OnGet("/api/users/1"), rift.Once())
	rifttest.AssertNotReceived(t, users, rift.OnPost("/api/users"))
}

// Recording is enabled automatically: an imposter you cannot assert against is rarely what a
// test wants, and the failure mode otherwise is a confusing empty journal.
func TestRecordingIsOnByDefault(t *testing.T) {
	skipWithoutEngine(t)

	h := rifttest.Imposter(t, rift.NewImposter("implicit-record").
		Stub(rift.OnAny().Return(rift.OK())))
	get(t, h.URL("/anything"))

	if recs := h.Recorded(); len(recs) != 1 {
		t.Errorf("recorded %d requests, want 1 — recording should be on by default", len(recs))
	}
}

func TestCountMatchers(t *testing.T) {
	skipWithoutEngine(t)

	h := rifttest.Imposter(t, rift.NewImposter("counts").
		Stub(rift.OnAny().Return(rift.OK())))
	for range 3 {
		get(t, h.URL("/ping"))
	}

	rifttest.AssertReceived(t, h, rift.OnGet("/ping"), rift.Times(3))
	rifttest.AssertReceived(t, h, rift.OnGet("/ping"), rift.AtLeast(2))
	rifttest.AssertReceived(t, h, rift.OnGet("/ping"), rift.AtMost(5))
	rifttest.AssertReceived(t, h, rift.OnGet("/ping"), rift.Between(1, 3))
	rifttest.AssertReceived(t, h, rift.OnGet("/absent"), rift.Never())
}

func TestClearRecordedResetsTheJournal(t *testing.T) {
	skipWithoutEngine(t)

	h := rifttest.Imposter(t, rift.NewImposter("clearable").
		Stub(rift.OnAny().Return(rift.OK())))
	get(t, h.URL("/before"))
	h.ClearRecorded()
	get(t, h.URL("/after"))

	rifttest.AssertReceived(t, h, rift.OnGet("/after"), rift.Once())
	rifttest.AssertNotReceived(t, h, rift.OnGet("/before"))
}

// Stub order decides the winner — the engine serves the first stub whose predicates match — so
// a stub added behind an existing catch-all is dead code. AddStubAt(0) is how you put a new
// stub in front of one.
func TestAddStubToLiveImposter(t *testing.T) {
	skipWithoutEngine(t)

	h := rifttest.Imposter(t, rift.NewImposter("growable").
		Stub(rift.OnAny().Return(rift.Status(404))))
	if _, status := get(t, h.URL("/later")); status != 404 {
		t.Fatalf("status before adding the stub = %d, want 404", status)
	}

	// Appending would land behind the catch-all and never match.
	h.AddStubAt(0, rift.OnGet("/later").Return(rift.OKText("now here")))

	body, status := get(t, h.URL("/later"))
	if status != 200 || body != "now here" {
		t.Errorf("after inserting stub at 0: status %d body %q", status, body)
	}

	// An appended stub behind a catch-all is unreachable — assert the trap, so the ordering
	// contract is pinned rather than folklore.
	h.AddStub(rift.OnGet("/appended").Return(rift.OKText("unreachable")))
	if _, status := get(t, h.URL("/appended")); status != 404 {
		t.Errorf("appended stub behind a catch-all should be unreachable, got status %d", status)
	}
}

// Two imposters in one test binary must not see each other's traffic — the isolation the
// shared-engine design depends on.
func TestImpostersAreIsolated(t *testing.T) {
	skipWithoutEngine(t)

	a := rifttest.Imposter(t, rift.NewImposter("a").Stub(rift.OnAny().Return(rift.OKText("a"))))
	b := rifttest.Imposter(t, rift.NewImposter("b").Stub(rift.OnAny().Return(rift.OKText("b"))))

	if a.Port() == b.Port() {
		t.Fatalf("both imposters bound port %d", a.Port())
	}
	get(t, a.URL("/only-a"))

	rifttest.AssertReceived(t, a, rift.OnGet("/only-a"), rift.Once())
	rifttest.AssertNotReceived(t, b, rift.OnGet("/only-a"))
}

// A failing assertion is only useful if it can explain itself, which means the engine must
// return both a closest-match and a readable journal. Rather than provoke a failure — Go's
// testing.T cannot be intercepted, so that would just fail this test — assert that the data the
// diagnostic renders is actually available.
func TestDiagnosticDataIsAvailableOnAMiss(t *testing.T) {
	skipWithoutEngine(t)

	h := rifttest.Imposter(t, rift.NewImposter("diagnostics").
		Stub(rift.OnAny().Return(rift.OK())))
	get(t, h.URL("/actually-called"))

	res, err := h.Client().Verify(t.Context(), h.Port(), rift.VerifyRequest{
		Predicates:     rift.OnGet("/never-called").Build().Predicates,
		IncludeClosest: true,
	})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if res.Matched != 0 {
		t.Errorf("matched = %d, want 0", res.Matched)
	}
	if res.Total != 1 {
		t.Errorf("total = %d, want 1 — the journal should still hold the request", res.Total)
	}

	recs := h.Recorded()
	if len(recs) != 1 || recs[0].Path != "/actually-called" {
		t.Errorf("journal = %+v, want one request for /actually-called", recs)
	}
}

func get(t *testing.T, url string) (string, int) {
	t.Helper()
	resp, err := http.Get(url) //nolint:noctx // short-lived test request
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b), resp.StatusCode
}
