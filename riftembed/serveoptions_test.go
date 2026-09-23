package riftembed_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/achird-labs/rift-go/rift"
	"github.com/achird-labs/rift-go/riftembed"
)

func TestBuildInfoIsTyped(t *testing.T) {
	eng := startEngine(t)
	info, err := eng.BuildInfo()
	if err != nil {
		t.Fatalf("BuildInfo: %v", err)
	}
	if info.Version == "" {
		t.Error("Version is empty")
	}
	for _, opt := range []string{"host", "port", "apiKey", "requireAdminAuth"} {
		if !info.SupportsServeOption(opt) {
			t.Errorf("engine %s does not advertise %q (ServeOptions %v)", info.Version, opt, info.ServeOptions)
		}
	}
}

// serve starts the admin plane and returns a client for it.
func serve(t *testing.T, eng *riftembed.Engine, opts riftembed.ServeOptions) (*rift.Remote, map[string]any) {
	t.Helper()
	raw, err := eng.ServeAdmin(t.Context(), opts)
	if err != nil {
		t.Fatalf("ServeAdmin: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("ServeAdmin returned %s: %v", raw, err)
	}
	url, _ := out["adminUrl"].(string)
	c, err := rift.Connect(url, rift.RemoteOptions{})
	if err != nil {
		t.Fatalf("Connect %q: %v", url, err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c, out
}

func freeTCPPort(t *testing.T) uint16 {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return uint16(l.Addr().(*net.TCPAddr).Port) //nolint:gosec // an OS-assigned port fits
}

// assertServes checks that an imposter on port answers path with body.
func assertServes(t *testing.T, port uint16, body string) {
	t.Helper()
	got, status := httpGet(t, fmt.Sprintf("http://127.0.0.1:%d/hello", port))
	if status != 200 || got != body {
		t.Errorf("imposter on %d: %d %q, want 200 %q", port, status, got, body)
	}
}

func TestServeAdminLoadsAConfigFile(t *testing.T) {
	port := freeTCPPort(t)
	path := filepath.Join(t.TempDir(), "imposters.json")
	doc := fmt.Sprintf(`{"imposters":[{"port":%d,"protocol":"http","stubs":[`+
		`{"responses":[{"is":{"statusCode":200,"body":"from-file"}}]}]}]}`, port)
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}

	serve(t, startEngine(t), riftembed.ServeOptions{Host: "127.0.0.1", ConfigFile: path})
	assertServes(t, port, "from-file")
}

func TestServeAdminAppliesInlineConfig(t *testing.T) {
	port := freeTCPPort(t)
	cfg := rift.ImpostersConfig{Imposters: []rift.Imposter{
		rift.NewImposter("inline").Port(port).Stub(rift.OnAny().Return(rift.OKText("inline"))).Build(),
	}}

	serve(t, startEngine(t), riftembed.ServeOptions{Host: "127.0.0.1", Config: &cfg})
	assertServes(t, port, "inline")
}

func TestServeAdminAcceptsTheRemainingOptions(t *testing.T) {
	metrics := freeTCPPort(t)
	_, out := serve(t, startEngine(t), riftembed.ServeOptions{
		Host: "127.0.0.1", MetricsPort: metrics, AllowInjection: true, RequireAdminAuth: true,
	})
	if got, _ := out["metricsPort"].(float64); uint16(got) != metrics {
		t.Errorf("metricsPort = %v, want %d", out["metricsPort"], metrics)
	}
	body, status := httpGet(t, fmt.Sprintf("http://127.0.0.1:%d/metrics", metrics))
	if status != 200 || !strings.Contains(body, "rift") {
		t.Errorf("metrics endpoint: %d, %.80q", status, body)
	}
}

// RequireAdminAuth must actually reach the engine: an off-host plane with no key is refused by
// the engine itself, not by the SDK's version preflight.
func TestRequireAdminAuthRefusesAnOpenOffHostPlane(t *testing.T) {
	_, err := startEngine(t).ServeAdmin(t.Context(), riftembed.ServeOptions{
		Host: "0.0.0.0", RequireAdminAuth: true,
	})
	if err == nil {
		t.Fatal("ServeAdmin bound an off-host admin plane with no API key despite RequireAdminAuth")
	}
	if errors.Is(err, rift.ErrVersionMismatch) {
		t.Fatalf("err = %v: refused by the preflight, so the option never reached the engine", err)
	}
	var ee *rift.EngineError
	if !errors.As(err, &ee) {
		t.Errorf("err = %v, want the engine's own refusal (*rift.EngineError)", err)
	}
}
