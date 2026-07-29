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

// RecordMatches additionally records which stub matched each request.
func (b *ImposterBuilder) RecordMatches() *ImposterBuilder {
	b.imp.RecordMatches = true
	return b
}

// AllowCORS enables permissive CORS handling.
func (b *ImposterBuilder) AllowCORS() *ImposterBuilder {
	b.imp.AllowCORS = true
	return b
}

// MutualAuth requires a client certificate (HTTPS imposters).
func (b *ImposterBuilder) MutualAuth() *ImposterBuilder {
	b.imp.MutualAuth = true
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
