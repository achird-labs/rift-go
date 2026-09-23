package riftembed_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/achird-labs/rift-go/rift"
	"github.com/achird-labs/rift-go/riftembed"
)

// privateOrigin is an HTTPS upstream whose leaf is issued by a CA no system trust store holds,
// standing in for a service behind a corporate CA. httptest's own certificate will not do: it is
// a self-signed CA serving as its own leaf, which a strict verifier refuses whatever it trusts.
func privateOrigin(t *testing.T) (url, caPEM string) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "rift-go test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, ca, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("from-origin"))
	}))
	srv.TLS = &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{leafDER}, PrivateKey: leafKey}},
		MinVersion:   tls.VersionTLS12,
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv.URL, string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}))
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
