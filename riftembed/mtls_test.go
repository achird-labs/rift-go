package riftembed_test

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/achird-labs/rift-go/rift"
)

// An HTTPS imposter built with RequireClientCert must actually gate the handshake: this is the
// behaviour that engines before 0.18.0 documented and silently dropped.
func TestRequireClientCertGatesTheHandshake(t *testing.T) {
	serverCA, clientCA, foreignCA := newTestCA(t), newTestCA(t), newTestCA(t)
	server := serverCA.serverLeaf(t)

	eng := startEngine(t)
	ctx := t.Context()

	get := func(t *testing.T, port uint16, client *leaf) (int, error) {
		t.Helper()
		roots := x509.NewCertPool()
		roots.AppendCertsFromPEM([]byte(serverCA.PEM))
		cfg := &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
		if client != nil {
			cfg.Certificates = []tls.Certificate{client.TLS}
		}
		tr := &http.Transport{TLSClientConfig: cfg}
		defer tr.CloseIdleConnections()
		c := &http.Client{Transport: tr}
		resp, err := c.Get(fmt.Sprintf("https://127.0.0.1:%d/x", port)) //nolint:noctx // short test request
		if err != nil {
			return 0, err
		}
		defer func() { _ = resp.Body.Close() }()
		_, _ = io.Copy(io.Discard, resp.Body)
		return resp.StatusCode, nil
	}

	t.Run("pinned CA", func(t *testing.T) {
		port, err := eng.CreateImposter(ctx, rift.NewImposter("mtls").
			HTTPS(server.CertPEM, server.KeyPEM).RequireClientCert(clientCA.PEM).
			Stub(rift.OnAny().Return(rift.OK())))
		if err != nil {
			t.Fatalf("CreateImposter: %v", err)
		}

		if _, err := get(t, port, nil); err == nil {
			t.Error("a client with no certificate completed the handshake")
		}
		trusted := clientCA.clientLeaf(t)
		if status, err := get(t, port, &trusted); err != nil || status != 200 {
			t.Errorf("client with a certificate from the pinned CA: %d, %v; want 200", status, err)
		}
		foreign := foreignCA.clientLeaf(t)
		if _, err := get(t, port, &foreign); err == nil {
			t.Error("a client certificate from an unpinned CA completed the handshake")
		}
	})

	t.Run("any certificate", func(t *testing.T) {
		port, err := eng.CreateImposter(ctx, rift.NewImposter("mtls-any").
			HTTPS(server.CertPEM, server.KeyPEM).RequireClientCert().
			Stub(rift.OnAny().Return(rift.OK())))
		if err != nil {
			t.Fatalf("CreateImposter: %v", err)
		}
		if _, err := get(t, port, nil); err == nil {
			t.Error("a client with no certificate completed the handshake")
		}
		anyCert := foreignCA.clientLeaf(t)
		if status, err := get(t, port, &anyCert); err != nil || status != 200 {
			t.Errorf("client with any certificate: %d, %v; want 200", status, err)
		}
	})
}

// Mutual auth on a cleartext imposter was silently dropped before 0.18.0; now it is refused.
func TestRequireClientCertOnHTTPIsRefused(t *testing.T) {
	_, err := startEngine(t).CreateImposter(t.Context(), rift.NewImposter("plain").
		Protocol("http").RequireClientCert())
	if !errors.Is(err, rift.ErrInvalidDefinition) {
		t.Errorf("err = %v, want the engine's refusal as ErrInvalidDefinition", err)
	}
}
