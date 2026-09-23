package riftembed_test

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/achird-labs/rift-go/rift"
	"github.com/achird-labs/rift-go/riftembed"
)

// privateOrigin is an HTTPS upstream whose leaf is issued by a CA no system trust store holds,
// standing in for a service behind a corporate CA.
func privateOrigin(t *testing.T) (url, caPEM string) {
	t.Helper()
	ca := newTestCA(t)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("from-origin"))
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{ca.serverLeaf(t).TLS}, MinVersion: tls.VersionTLS12}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv.URL, ca.PEM
}

// proxyThrough serves the admin plane with opts, then creates a proxy imposter to origin and
// returns what a request through it gets. The imposter is created after ServeAdmin because
// imposters pick up the upstream trust policy when they are created.
func proxyThrough(t *testing.T, origin string, opts riftembed.ServeOptions) (string, int) {
	t.Helper()
	eng := startEngine(t)
	opts.Host = "127.0.0.1"
	if _, err := eng.ServeAdmin(t.Context(), opts); err != nil {
		t.Fatalf("ServeAdmin: %v", err)
	}
	port, err := eng.CreateImposter(t.Context(), rift.NewImposter("via").
		Stub(rift.OnAny().Return(rift.Proxy(origin).Always())))
	if err != nil {
		t.Fatalf("CreateImposter: %v", err)
	}
	return httpGet(t, fmt.Sprintf("http://127.0.0.1:%d/x", port))
}

func TestUpstreamTrust(t *testing.T) {
	origin, caPEM := privateOrigin(t)

	t.Run("untrusted by default", func(t *testing.T) {
		if body, status := proxyThrough(t, origin, riftembed.ServeOptions{}); status == 200 {
			t.Errorf("proxy to a private-CA origin succeeded without a trust anchor: %q", body)
		}
	})

	t.Run("inline anchor", func(t *testing.T) {
		body, status := proxyThrough(t, origin, riftembed.ServeOptions{UpstreamCAPEM: caPEM})
		if status != 200 || body != "from-origin" {
			t.Errorf("got %d %q, want 200 %q", status, body, "from-origin")
		}
	})

	t.Run("anchor file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "ca.pem")
		if err := os.WriteFile(path, []byte(caPEM), 0o600); err != nil {
			t.Fatal(err)
		}
		body, status := proxyThrough(t, origin, riftembed.ServeOptions{UpstreamCAFile: path})
		if status != 200 || body != "from-origin" {
			t.Errorf("got %d %q, want 200 %q", status, body, "from-origin")
		}
	})

	t.Run("skip verify", func(t *testing.T) {
		body, status := proxyThrough(t, origin, riftembed.ServeOptions{UpstreamTLSSkipVerify: true})
		if status != 200 || body != "from-origin" {
			t.Errorf("got %d %q, want 200 %q", status, body, "from-origin")
		}
	})
}

func TestBothUpstreamAnchorsAreRefusedBeforeTheEngine(t *testing.T) {
	eng := startEngine(t)
	if err := eng.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	_, err := eng.ServeAdmin(t.Context(), riftembed.ServeOptions{UpstreamCAFile: "ca.pem", UpstreamCAPEM: "pem"})
	if !errors.Is(err, rift.ErrInvalidDefinition) {
		t.Errorf("err = %v, want ErrInvalidDefinition", err)
	}
	if errors.Is(err, rift.ErrClosed) {
		t.Errorf("err = %v: the engine was reached before the options were checked", err)
	}
}
