package riftembed_test

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"io"
	"testing"

	"github.com/achird-labs/rift-go/rift"
	"github.com/achird-labs/rift-go/riftembed"
)

// The wedge this exists for: mock an HTTPS dependency whose host the system under test hard-codes
// and cannot be repointed. The SUT here is an http.Client the SDK hands back pre-configured.
func TestInterceptForwardsHardcodedHTTPSHostToAnImposter(t *testing.T) {
	eng := startEngine(t)
	ctx := t.Context()

	// The mock the intercepted traffic should land on.
	port, err := eng.CreateImposter(ctx, rift.NewImposter("cdn").Record().
		Stub(rift.OnGet("/config.json").
			Return(rift.OKJSON(map[string]rift.JSON{"flag": true}))))
	if err != nil {
		t.Fatalf("CreateImposter: %v", err)
	}

	ic, err := eng.StartIntercept(ctx, riftembed.InterceptOptions{})
	if err != nil {
		t.Fatalf("StartIntercept: %v", err)
	}
	t.Cleanup(func() { _ = ic.Stop(ctx) })
	t.Logf("intercept proxy on %s", ic.ProxyURL())

	if err := ic.AddRules(ctx, riftembed.InterceptForward("cdn.example.com", port)); err != nil {
		t.Fatalf("AddRules: %v", err)
	}

	client, err := ic.HTTPClient(ctx)
	if err != nil {
		t.Fatalf("HTTPClient: %v", err)
	}

	// A real HTTPS request to a host that does not exist, answered by the imposter.
	resp, err := client.Get("https://cdn.example.com/config.json") //nolint:noctx // short test request
	if err != nil {
		t.Fatalf("GET through the intercept proxy: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200 (body %q)", resp.StatusCode, body)
	}
	if string(body) == "" {
		t.Error("empty body")
	}
	t.Logf("intercepted response: %s", body)

	// The imposter must have seen it, which proves the traffic really was re-proxied rather
	// than answered by the listener.
	recs, err := eng.Recorded(ctx, port)
	if err != nil {
		t.Fatalf("Recorded: %v", err)
	}
	if len(recs) != 1 || recs[0].Path != "/config.json" {
		t.Errorf("imposter journal = %+v, want one request for /config.json", recs)
	}
}

func TestInterceptServesCannedResponses(t *testing.T) {
	eng := startEngine(t)
	ctx := t.Context()

	ic, err := eng.StartIntercept(ctx, riftembed.InterceptOptions{})
	if err != nil {
		t.Fatalf("StartIntercept: %v", err)
	}
	t.Cleanup(func() { _ = ic.Stop(ctx) })

	if err := ic.AddRules(ctx,
		riftembed.InterceptServe("flags.example.com", rift.OKText("served-inline"))); err != nil {
		t.Fatalf("AddRules: %v", err)
	}

	rules, err := ic.ListRules(ctx)
	if err != nil {
		t.Fatalf("ListRules: %v", err)
	}
	if len(rules) != 1 || rules[0].Host != "flags.example.com" {
		t.Fatalf("rules = %+v", rules)
	}

	client, err := ic.HTTPClient(ctx)
	if err != nil {
		t.Fatalf("HTTPClient: %v", err)
	}
	resp, err := client.Get("https://flags.example.com/anything") //nolint:noctx // short test request
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "served-inline" {
		t.Errorf("body = %q, want %q", body, "served-inline")
	}

	if err := ic.ClearRules(ctx); err != nil {
		t.Errorf("ClearRules: %v", err)
	}
	if rules, err := ic.ListRules(ctx); err != nil {
		t.Errorf("ListRules after clear: %v", err)
	} else if len(rules) != 0 {
		t.Errorf("rules after clear = %+v, want none", rules)
	}
}

// TLSConfig must produce a pool that trusts the intercept CA.
//
// It deliberately does not assert that the system roots survived: x509.CertPool.Subjects is
// deprecated precisely because on macOS it does not report the lazily-consulted system store, so
// any count-based check would be testing the platform rather than this code. The CA being
// trusted is the property that matters and is portable to assert.
func TestTLSConfigTrustsTheInterceptCA(t *testing.T) {
	eng := startEngine(t)
	ctx := t.Context()

	ic, err := eng.StartIntercept(ctx, riftembed.InterceptOptions{})
	if err != nil {
		t.Fatalf("StartIntercept: %v", err)
	}
	t.Cleanup(func() { _ = ic.Stop(ctx) })

	cfg, err := ic.TLSConfig(ctx)
	if err != nil {
		t.Fatalf("TLSConfig: %v", err)
	}
	if cfg.RootCAs == nil {
		t.Fatal("RootCAs is nil")
	}
	if cfg.MinVersion < tls.VersionTLS12 {
		t.Errorf("MinVersion = %x, want at least TLS 1.2", cfg.MinVersion)
	}

	pemBytes, err := ic.CACertPEM(ctx)
	if err != nil {
		t.Fatalf("CACertPEM: %v", err)
	}
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		t.Fatalf("CA PEM did not decode: %q", pemBytes)
	}
	ca, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse CA: %v", err)
	}
	// A CA is self-signed, so it verifies against a pool that contains it — and fails against
	// one that does not.
	if _, err := ca.Verify(x509.VerifyOptions{Roots: cfg.RootCAs}); err != nil {
		t.Errorf("the intercept CA does not verify against the returned pool: %v", err)
	}
}
