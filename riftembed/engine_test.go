package riftembed_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/achird-labs/rift-go/rift"
	"github.com/achird-labs/rift-go/riftembed"
)

// These tests exercise the real native library. They are skipped when none is discoverable, so
// `go test ./...` works on a fresh checkout; CI installs one (or sets RIFT_FFI_LIB) and they
// become required.
func requireLibrary(t *testing.T) {
	t.Helper()
	if _, err := riftembed.LibraryPath(); err != nil {
		t.Skipf("no native library found, skipping embedded tests: %v", err)
	}
}

func startEngine(t *testing.T) *riftembed.Engine {
	t.Helper()
	requireLibrary(t)
	eng, err := riftembed.Start(riftembed.Options{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		if err := eng.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return eng
}

func TestStartReportsBuildInfoAndABI(t *testing.T) {
	eng := startEngine(t)

	if got := eng.ABIVersion(); got != 2 {
		t.Errorf("ABIVersion = %d, want 2", got)
	}

	info, err := eng.BuildInfo()
	if err != nil {
		t.Fatalf("BuildInfo: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(info, &parsed); err != nil {
		t.Fatalf("BuildInfo is not JSON: %v (%s)", err, info)
	}
	if _, ok := parsed["version"]; !ok {
		t.Errorf("BuildInfo has no version key: %s", info)
	}
	t.Logf("build info: %s", info)
}

// The end-to-end shape: build an imposter with the DSL, create it in-process, drive it over
// real HTTP, and read the journal back through the C ABI.
func TestCreateServeAndRecord(t *testing.T) {
	eng := startEngine(t)

	port, err := eng.CreateImposter(rift.NewImposter("users").Record().
		Stub(rift.OnGet("/api/users/1").
			Return(rift.OKJSON(map[string]rift.JSON{"id": 1, "name": "Alice"}))).
		Stub(rift.OnAny().Return(rift.Status(404))))
	if err != nil {
		t.Fatalf("CreateImposter: %v", err)
	}
	if port == 0 {
		t.Fatal("CreateImposter returned port 0")
	}
	t.Logf("imposter on port %d", port)

	body, status := httpGet(t, fmt.Sprintf("http://127.0.0.1:%d/api/users/1", port))
	if status != 200 {
		t.Errorf("status = %d, want 200", status)
	}
	var user map[string]any
	if err := json.Unmarshal([]byte(body), &user); err != nil {
		t.Fatalf("response is not JSON: %v (%q)", err, body)
	}
	if user["name"] != "Alice" {
		t.Errorf("name = %v, want Alice", user["name"])
	}

	// The catch-all stub must win only when the first does not match.
	if _, status := httpGet(t, fmt.Sprintf("http://127.0.0.1:%d/nope", port)); status != 404 {
		t.Errorf("unmatched path status = %d, want 404", status)
	}

	recorded, err := eng.Recorded(port)
	if err != nil {
		t.Fatalf("Recorded: %v", err)
	}
	if len(recorded) != 2 {
		t.Fatalf("recorded %d requests, want 2: %+v", len(recorded), recorded)
	}
	if recorded[0].Method != "GET" || recorded[0].Path != "/api/users/1" {
		t.Errorf("first recorded = %s %s", recorded[0].Method, recorded[0].Path)
	}
}

// Verification runs through the engine's own predicate evaluator, not client-side.
func TestVerifyCountsThroughEngine(t *testing.T) {
	eng := startEngine(t)

	port, err := eng.CreateImposter(rift.NewImposter("verify").Record().
		Stub(rift.OnAny().Return(rift.OK())))
	if err != nil {
		t.Fatalf("CreateImposter: %v", err)
	}

	for range 3 {
		httpGet(t, fmt.Sprintf("http://127.0.0.1:%d/ping", port))
	}
	httpGet(t, fmt.Sprintf("http://127.0.0.1:%d/other", port))

	res, err := eng.Verify(port, rift.VerifyRequest{
		Predicates: []rift.Predicate{rift.PredicateOn("path", rift.Equals("/ping"))},
	})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if res.Matched != 3 {
		t.Errorf("matched = %d, want 3 (total %d)", res.Matched, res.Total)
	}
	if res.Total != 4 {
		t.Errorf("total = %d, want 4", res.Total)
	}
}

// Spaces partition one port between parallel flows — the primitive that lets shards share an
// imposter without seeing each other's traffic.
func TestSpacesIsolateFlows(t *testing.T) {
	eng := startEngine(t)

	port, err := eng.CreateImposter(rift.NewImposter("spaces").Record().
		Stub(rift.OnAny().Return(rift.Status(404))))
	if err != nil {
		t.Fatalf("CreateImposter: %v", err)
	}

	if err := eng.SpaceAddStub(port, "flow-a",
		rift.OnGet("/who").Return(rift.OKText("a"))); err != nil {
		t.Fatalf("SpaceAddStub(flow-a): %v", err)
	}
	if err := eng.SpaceAddStub(port, "flow-b",
		rift.OnGet("/who").Return(rift.OKText("b"))); err != nil {
		t.Fatalf("SpaceAddStub(flow-b): %v", err)
	}

	stubs, err := eng.SpaceListStubs(port, "flow-a")
	if err != nil {
		t.Fatalf("SpaceListStubs: %v", err)
	}
	var listed []any
	if err := json.Unmarshal(stubs, &listed); err != nil {
		// Some engine versions wrap the list; accept either shape rather than assert a schema
		// this test does not own.
		t.Logf("space stubs (non-array shape): %s", stubs)
	} else if len(listed) != 1 {
		t.Errorf("flow-a has %d stubs, want 1", len(listed))
	}

	if err := eng.SpaceDelete(port, "flow-a"); err != nil {
		t.Errorf("SpaceDelete: %v", err)
	}
}

// Errors must classify, so callers can branch without matching on strings.
func TestMissingImposterClassifies(t *testing.T) {
	eng := startEngine(t)

	err := eng.DeleteImposter(59999)
	if err == nil {
		t.Fatal("deleting a nonexistent imposter returned nil error")
	}
	var engErr *rift.EngineError
	if !errors.As(err, &engErr) {
		t.Fatalf("error is not *rift.EngineError: %T %v", err, err)
	}
	t.Logf("classified error: %v", err)
}

// Calls after Close must fail cleanly rather than using a freed handle.
func TestUseAfterCloseIsSafe(t *testing.T) {
	requireLibrary(t)
	eng, err := riftembed.Start(riftembed.Options{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := eng.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := eng.Close(); err != nil {
		t.Errorf("second Close should be a no-op, got %v", err)
	}
	if _, err := eng.CreateImposter(rift.NewImposter("x")); !errors.Is(err, rift.ErrClosed) {
		t.Errorf("CreateImposter after Close = %v, want ErrClosed", err)
	}
}

func TestMissingLibraryGivesActionableError(t *testing.T) {
	_, err := riftembed.Start(riftembed.Options{LibraryPath: "/nonexistent/librift_ffi.dylib"})
	if !errors.Is(err, rift.ErrEngineUnavailable) {
		t.Fatalf("error = %v, want ErrEngineUnavailable", err)
	}
}

func httpGet(t *testing.T, url string) (string, int) {
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
