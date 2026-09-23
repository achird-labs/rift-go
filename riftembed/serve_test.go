package riftembed_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/achird-labs/rift-go/rift"
	"github.com/achird-labs/rift-go/riftembed"
)

func TestServeAdminRefusesABlankAPIKeyBeforeTheEngine(t *testing.T) {
	eng := startEngine(t)
	// A closed engine answers ErrClosed from anything that reaches the handle, so getting
	// ErrInvalidDefinition back proves the key is checked first.
	if err := eng.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	for _, key := range []string{" ", "\t\n", "   "} {
		t.Run(fmt.Sprintf("%q", key), func(t *testing.T) {
			_, err := eng.ServeAdmin(t.Context(), riftembed.ServeOptions{APIKey: key})
			if !errors.Is(err, rift.ErrInvalidDefinition) {
				t.Errorf("err = %v, want ErrInvalidDefinition", err)
			}
			if errors.Is(err, rift.ErrClosed) {
				t.Errorf("err = %v: the engine was reached before the key was checked", err)
			}
		})
	}
}

func TestServeAdminWithAPIKeyGatesTheAdminPlane(t *testing.T) {
	eng := startEngine(t)
	ctx := t.Context()

	raw, err := eng.ServeAdmin(ctx, riftembed.ServeOptions{Host: "127.0.0.1", APIKey: "s3cret"})
	if err != nil {
		t.Fatalf("ServeAdmin: %v", err)
	}
	var served struct {
		AdminURL string `json:"adminUrl"`
	}
	if err := json.Unmarshal(raw, &served); err != nil || served.AdminURL == "" {
		t.Fatalf("ServeAdmin returned %s (%v), want an adminUrl", raw, err)
	}

	keyed, err := rift.Connect(served.AdminURL, rift.RemoteOptions{APIKey: "s3cret"})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = keyed.Close() })
	if _, err := keyed.Config(ctx); err != nil {
		t.Errorf("Config with the key: %v", err)
	}

	anon, err := rift.Connect(served.AdminURL, rift.RemoteOptions{})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = anon.Close() })
	_, err = anon.Config(ctx)
	var ee *rift.EngineError
	if !errors.As(err, &ee) || ee.Code != 401 {
		t.Errorf("Config without the key = %v, want an EngineError with code 401", err)
	}
}

func TestServeAdminWithoutAPIKeyIsUnauthenticated(t *testing.T) {
	eng := startEngine(t)
	ctx := t.Context()

	raw, err := eng.ServeAdmin(ctx, riftembed.ServeOptions{Host: "127.0.0.1"})
	if err != nil {
		t.Fatalf("ServeAdmin: %v", err)
	}
	var served struct {
		AdminURL string `json:"adminUrl"`
	}
	if err := json.Unmarshal(raw, &served); err != nil || served.AdminURL == "" {
		t.Fatalf("ServeAdmin returned %s (%v), want an adminUrl", raw, err)
	}

	anon, err := rift.Connect(served.AdminURL, rift.RemoteOptions{})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = anon.Close() })
	if _, err := anon.Config(ctx); err != nil {
		t.Errorf("Config without a key on an unkeyed plane: %v", err)
	}
}
