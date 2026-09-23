package riftembed_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/achird-labs/rift-go/rift"
	"github.com/achird-labs/rift-go/riftembed"
)

// The engine's start options are deny_unknown_fields, so the key spelling is the contract.
func TestInterceptOptionsUseTheEngineKeys(t *testing.T) {
	opts := riftembed.InterceptOptions{
		Port: 1, Host: "127.0.0.1",
		CACertPEM: "c", CAKeyPEM: "k",
		CACertPath: "cp", CAKeyPath: "kp",
		ReturnCAKey: true,
		Auth:        &riftembed.InterceptAuth{Username: "u", Password: "p"},
		Rules:       []rift.InterceptRule{riftembed.InterceptForward("a.example", 9)},
	}
	raw, err := json.Marshal(opts)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	want := []string{"port", "host", "caCertPem", "caKeyPem", "caCertPath", "caKeyPath",
		"returnCaKey", "auth", "rules"}
	for _, k := range want {
		if _, ok := got[k]; !ok {
			t.Errorf("key %q missing from %s", k, raw)
		}
	}
	if len(got) != len(want) {
		t.Errorf("got %d keys, want %d: %s", len(got), len(want), raw)
	}
	if a, _ := got["auth"].(map[string]any); a["username"] != "u" || a["password"] != "p" {
		t.Errorf("auth = %v, want {username:u password:p}", got["auth"])
	}

	empty, err := json.Marshal(riftembed.InterceptOptions{})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(empty) != "{}" {
		t.Errorf("zero options = %s, want {} (so an older engine without `rules` still starts)", empty)
	}
}

func TestInvalidInterceptOptionsAreRefusedBeforeTheEngine(t *testing.T) {
	eng := startEngine(t)
	if err := eng.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	cases := map[string]riftembed.InterceptOptions{
		"cert pem without key":      {CACertPEM: "c"},
		"key pem without cert":      {CAKeyPEM: "k"},
		"cert path without key":     {CACertPath: "c"},
		"key path without cert":     {CAKeyPath: "k"},
		"both pairs":                {CACertPEM: "c", CAKeyPEM: "k", CACertPath: "cp", CAKeyPath: "kp"},
		"return key with pem pair":  {ReturnCAKey: true, CACertPEM: "c", CAKeyPEM: "k"},
		"return key with path pair": {ReturnCAKey: true, CACertPath: "c", CAKeyPath: "k"},
		"blank auth username":       {Auth: &riftembed.InterceptAuth{Username: " ", Password: "p"}},
		"blank auth password":       {Auth: &riftembed.InterceptAuth{Username: "u", Password: ""}},
	}
	for name, opts := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := eng.StartIntercept(t.Context(), opts)
			if !errors.Is(err, rift.ErrInvalidDefinition) {
				t.Errorf("err = %v, want ErrInvalidDefinition", err)
			}
			if errors.Is(err, rift.ErrClosed) {
				t.Errorf("err = %v: the engine was reached before the options were checked", err)
			}
		})
	}
}

// A minted CA handed back once, then supplied again inline and from files, must be the CA the
// listener presents — which is the whole point of sharing an anchor across engines.
func TestInterceptCAMaterialRoundTrips(t *testing.T) {
	ctx := t.Context()

	minter := startEngine(t)
	ic, err := minter.StartIntercept(ctx, riftembed.InterceptOptions{ReturnCAKey: true})
	if err != nil {
		t.Fatalf("StartIntercept(ReturnCAKey): %v", err)
	}
	t.Cleanup(func() { _ = ic.Stop(ctx) })
	mat, ok := ic.CAMaterial()
	if !ok || !strings.Contains(mat.CertPEM, "BEGIN CERTIFICATE") || !strings.Contains(mat.KeyPEM, "PRIVATE KEY") {
		t.Fatalf("CAMaterial = (%d-byte cert, %v), want a certificate and a private key", len(mat.CertPEM), ok)
	}
	for _, format := range []string{"%v", "%+v", "%#v", "%s"} {
		for name, v := range map[string]any{"intercept": ic, "material": mat} {
			if s := fmt.Sprintf(format, v); strings.Contains(s, "PRIVATE KEY") {
				t.Errorf("%s formatted with %s leaks the CA private key", name, format)
			}
		}
	}

	t.Run("inline", func(t *testing.T) {
		eng := startEngine(t)
		ic2, err := eng.StartIntercept(ctx, riftembed.InterceptOptions{CACertPEM: mat.CertPEM, CAKeyPEM: mat.KeyPEM})
		if err != nil {
			t.Fatalf("StartIntercept(PEM pair): %v", err)
		}
		t.Cleanup(func() { _ = ic2.Stop(ctx) })
		assertCA(t, ic2, mat.CertPEM)
		if _, ok := ic2.CAMaterial(); ok {
			t.Error("CAMaterial reported material for a caller-supplied CA; it is only returned when minted")
		}
	})

	t.Run("files", func(t *testing.T) {
		dir := t.TempDir()
		certPath, keyPath := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "ca.key")
		if err := os.WriteFile(certPath, []byte(mat.CertPEM), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(keyPath, []byte(mat.KeyPEM), 0o600); err != nil {
			t.Fatal(err)
		}
		eng := startEngine(t)
		ic3, err := eng.StartIntercept(ctx, riftembed.InterceptOptions{CACertPath: certPath, CAKeyPath: keyPath})
		if err != nil {
			t.Fatalf("StartIntercept(path pair): %v", err)
		}
		t.Cleanup(func() { _ = ic3.Stop(ctx) })
		assertCA(t, ic3, mat.CertPEM)
	})
}

func assertCA(t *testing.T, ic *riftembed.Intercept, wantPEM string) {
	t.Helper()
	got, err := ic.CACertPEM(t.Context())
	if err != nil {
		t.Fatalf("CACertPEM: %v", err)
	}
	if strings.TrimSpace(string(got)) != strings.TrimSpace(wantPEM) {
		t.Errorf("listener CA differs from the supplied one")
	}
}

func TestInterceptRulesInstalledAtStart(t *testing.T) {
	eng := startEngine(t)
	ctx := t.Context()

	ic, err := eng.StartIntercept(ctx, riftembed.InterceptOptions{
		Rules: []rift.InterceptRule{riftembed.InterceptServe("flags.example.com", rift.OKText("at-start"))},
	})
	if err != nil {
		t.Fatalf("StartIntercept: %v", err)
	}
	t.Cleanup(func() { _ = ic.Stop(ctx) })

	rules, err := ic.ListRules(ctx)
	if err != nil {
		t.Fatalf("ListRules: %v", err)
	}
	if len(rules) != 1 || rules[0].Host != "flags.example.com" {
		t.Fatalf("rules = %+v, want the one installed at start", rules)
	}
	if body := interceptGet(t, ic, "https://flags.example.com/x"); body != "at-start" {
		t.Errorf("body = %q, want %q", body, "at-start")
	}
}

func TestInterceptProxyAuth(t *testing.T) {
	eng := startEngine(t)
	ctx := t.Context()

	ic, err := eng.StartIntercept(ctx, riftembed.InterceptOptions{
		Auth:  &riftembed.InterceptAuth{Username: "tester", Password: "s3cret"},
		Rules: []rift.InterceptRule{riftembed.InterceptServe("flags.example.com", rift.OKText("authed"))},
	})
	if err != nil {
		t.Fatalf("StartIntercept: %v", err)
	}
	t.Cleanup(func() { _ = ic.Stop(ctx) })

	for _, format := range []string{"%v", "%+v", "%#v"} {
		if s := fmt.Sprintf(format, ic); strings.Contains(s, "s3cret") {
			t.Errorf("Intercept formatted with %s leaks the proxy password: %s", format, s)
		}
	}

	// HTTPClient carries the credentials, so it keeps working unchanged.
	if body := interceptGet(t, ic, "https://flags.example.com/x"); body != "authed" {
		t.Errorf("body = %q, want %q", body, "authed")
	}

	// Without them the listener refuses the CONNECT.
	tlsCfg, err := ic.TLSConfig(ctx)
	if err != nil {
		t.Fatalf("TLSConfig: %v", err)
	}
	bare := *ic.ProxyURL()
	bare.User = nil
	anon := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(&bare), TLSClientConfig: tlsCfg}}
	resp, err := anon.Get("https://flags.example.com/x") //nolint:noctx // short test request
	if err == nil {
		_ = resp.Body.Close()
		t.Errorf("unauthenticated request through an authenticated proxy got %d, want refusal", resp.StatusCode)
	}
}

func interceptGet(t *testing.T, ic *riftembed.Intercept, url string) string {
	t.Helper()
	client, err := ic.HTTPClient(t.Context())
	if err != nil {
		t.Fatalf("HTTPClient: %v", err)
	}
	resp, err := client.Get(url) //nolint:noctx // short test request
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(body)
}
