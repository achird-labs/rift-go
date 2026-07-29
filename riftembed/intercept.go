package riftembed

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/achird-labs/rift-go/rift"
)

// Intercept is a TLS-terminating forward proxy: it lets a test mock an HTTPS dependency whose
// host the system under test hard-codes and cannot be repointed.
//
// The mechanism is unavoidable — the SUT must trust the intercept CA — so what the SDK can do
// is remove the provisioning work. TLSConfig and HTTPClient hand back a client that already
// trusts the CA and already routes through the proxy, which is the whole ceremony reduced to
// one call.

// InterceptOptions configure the intercept listener.
type InterceptOptions struct {
	// Port pins the proxy's listening port. Zero lets the engine choose.
	Port uint16 `json:"port,omitempty"`
	// Host binds the proxy to a specific interface.
	Host string `json:"host,omitempty"`
	// CACertPEM and CAKeyPEM supply an existing certificate authority. Both empty means the
	// engine mints one, which is what a test almost always wants.
	CACertPEM string `json:"caCert,omitempty"`
	CAKeyPEM  string `json:"caKey,omitempty"`
}

// InterceptInfo describes a running listener, as the engine reports it:
// {"interceptPort":62969,"interceptUrl":"http://127.0.0.1:62969"}.
type InterceptInfo struct {
	Port uint16 `json:"interceptPort"`
	URL  string `json:"interceptUrl"`
}

// StartIntercept starts the intercept listener and returns its details.
func (e *Engine) StartIntercept(ctx context.Context, opts InterceptOptions) (*Intercept, error) {
	body, err := json.Marshal(opts)
	if err != nil {
		return nil, err
	}
	var raw json.RawMessage
	if err := e.withHandle(ctx, func(h uintptr) error {
		raw, err = e.takeJSON("start intercept", e.sym.startIntercept(h, string(body)))
		return err
	}); err != nil {
		return nil, err
	}
	var info InterceptInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		return nil, fmt.Errorf("%w: decode intercept info: %w", rift.ErrInvalidDefinition, err)
	}
	if info.Port == 0 {
		return nil, fmt.Errorf("%w: engine reported no intercept port (%s)",
			rift.ErrInvalidDefinition, raw)
	}
	return &Intercept{engine: e, info: info}, nil
}

// Intercept is a running intercept listener.
type Intercept struct {
	engine *Engine
	info   InterceptInfo
}

// Port is the proxy's listening port.
func (i *Intercept) Port() uint16 { return i.info.Port }

// ProxyURL is the URL to point an HTTP client's proxy setting at.
//
// It prefers the URL the engine reported over one reconstructed from the port, so a listener
// bound to a non-loopback interface is addressed correctly.
func (i *Intercept) ProxyURL() *url.URL {
	if i.info.URL != "" {
		if u, err := url.Parse(i.info.URL); err == nil {
			return u
		}
	}
	return &url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", i.info.Port)}
}

// Stop shuts the listener down.
func (i *Intercept) Stop(ctx context.Context) error {
	return i.engine.withHandle(ctx, func(h uintptr) error {
		return i.engine.checkRC("stop intercept", i.engine.sym.stopIntercept(h))
	})
}

// AddRules appends interception rules. Rules are evaluated in order; the first match wins.
func (i *Intercept) AddRules(ctx context.Context, rules ...rift.InterceptRule) error {
	body, err := rift.ToJSON(rules)
	if err != nil {
		return err
	}
	return i.engine.withHandle(ctx, func(h uintptr) error {
		return i.engine.checkRC("add intercept rules",
			i.engine.sym.interceptAddRules(h, string(body)))
	})
}

// ClearRules removes every rule, leaving the listener running.
func (i *Intercept) ClearRules(ctx context.Context) error {
	return i.engine.withHandle(ctx, func(h uintptr) error {
		return i.engine.checkRC("clear intercept rules",
			i.engine.sym.interceptClear(h))
	})
}

// ListRules returns the active rules.
func (i *Intercept) ListRules(ctx context.Context) ([]rift.InterceptRule, error) {
	var raw json.RawMessage
	var err error
	if err = i.engine.withHandle(ctx, func(h uintptr) error {
		raw, err = i.engine.takeJSON("list intercept rules", i.engine.sym.interceptListRules(h))
		return err
	}); err != nil {
		return nil, err
	}
	var rules []rift.InterceptRule
	if err := json.Unmarshal(raw, &rules); err != nil {
		return nil, fmt.Errorf("%w: decode intercept rules: %w", rift.ErrInvalidDefinition, err)
	}
	return rules, nil
}

// CACertPEM returns the intercept CA certificate. Anything that must trust the proxy needs this.
func (i *Intercept) CACertPEM(ctx context.Context) ([]byte, error) {
	var raw json.RawMessage
	var err error
	if err = i.engine.withHandle(ctx, func(h uintptr) error {
		raw, err = i.engine.takeJSON("intercept CA", i.engine.sym.interceptCAPEM(h))
		return err
	}); err != nil {
		return nil, err
	}
	// The engine may hand back a bare PEM or a JSON string wrapping one; accept both rather
	// than depending on which.
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil && asString != "" {
		return []byte(asString), nil
	}
	return raw, nil
}

// ExportTruststore writes a truststore containing the CA to outPath.
//
// format is engine-defined — "pem", "pkcs12" and "jks" are the ones that exist today, and the
// string is passed through so a newer engine's format works without an SDK release. A JVM system
// under test wants "jks" or "pkcs12"; a Go one needs nothing but TLSConfig.
func (i *Intercept) ExportTruststore(ctx context.Context, format, password, outPath string) error {
	return i.engine.withHandle(ctx, func(h uintptr) error {
		return i.engine.checkRC("export truststore",
			i.engine.sym.interceptTruststor(h, format, password, outPath))
	})
}

// TLSConfig returns a tls.Config trusting the intercept CA.
//
// It seeds the pool from the system roots so a client using this config still reaches ordinary
// hosts; only the interception is added, nothing is taken away.
func (i *Intercept) TLSConfig(ctx context.Context) (*tls.Config, error) {
	pemBytes, err := i.CACertPEM(ctx)
	if err != nil {
		return nil, err
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(pemBytes) {
		return nil, fmt.Errorf("%w: the engine's CA PEM could not be parsed", rift.ErrInvalidDefinition)
	}
	return &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}, nil
}

// HTTPClient returns an *http.Client that routes through the intercept proxy and trusts its CA —
// a system under test that accepts an injected client needs no other configuration.
func (i *Intercept) HTTPClient(ctx context.Context) (*http.Client, error) {
	tlsCfg, err := i.TLSConfig(ctx)
	if err != nil {
		return nil, err
	}
	proxyURL := i.ProxyURL()
	return &http.Client{
		Transport: &http.Transport{
			Proxy:           http.ProxyURL(proxyURL),
			TLSClientConfig: tlsCfg,
		},
	}, nil
}

// --- rule builders ---

// InterceptServe builds a rule serving a canned response for a host.
func InterceptServe(host string, r *rift.ResponseBuilder) rift.InterceptRule {
	resp := r.BuildResponse()
	is := resp.Is
	if is == nil {
		is = &rift.IsResponse{}
	}
	return rift.InterceptRule{Host: host, Action: rift.InterceptAction{Serve: is}}
}

// InterceptForward builds a rule re-proxying a host's traffic, in plaintext, to a local imposter.
// This is the common shape: intercept the HTTPS call the SUT hard-codes, and answer it with a
// full imposter rather than a single canned response.
func InterceptForward(host string, port uint16) rift.InterceptRule {
	return rift.InterceptRule{
		Host:   host,
		Action: rift.InterceptAction{Forward: &rift.InterceptTarget{Port: port}},
	}
}
