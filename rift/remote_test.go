package rift_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/achird-labs/rift-go/rift"
)

// These exercise the admin client against a stub server, so they run everywhere and pin the
// wire contract — paths, methods, and bodies — without needing an engine. The
// engine-for-real coverage lives in the integration tests.

func newFakeEngine(t *testing.T, h http.HandlerFunc) *rift.Remote {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := rift.Connect(srv.URL, rift.RemoteOptions{})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestConnectRejectsBadURLs(t *testing.T) {
	for _, u := range []string{"", "not-a-url", "/imposters"} {
		if _, err := rift.Connect(u, rift.RemoteOptions{}); !errors.Is(err, rift.ErrInvalidDefinition) {
			t.Errorf("Connect(%q) = %v, want ErrInvalidDefinition", u, err)
		}
	}
}

func TestCreateImposterPostsToImpostersAndReadsPort(t *testing.T) {
	var gotMethod, gotPath, gotBody string
	c := newFakeEngine(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		b := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(b)
		gotBody = string(b)
		_, _ = w.Write([]byte(`{"port":4545,"protocol":"http"}`))
	})

	port, err := c.CreateImposter(t.Context(), rift.NewImposter("users").
		Stub(rift.OnGet("/x").Return(rift.OKText("hi"))))
	if err != nil {
		t.Fatalf("CreateImposter: %v", err)
	}
	if port != 4545 {
		t.Errorf("port = %d, want 4545", port)
	}
	if gotMethod != http.MethodPost || gotPath != "/imposters" {
		t.Errorf("request = %s %s, want POST /imposters", gotMethod, gotPath)
	}
	var sent map[string]any
	if err := json.Unmarshal([]byte(gotBody), &sent); err != nil {
		t.Fatalf("body is not JSON: %v (%q)", err, gotBody)
	}
	if sent["name"] != "users" {
		t.Errorf("body name = %v", sent["name"])
	}
}

// An engine that echoes no port would otherwise hand back an unusable zero.
func TestCreateImposterFallsBackToRequestedPort(t *testing.T) {
	c := newFakeEngine(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	})
	port, err := c.CreateImposter(t.Context(), rift.NewImposter("pinned").Port(4501))
	if err != nil {
		t.Fatalf("CreateImposter: %v", err)
	}
	if port != 4501 {
		t.Errorf("port = %d, want the requested 4501", port)
	}
}

func TestErrorStatusBecomesEngineErrorWithMessage(t *testing.T) {
	c := newFakeEngine(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"errors":[{"code":"bad data","message":"port 4545 is already in use"}]}`))
	})

	_, err := c.CreateImposter(t.Context(), rift.NewImposter("x"))
	if err == nil {
		t.Fatal("want an error")
	}
	var engErr *rift.EngineError
	if !errors.As(err, &engErr) {
		t.Fatalf("error is not *EngineError: %T", err)
	}
	if engErr.Code != 400 {
		t.Errorf("code = %d, want 400", engErr.Code)
	}
	if engErr.Message != "port 4545 is already in use" {
		t.Errorf("message = %q", engErr.Message)
	}
	if !errors.Is(err, rift.ErrInvalidDefinition) {
		t.Errorf("400 should classify as ErrInvalidDefinition")
	}
}

func TestNotFoundClassifiesAsImposterNotFound(t *testing.T) {
	c := newFakeEngine(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	err := c.DeleteImposter(t.Context(), 4545)
	if !errors.Is(err, rift.ErrImposterNotFound) {
		t.Errorf("err = %v, want ErrImposterNotFound", err)
	}
}

// An unreachable engine must be distinguishable from one that answered with a rejection.
func TestUnreachableEngineClassifiesAsUnavailable(t *testing.T) {
	c, err := rift.Connect("http://127.0.0.1:1", rift.RemoteOptions{})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	if err := c.Ping(t.Context()); !errors.Is(err, rift.ErrEngineUnavailable) {
		t.Errorf("err = %v, want ErrEngineUnavailable", err)
	}
}

func TestAdminRoutePaths(t *testing.T) {
	var got []string
	c := newFakeEngine(t, func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Method+" "+r.URL.Path)
		switch r.URL.Path {
		case "/imposters/4545/savedRequests":
			_, _ = w.Write([]byte(`[{"method":"GET","path":"/x"}]`))
		case "/imposters/4545/verify":
			_, _ = w.Write([]byte(`{"matched":1,"total":2}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	})
	ctx := t.Context()

	_ = c.DeleteAll(ctx)
	_ = c.ClearRecorded(ctx, 4545)
	_ = c.ReplaceStubs(ctx, 4545, []rift.Stub{rift.OnGet("/x").Build()})
	_ = c.AddStub(ctx, 4545, rift.OnGet("/y"), -1)
	_ = c.SetScenarioState(ctx, 4545, "retry", "done", "")
	_ = c.ResetScenarios(ctx, 4545, "")
	_ = c.SpaceAddStub(ctx, 4545, "flow-1", rift.OnGet("/z"))
	_ = c.SpaceDelete(ctx, 4545, "flow-1")
	_ = c.SetImposterEnabled(ctx, 4545, false)

	want := []string{
		"DELETE /imposters",
		"DELETE /imposters/4545/savedRequests",
		"PUT /imposters/4545/stubs",
		"POST /imposters/4545/stubs",
		"PUT /imposters/4545/scenarios/retry/state",
		"POST /imposters/4545/scenarios/reset",
		"POST /imposters/4545/spaces/flow-1/stubs",
		"DELETE /imposters/4545/spaces/flow-1",
		"POST /imposters/4545/disable",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d requests, want %d:\n%v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("request %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// The journal comes back as a bare array from the admin route and wrapped from the C ABI;
// both must decode.
func TestRecordedAcceptsBareAndWrappedShapes(t *testing.T) {
	for name, payload := range map[string]string{
		"bare":    `[{"method":"GET","path":"/x"}]`,
		"wrapped": `{"requests":[{"method":"GET","path":"/x"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			c := newFakeEngine(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(payload))
			})
			recs, err := c.Recorded(t.Context(), 4545)
			if err != nil {
				t.Fatalf("Recorded: %v", err)
			}
			if len(recs) != 1 || recs[0].Path != "/x" {
				t.Errorf("recs = %+v", recs)
			}
		})
	}
}

func TestAPIKeyHeaderIsSent(t *testing.T) {
	var key string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key = r.Header.Get("x-api-key")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	c, err := rift.Connect(srv.URL, rift.RemoteOptions{APIKey: "s3cret"})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	if err := c.Ping(t.Context()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if key != "s3cret" {
		t.Errorf("x-api-key = %q", key)
	}
}

func TestBaseURLForUsesRemoteHost(t *testing.T) {
	c, err := rift.Connect("http://engine.internal:2525", rift.RemoteOptions{})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	if got := rift.BaseURLFor(c, "http", 4545); got != "http://engine.internal:4545" {
		t.Errorf("BaseURLFor = %q", got)
	}
}

func TestSpawnWithoutBinaryIsActionable(t *testing.T) {
	_, err := rift.Spawn(t.Context(), rift.SpawnOptions{Binary: "/nonexistent/rift"})
	if !errors.Is(err, rift.ErrEngineUnavailable) {
		t.Errorf("err = %v, want ErrEngineUnavailable", err)
	}
}
