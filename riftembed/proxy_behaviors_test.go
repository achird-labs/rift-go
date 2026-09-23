package riftembed_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/achird-labs/rift-go/rift"
)

// Engine 0.18.0 runs a proxy response's behaviors; before, a wait on a proxy was silently dropped.
func TestProxyResponseWaitIsHonoured(t *testing.T) {
	eng := startEngine(t)
	ctx := t.Context()
	origin, err := eng.CreateImposter(ctx, rift.NewImposter("origin").
		Stub(rift.OnAny().Return(rift.OKText("from-origin"))))
	if err != nil {
		t.Fatalf("CreateImposter origin: %v", err)
	}
	const wait = 150 * time.Millisecond
	front, err := eng.CreateImposter(ctx, rift.NewImposter("front").
		Stub(rift.OnAny().Return(rift.Proxy(fmt.Sprintf("http://127.0.0.1:%d", origin)).Always().After(wait))))
	if err != nil {
		t.Fatalf("CreateImposter front: %v", err)
	}

	start := time.Now()
	body, status := httpGet(t, fmt.Sprintf("http://127.0.0.1:%d/x", front))
	elapsed := time.Since(start)
	if status != 200 || body != "from-origin" {
		t.Fatalf("got %d %q, want 200 %q", status, body, "from-origin")
	}
	if elapsed < wait {
		t.Errorf("answered in %s, want at least the %s wait", elapsed, wait)
	}
}
