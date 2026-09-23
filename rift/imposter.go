package rift

import "fmt"

// ImposterSource is anything that can produce a wire Imposter: the fluent builder, or an
// Imposter the caller assembled directly (including one from ImposterFromJSON).
type ImposterSource interface {
	BuildImposter() Imposter
}

func (i Imposter) BuildImposter() Imposter { return i }

// ImposterBuilder builds an imposter definition.
type ImposterBuilder struct {
	imp Imposter
}

// NewImposter starts an imposter with the given name. The name is metadata only — it does not
// affect matching — but it makes admin listings and failure messages readable.
//
// The port is left for the engine to assign. Call Port to pin it; an explicit port is respected
// verbatim.
func NewImposter(name string) *ImposterBuilder {
	return &ImposterBuilder{imp: Imposter{Name: name}}
}

// Port pins the listening port.
func (b *ImposterBuilder) Port(p uint16) *ImposterBuilder {
	b.imp.Port = p
	return b
}

// Protocol sets the protocol ("http", "https", "h2c"). Prefer HTTPS for a TLS imposter.
func (b *ImposterBuilder) Protocol(p string) *ImposterBuilder {
	b.imp.Protocol = p
	return b
}

// HTTPS switches the imposter to TLS with an inline PEM certificate and key. Passing empty
// strings selects the engine's self-signed default certificate.
func (b *ImposterBuilder) HTTPS(certPEM, keyPEM string) *ImposterBuilder {
	b.imp.Protocol = "https"
	b.imp.Cert, b.imp.Key = certPEM, keyPEM
	return b
}

// Host binds the imposter to a specific interface.
func (b *ImposterBuilder) Host(h string) *ImposterBuilder {
	b.imp.Host = h
	return b
}

// Record turns on request recording, so Recorded and Verify have a journal to read.
func (b *ImposterBuilder) Record() *ImposterBuilder {
	b.imp.RecordRequests = true
	return b
}

// RecordMatches sets the Mountebank recordMatches key.
//
// Deprecated: the key has never had an effect on a Rift engine, which does not record per-stub
// matches, and engines from 0.18.0 on report it as a config_key_ignored warning. Use Record and
// the request journal to see what arrived. It still serialises, so a config built for Mountebank
// round-trips unchanged.
func (b *ImposterBuilder) RecordMatches() *ImposterBuilder {
	b.imp.RecordMatches = true
	return b
}

// AllowCORS enables permissive CORS handling.
func (b *ImposterBuilder) AllowCORS() *ImposterBuilder {
	b.imp.AllowCORS = true
	return b
}

// MutualAuth requires a client certificate at the TLS handshake, without validating it.
//
// Deprecated: use RequireClientCert, which also sets the protocol and can pin trust anchors.
// Since engine 0.18.0 this key is enforced: a client that presents no certificate fails the
// handshake, and a non-HTTPS imposter carrying it is refused.
func (b *ImposterBuilder) MutualAuth() *ImposterBuilder {
	b.imp.MutualAuth = true
	return b
}

// RequireClientCert makes an HTTPS imposter demand a client certificate at the TLS handshake. With
// one or more CA PEMs the certificate must also chain to one of them; with none, any certificate
// the client holds is accepted. The protocol becomes "https" unless one was already set.
//
// Engine 0.18.0 or later. Older engines ignore all three keys and accept every client.
func (b *ImposterBuilder) RequireClientCert(caPEMs ...string) *ImposterBuilder {
	if b.imp.Protocol == "" {
		b.imp.Protocol = "https"
	}
	b.imp.MutualAuth = true
	switch len(caPEMs) {
	case 0:
		b.imp.RejectUnauthorized, b.imp.CA = false, nil
	case 1:
		b.imp.RejectUnauthorized, b.imp.CA = true, caPEMs[0]
	default:
		anchors := make([]JSON, len(caPEMs))
		for i, p := range caPEMs {
			anchors[i] = p
		}
		b.imp.RejectUnauthorized, b.imp.CA = true, anchors
	}
	return b
}

// StrictBehaviors makes behavior errors fail the response instead of being tolerated.
func (b *ImposterBuilder) StrictBehaviors() *ImposterBuilder {
	b.imp.StrictBehaviors = true
	return b
}

// DefaultResponse sets the response served when no stub matches.
func (b *ImposterBuilder) DefaultResponse(r *ResponseBuilder) *ImposterBuilder {
	resp := r.BuildResponse()
	if resp.Is != nil {
		b.imp.DefaultResponse = resp.Is
	}
	return b
}

// DefaultForward proxies unmatched requests to an upstream instead of serving a default.
func (b *ImposterBuilder) DefaultForward(to string) *ImposterBuilder {
	b.imp.DefaultForward = to
	return b
}

// Stub appends stubs. Order matters: the engine serves the first stub whose predicates match.
func (b *ImposterBuilder) Stub(stubs ...StubSource) *ImposterBuilder {
	for _, s := range stubs {
		b.imp.Stubs = append(b.imp.Stubs, s.BuildStub())
	}
	return b
}

// WithRift sets the `_rift` extension block verbatim.
func (b *ImposterBuilder) WithRift(cfg *RiftImposter) *ImposterBuilder {
	b.imp.Rift = cfg
	return b
}

// WithExtra sets an arbitrary top-level key — the escape hatch for a grammar addition the
// builder does not model yet.
func (b *ImposterBuilder) WithExtra(key string, value JSON) *ImposterBuilder {
	if b.imp.Extra == nil {
		b.imp.Extra = map[string]JSON{}
	}
	b.imp.Extra[key] = value
	return b
}

// Build returns the wire imposter.
func (b *ImposterBuilder) Build() Imposter { return b.imp }

func (b *ImposterBuilder) BuildImposter() Imposter { return b.imp }

// BaseURL returns the URL an imposter on port listens at, for the given protocol.
func BaseURL(protocol string, port uint16) string {
	if protocol == "" {
		protocol = "http"
	}
	return fmt.Sprintf("%s://localhost:%d", protocol, port)
}
