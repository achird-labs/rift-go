package conformance_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/achird-labs/rift-go/conformance"
	"github.com/achird-labs/rift-go/rift"
	"github.com/achird-labs/rift-go/riftembed"
)

// The SDK conformance corpus, replayed per the contract in sdk-conformance/README.md:
//
//  1. DSL-expressibility gate — reconstruct the fixture through the typed model and require the
//     serialized output to deep-equal the fixture. A fixture the DSL cannot express means the
//     DSL has drifted from the engine grammar, and is a red build.
//  2. Serve & replay — create the imposter and drive its `_verify` sequence, asserting each step.
//  3. Both transports — embedded and remote must behave identically.
//  4. Capability skips only per the manifest's `requires`.

func loadCorpus(t *testing.T) *conformance.Corpus {
	t.Helper()
	c, err := conformance.Load("")
	if err != nil {
		t.Skipf("conformance corpus unavailable: %v", err)
	}
	t.Logf("corpus: engineVersion=%s fixtures=%d root=%s",
		c.Manifest.EngineVersion, len(c.Manifest.Fixtures), c.Root)
	return c
}

// --- 1. DSL-expressibility gate ---
//
// This runs without an engine: it is a pure statement about the typed model, and keeping it
// engine-free means a DSL regression is caught even on a machine with no native library.
func TestDSLExpressibility(t *testing.T) {
	corpus := loadCorpus(t)

	for _, f := range corpus.Manifest.Fixtures {
		t.Run(fixtureName(f), func(t *testing.T) {
			raw, err := corpus.ReadFixture(f)
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}

			imp, err := rift.ImposterFromJSON(raw)
			if err != nil {
				t.Fatalf("the typed model cannot parse this fixture: %v", err)
			}
			got, err := rift.ToJSON(imp)
			if err != nil {
				t.Fatalf("re-serialize: %v", err)
			}

			wantDoc, gotDoc := decode(t, raw), decode(t, got)
			normalise(wantDoc)
			normalise(gotDoc)

			if !reflect.DeepEqual(gotDoc, wantDoc) {
				t.Errorf("round-trip is not faithful — the DSL has drifted from the engine grammar\n%s",
					firstDifference(wantDoc, gotDoc, ""))
			}
		})
	}
}

// --- 2 & 3. Serve and replay, on every transport ---

func TestServeAndReplay(t *testing.T) {
	corpus := loadCorpus(t)

	for _, lane := range lanes(t) {
		t.Run(lane.name, func(t *testing.T) {
			client, caps, ok := lane.open(t)
			if !ok {
				return // open() already called Skip
			}

			// The corpus is a coherent set, not independent fixtures: 07 proxies to
			// http://localhost:4501, which is 01's manifest port. So every fixture is created
			// up front, on its declared port, and only then are the transcripts replayed.
			// Creating them one at a time on engine-assigned ports would leave 07 pointing at
			// an upstream that no longer exists.
			live := make(map[string]uint16, len(corpus.Manifest.Fixtures))
			for _, f := range corpus.Manifest.Fixtures {
				if missing := caps.Missing(f); len(missing) > 0 {
					t.Logf("skipping %s: lane lacks %s", f.Name, strings.Join(missing, ", "))
					continue
				}
				imp := parseFixture(t, corpus, f)
				port, err := client.CreateImposter(t.Context(), imp)
				if err != nil {
					t.Fatalf("create imposter for %s (port %d): %v", f.Name, f.Port, err)
				}
				if port != f.Port {
					t.Errorf("%s: bound port %d, want the manifest's %d — an explicit port must be respected verbatim",
						f.Name, port, f.Port)
				}
				live[f.File] = port
			}
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.WithoutCancel(context.Background()), 15*time.Second)
				defer cancel()
				_ = client.DeleteAll(ctx)
			})

			for _, f := range corpus.Manifest.Fixtures {
				port, created := live[f.File]
				if !created {
					continue
				}
				t.Run(fixtureName(f), func(t *testing.T) {
					replayFixture(t, corpus, f, port)
				})
			}
		})
	}
}

// lane is one transport under test.
type lane struct {
	name string
	// open returns a client and the capabilities the lane can satisfy, or ok=false after
	// skipping.
	open func(t *testing.T) (rift.Client, conformance.Capabilities, bool)
}

func lanes(t *testing.T) []lane {
	t.Helper()
	return []lane{
		{
			name: "embedded",
			open: func(t *testing.T) (rift.Client, conformance.Capabilities, bool) {
				t.Helper()
				if _, err := riftembed.LibraryPath(); err != nil {
					t.Skipf("no native library: %v", err)
					return nil, nil, false
				}
				eng, err := riftembed.Start(riftembed.Options{})
				if err != nil {
					t.Fatalf("start embedded engine: %v", err)
				}
				t.Cleanup(func() { _ = eng.Close() })
				// The direct C-ABI caller is the trusted in-process embedder, so the engine
				// accepts inject stubs here without --allowInjection.
				return eng, conformance.Capabilities{
					"injection": true,
					"proxy":     true,
					"https":     true,
				}, true
			},
		},
		{
			name: "remote",
			open: func(t *testing.T) (rift.Client, conformance.Capabilities, bool) {
				t.Helper()
				ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
				defer cancel()

				engine, err := rift.Spawn(ctx, rift.SpawnOptions{
					Args: []string{"--allowInjection", "--loglevel", "error"},
				})
				if err != nil {
					t.Skipf("no rift binary for the remote lane: %v", err)
					return nil, nil, false
				}
				t.Cleanup(func() { _ = engine.Close() })
				return engine, conformance.Capabilities{
					"injection": true,
					"proxy":     true,
					"https":     true,
				}, true
			},
		},
	}
}

// parseFixture reads a fixture and reconstructs it through the typed model, which is what makes
// the replay a test of the SDK rather than of the raw JSON.
func parseFixture(t *testing.T, corpus *conformance.Corpus, f conformance.Fixture) rift.Imposter {
	t.Helper()
	raw, err := corpus.ReadFixture(f)
	if err != nil {
		t.Fatalf("read fixture %s: %v", f.File, err)
	}
	imp, err := rift.ImposterFromJSON(raw)
	if err != nil {
		t.Fatalf("parse fixture %s: %v", f.File, err)
	}
	return imp
}

// replayFixture drives a created imposter's `_verify` transcripts.
func replayFixture(t *testing.T, corpus *conformance.Corpus, f conformance.Fixture, port uint16) {
	t.Helper()
	if !f.HasVerify {
		return // serving without error is the whole assertion for these
	}

	imp := parseFixture(t, corpus, f)
	base := rift.BaseURL(protocolOf(imp), port)

	for i, stub := range imp.Stubs {
		v, ok := parseVerify(t, stub)
		if !ok {
			continue
		}
		for j, step := range v.Sequence {
			t.Run(fmt.Sprintf("stub%d/step%d", i, j), func(t *testing.T) {
				runVerifyStep(t, base, step)
			})
		}
	}
}

// parseVerify extracts a stub's `_verify` annotation, if it has one.
func parseVerify(t *testing.T, stub rift.Stub) (conformance.Verify, bool) {
	t.Helper()
	if stub.Verify == nil {
		return conformance.Verify{}, false
	}
	raw, err := json.Marshal(stub.Verify)
	if err != nil {
		t.Fatalf("re-encode _verify: %v", err)
	}
	var v conformance.Verify
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("parse _verify: %v", err)
	}
	return v, len(v.Sequence) > 0
}

// runVerifyStep sends one transcript request and asserts its expectation.
func runVerifyStep(t *testing.T, base string, step conformance.VerifyStep) {
	t.Helper()

	method := step.Request.Method
	if method == "" {
		method = http.MethodGet
	}
	var body io.Reader
	if step.Request.Body != nil {
		body = bytes.NewReader(encodeBody(t, step.Request.Body))
	}

	req, err := http.NewRequestWithContext(t.Context(), method, base+step.Request.Path, body)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	for k, v := range step.Request.Headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, step.Request.Path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	if step.Expect.Status != 0 && resp.StatusCode != step.Expect.Status {
		t.Errorf("%s %s: status = %d, want %d (body %q)",
			method, step.Request.Path, resp.StatusCode, step.Expect.Status, truncate(string(got)))
	}
	if step.Expect.BodyContains != "" && !strings.Contains(string(got), step.Expect.BodyContains) {
		t.Errorf("%s %s: body %q does not contain %q",
			method, step.Request.Path, truncate(string(got)), step.Expect.BodyContains)
	}
}

// --- helpers ---

// encodeBody sends a string body verbatim and marshals anything else, so a fixture asserting on
// a raw string is not silently re-quoted into JSON.
func encodeBody(t *testing.T, body any) []byte {
	t.Helper()
	if s, ok := body.(string); ok {
		return []byte(s)
	}
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("encode request body: %v", err)
	}
	return b
}

func protocolOf(imp rift.Imposter) string {
	if imp.Protocol == "" {
		return "http"
	}
	return imp.Protocol
}

func decode(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return m
}

// normalise applies exactly the equivalences the replay contract permits: an omitted key and an
// explicitly-default one are the same document.
//
// Two cases arise in the corpus. A fixture spelling out "recordRequests": false would otherwise
// fail a round-trip that legitimately drops it; and an explicit null on an optional key —
// "proxy": null in the Mountebank-compat fixture — is the same to the engine as omitting it,
// because both deserialize to None. Neither is the SDK losing information.
func normalise(v any) {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if val == nil || isDefault(k, val) {
				delete(t, k)
				continue
			}
			normalise(val)
		}
	case []any:
		for _, val := range t {
			normalise(val)
		}
	}
}

// isDefault reports whether a key carries its engine default, making it equivalent to absent.
func isDefault(key string, val any) bool {
	switch key {
	case "protocol":
		return val == "http"
	case "recordRequests", "recordMatches", "allowCORS", "mutualAuth", "strictBehaviors":
		return val == false
	default:
		return false
	}
}

// firstDifference walks two decoded documents and reports the first divergence, so a failure
// names the key that drifted rather than dumping two large JSON blobs.
func firstDifference(want, got any, path string) string {
	if reflect.DeepEqual(want, got) {
		return ""
	}
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			return fmt.Sprintf("  at %s: want object, got %T", path, got)
		}
		for k, wv := range w {
			gv, present := g[k]
			if !present {
				return fmt.Sprintf("  at %s.%s: missing from the round-trip (want %v)", path, k, compact(wv))
			}
			if d := firstDifference(wv, gv, path+"."+k); d != "" {
				return d
			}
		}
		for k, gv := range g {
			if _, present := w[k]; !present {
				return fmt.Sprintf("  at %s.%s: added by the round-trip (got %v)", path, k, compact(gv))
			}
		}
	case []any:
		g, ok := got.([]any)
		if !ok {
			return fmt.Sprintf("  at %s: want array, got %T", path, got)
		}
		if len(w) != len(g) {
			return fmt.Sprintf("  at %s: want %d elements, got %d", path, len(w), len(g))
		}
		for i := range w {
			if d := firstDifference(w[i], g[i], fmt.Sprintf("%s[%d]", path, i)); d != "" {
				return d
			}
		}
	default:
		return fmt.Sprintf("  at %s: want %v, got %v", path, compact(want), compact(got))
	}
	return fmt.Sprintf("  at %s: documents differ", path)
}

func compact(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return truncate(string(b))
}

func truncate(s string) string {
	const max = 200
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

func fixtureName(f conformance.Fixture) string {
	name := f.Name
	if name == "" {
		name = f.File
	}
	// Subtest names split on slashes and spaces become underscores; make that predictable.
	return strings.NewReplacer("/", "_", " ", "_", "·", "-").Replace(name)
}
