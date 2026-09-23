package riftembed

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/achird-labs/rift-go/rift"
)

func TestParseBuildInfo(t *testing.T) {
	info, err := parseBuildInfo(`{"version":"0.18.0","commit":null,"builtAt":"2026-09-22T22:00:00Z",` +
		`"features":["redis-backend","javascript"],"serveOptions":["host","port","noParse"]}`)
	if err != nil {
		t.Fatalf("parseBuildInfo: %v", err)
	}
	if info.Version != "0.18.0" || info.Commit != "" || info.BuiltAt != "2026-09-22T22:00:00Z" {
		t.Errorf("identity = %q/%q/%q", info.Version, info.Commit, info.BuiltAt)
	}
	if len(info.Features) != 2 || info.Features[1] != "javascript" {
		t.Errorf("Features = %v", info.Features)
	}
	if len(info.ServeOptions) != 3 || info.ServeOptions[2] != "noParse" {
		t.Errorf("ServeOptions = %v", info.ServeOptions)
	}

	if _, err := parseBuildInfo(`not json`); !errors.Is(err, rift.ErrEngineUnavailable) {
		t.Errorf("garbage build info: err = %v, want ErrEngineUnavailable", err)
	}
}

func TestSupportsServeOption(t *testing.T) {
	listed := BuildInfo{ServeOptions: []string{"host", "port", "noParse"}}
	for name, want := range map[string]bool{"noParse": true, "port": true, "apiKey": false, "requireAdminAuth": false} {
		if got := listed.SupportsServeOption(name); got != want {
			t.Errorf("listed engine: SupportsServeOption(%q) = %v, want %v", name, got, want)
		}
	}

	// An engine older than 0.17.0 publishes no list. It accepts the seven original options and
	// silently ignores anything else, so only those seven count as supported.
	old := BuildInfo{Version: "0.16.0"}
	for name, want := range map[string]bool{
		"host": true, "port": true, "apiKey": true, "metricsPort": true, "configFile": true,
		"config": true, "allowInjection": true, "requireAdminAuth": false, "noParse": false,
	} {
		if got := old.SupportsServeOption(name); got != want {
			t.Errorf("pre-0.17 engine: SupportsServeOption(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestCheckServeOptionsRefusesWhatTheEngineWouldIgnore(t *testing.T) {
	old := BuildInfo{Version: "0.16.0"}

	// The case that matters: an old engine would drop requireAdminAuth and serve an open plane.
	err := checkServeOptions(old, mustMarshal(t, ServeOptions{RequireAdminAuth: true}))
	if !errors.Is(err, rift.ErrVersionMismatch) {
		t.Fatalf("err = %v, want ErrVersionMismatch", err)
	}
	for _, want := range []string{"requireAdminAuth", "0.16.0"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to name %q", err, want)
		}
	}

	if err := checkServeOptions(old, mustMarshal(t, ServeOptions{Port: 1, Host: "127.0.0.1", MetricsPort: 2,
		ConfigFile: "c.json", AllowInjection: true})); err != nil {
		t.Errorf("baseline options on a pre-0.17 engine: %v", err)
	}

	current := BuildInfo{Version: "0.18.0", ServeOptions: []string{"host", "port", "requireAdminAuth"}}
	if err := checkServeOptions(current, mustMarshal(t, ServeOptions{Host: "127.0.0.1", RequireAdminAuth: true})); err != nil {
		t.Errorf("listed options: %v", err)
	}
	if err := checkServeOptions(current, mustMarshal(t, ServeOptions{MetricsPort: 9})); !errors.Is(err, rift.ErrVersionMismatch) {
		t.Errorf("unlisted metricsPort: err = %v, want ErrVersionMismatch", err)
	}
	if err := checkServeOptions(current, mustMarshal(t, ServeOptions{})); err != nil {
		t.Errorf("zero options send nothing and need nothing: %v", err)
	}
}

func mustMarshal(t *testing.T, opts ServeOptions) []byte {
	t.Helper()
	b, err := json.Marshal(opts)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	return b
}
