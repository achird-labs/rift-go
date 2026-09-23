package riftembed

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

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
//
// With no CA source the engine mints a fresh CA, which is what a test almost always wants. A CA
// can instead be supplied inline (CACertPEM and CAKeyPEM) or from files (CACertPath and
// CAKeyPath); each pair is both-or-neither, and the two pairs are mutually exclusive.
type InterceptOptions struct {
	// Port pins the proxy's listening port. Zero lets the engine choose.
	Port uint16 `json:"port,omitempty"`
	// Host binds the proxy to an interface. It must be an IP literal: engines from 0.18.0 on
	// refuse a name such as "localhost".
	Host string `json:"host,omitempty"`

	CACertPEM  string `json:"caCertPem,omitempty"`
	CAKeyPEM   string `json:"caKeyPem,omitempty"`
	CACertPath string `json:"caCertPath,omitempty"`
	CAKeyPath  string `json:"caKeyPath,omitempty"`

	// ReturnCAKey has the engine mint a CA and hand back its certificate and private key once,
	// through Intercept.CAMaterial, so other engines can be started with the same anchor. It is
	// only valid when no CA source is supplied.
	ReturnCAKey bool `json:"returnCaKey,omitempty"`

	// Auth makes the listener require Proxy-Authorization on CONNECT. ProxyURL and HTTPClient
	// carry the credentials, so a client built from them keeps working.
	Auth *InterceptAuth `json:"auth,omitempty"`

	// Rules are installed before the listener accepts a connection, so no request can race a
	// follow-up AddRules. Engine 0.18.0 or later; omit it for an older engine.
	Rules []rift.InterceptRule `json:"rules,omitempty"`
}

// InterceptAuth is the credential the intercept listener demands on CONNECT. Neither field may be
// blank: a blank secret would switch the gate on and then admit everyone.
type InterceptAuth struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// validate mirrors the engine's own refusals, so a bad combination is named before a listener
// half-exists.
func (o InterceptOptions) validate() error {
	pemPair, err := caPair("caCertPem/caKeyPem", o.CACertPEM, o.CAKeyPEM)
	if err != nil {
		return err
	}
	pathPair, err := caPair("caCertPath/caKeyPath", o.CACertPath, o.CAKeyPath)
	if err != nil {
		return err
	}
	switch {
	case pemPair && pathPair:
		return fmt.Errorf("%w: intercept CA: supply the PEM pair or the path pair, not both",
			rift.ErrInvalidDefinition)
	case o.ReturnCAKey && (pemPair || pathPair):
		return fmt.Errorf("%w: intercept CA: ReturnCAKey mints a new CA, so it cannot be combined "+
			"with a supplied one", rift.ErrInvalidDefinition)
	}
	if o.Auth != nil && (strings.TrimSpace(o.Auth.Username) == "" || strings.TrimSpace(o.Auth.Password) == "") {
		return fmt.Errorf("%w: intercept auth needs a non-blank username and password",
			rift.ErrInvalidDefinition)
	}
	return nil
}

// caPair reports whether a both-or-neither pair is set, and refuses a half-set one.
func caPair(name, cert, key string) (bool, error) {
	if (cert == "") != (key == "") {
		return false, fmt.Errorf("%w: intercept CA: %s must be supplied together",
			rift.ErrInvalidDefinition, name)
	}
	return cert != "", nil
}

// InterceptInfo describes a running listener, as the engine reports it:
// {"interceptPort":62969,"interceptUrl":"http://127.0.0.1:62969"}.
type InterceptInfo struct {
	Port uint16 `json:"interceptPort"`
	URL  string `json:"interceptUrl"`
}

// startResponse is the whole start reply. The minted CA material is decoded apart from
// InterceptInfo so the private key never sits in a type callers print.
type startResponse struct {
	InterceptInfo
	CACertPEM string `json:"caCertPem"`
	CAKeyPEM  string `json:"caKeyPem"`
}

// CAMaterial is a CA certificate and its private key. The key is secret material, so the value
// formats with the key redacted; read KeyPEM explicitly to persist it.
type CAMaterial struct {
	CertPEM string
	KeyPEM  string
}

// String keeps the private key out of %v and %+v.
func (m CAMaterial) String() string {
	return fmt.Sprintf("CAMaterial{CertPEM: %d bytes, KeyPEM: <redacted>}", len(m.CertPEM))
}

// GoString keeps the private key out of %#v.
func (m CAMaterial) GoString() string { return m.String() }

// StartIntercept starts the intercept listener and returns its details.
func (e *Engine) StartIntercept(ctx context.Context, opts InterceptOptions) (*Intercept, error) {
	if err := opts.validate(); err != nil {
		return nil, err
	}
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
	var resp startResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		// Not %s of raw: with ReturnCAKey it holds the CA private key.
		return nil, fmt.Errorf("%w: decode intercept info: %w", rift.ErrInvalidDefinition, err)
	}
	if resp.Port == 0 {
		return nil, fmt.Errorf("%w: engine reported no intercept port", rift.ErrInvalidDefinition)
	}
	ic := &Intercept{engine: e, info: resp.InterceptInfo, auth: opts.Auth}
	if resp.CACertPEM != "" && resp.CAKeyPEM != "" {
		ic.ca = &CAMaterial{CertPEM: resp.CACertPEM, KeyPEM: resp.CAKeyPEM}
	}
	return ic, nil
}

// Intercept is a running intercept listener.
type Intercept struct {
	engine *Engine
	info   InterceptInfo
	auth   *InterceptAuth
	ca     *CAMaterial
}

// String describes the listener without its credentials or CA key, so printing an Intercept
// with %v or %+v is safe.
func (i *Intercept) String() string {
	return fmt.Sprintf("Intercept{port: %d, url: %s}", i.info.Port, i.ProxyURL().Redacted())
}

// GoString keeps credentials and the CA key out of %#v.
func (i *Intercept) GoString() string { return i.String() }

// Port is the proxy's listening port.
func (i *Intercept) Port() uint16 { return i.info.Port }

// CAMaterial returns the CA the engine minted when the listener was started with ReturnCAKey,
// and false otherwise. The key is secret material: persist it only where the CA is meant to be
// shared, and never log it.
func (i *Intercept) CAMaterial() (CAMaterial, bool) {
	if i.ca == nil {
		return CAMaterial{}, false
	}
	return *i.ca, true
}

// ProxyURL is the URL to point an HTTP client's proxy setting at. When the listener requires
// auth, the URL carries the credentials, which net/http sends as Proxy-Authorization; that also
// means its String form contains the password, so log u.Redacted() instead.
//
// It prefers the URL the engine reported over one reconstructed from the port, so a listener
// bound to a non-loopback interface is addressed correctly.
func (i *Intercept) ProxyURL() *url.URL {
	u := &url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", i.info.Port)}
	if i.info.URL != "" {
		if parsed, err := url.Parse(i.info.URL); err == nil {
			u = parsed
		}
	}
	if i.auth != nil {
		u.User = url.UserPassword(i.auth.Username, i.auth.Password)
	}
	return u
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
