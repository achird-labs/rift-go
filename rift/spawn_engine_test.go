package rift_test

import (
	"errors"
	"os"
	"testing"

	"github.com/achird-labs/rift-go/rift"
)

// TestSpawnWithAPIKeyAuthenticates runs against a real engine binary. It pins the admin key end to
// end: the flag Spawn passes must start the engine, and the header Connect sends must be the one
// the engine checks.
func TestSpawnWithAPIKeyAuthenticates(t *testing.T) {
	if os.Getenv(rift.EnvBinary) == "" {
		t.Skip("set RIFT_BINARY to run against a real engine")
	}
	ctx := t.Context()

	eng, err := rift.Spawn(ctx, rift.SpawnOptions{APIKey: "s3cret"})
	if err != nil {
		t.Fatalf("Spawn with an API key: %v", err)
	}
	t.Cleanup(func() { _ = eng.Close() })

	if _, err := eng.Config(ctx); err != nil {
		t.Fatalf("Config with the key: %v", err)
	}

	for name, key := range map[string]string{"no key": "", "wrong key": "nope"} {
		t.Run(name, func(t *testing.T) {
			other, err := rift.Connect(eng.AdminURL(), rift.RemoteOptions{APIKey: key})
			if err != nil {
				t.Fatalf("Connect: %v", err)
			}
			t.Cleanup(func() { _ = other.Close() })

			_, err = other.Config(ctx)
			var ee *rift.EngineError
			if !errors.As(err, &ee) || ee.Code != 401 {
				t.Errorf("Config = %v, want an EngineError with code 401", err)
			}
		})
	}
}
