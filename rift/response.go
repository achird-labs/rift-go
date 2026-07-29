package rift

import (
	"encoding/json"
	"time"
)

// ResponseSource is anything that can produce a wire StubResponse: the fluent builder, or a
// StubResponse the caller assembled directly.
type ResponseSource interface {
	BuildResponse() StubResponse
}

func (r StubResponse) BuildResponse() StubResponse { return r }

// ResponseBuilder builds one element of a stub's response cycle.
type ResponseBuilder struct {
	resp StubResponse
}

func (b *ResponseBuilder) BuildResponse() StubResponse { return b.resp }

// Build returns the wire response.
func (b *ResponseBuilder) Build() StubResponse { return b.resp }

// Status starts a canned response with the given HTTP status and no body.
func Status(code int) *ResponseBuilder {
	return &ResponseBuilder{resp: StubResponse{Is: &IsResponse{StatusCode: code}}}
}

// OK is a 200 with no body.
func OK() *ResponseBuilder { return Status(200) }

// Text is a canned response with a text/plain body.
func Text(code int, body string) *ResponseBuilder {
	return Status(code).WithHeader("Content-Type", "text/plain").WithBody(body)
}

// OKText is a 200 text/plain response.
func OKText(body string) *ResponseBuilder { return Text(200, body) }

// JSONBody is a canned response with an application/json body.
//
// A string argument is passed through verbatim (so a raw JSON literal stays exactly as written);
// any other value is marshalled. That distinction matters for the conformance corpus, where a
// body may legitimately be either a JSON string or a JSON object.
func JSONBody(code int, body JSON) *ResponseBuilder {
	return Status(code).WithHeader("Content-Type", "application/json").WithBody(body)
}

// OKJSON is a 200 application/json response.
func OKJSON(body JSON) *ResponseBuilder { return JSONBody(200, body) }

// Created is a 201 with an optional Location header.
func Created(location string) *ResponseBuilder {
	b := Status(201)
	if location != "" {
		b = b.WithHeader("Location", location)
	}
	return b
}

// NoContent is a 204.
func NoContent() *ResponseBuilder { return Status(204) }

// NotFound is a 404.
func NotFound() *ResponseBuilder { return Status(404) }

// WithHeader sets a response header. Repeat for multiple headers; pass a []string value via
// WithHeaderValues for a repeated header.
func (b *ResponseBuilder) WithHeader(name, value string) *ResponseBuilder {
	b.ensureIs()
	if b.resp.Is.Headers == nil {
		b.resp.Is.Headers = Headers{}
	}
	b.resp.Is.Headers[name] = value
	return b
}

// WithHeaderValues sets a header that appears multiple times.
func (b *ResponseBuilder) WithHeaderValues(name string, values ...string) *ResponseBuilder {
	b.ensureIs()
	if b.resp.Is.Headers == nil {
		b.resp.Is.Headers = Headers{}
	}
	vs := make([]JSON, len(values))
	for i, v := range values {
		vs[i] = v
	}
	b.resp.Is.Headers[name] = vs
	return b
}

// WithBody sets the response body.
func (b *ResponseBuilder) WithBody(body JSON) *ResponseBuilder {
	b.ensureIs()
	b.resp.Is.Body = body
	return b
}

// WithStatus overrides the status code.
func (b *ResponseBuilder) WithStatus(code int) *ResponseBuilder {
	b.ensureIs()
	b.resp.Is.StatusCode = code
	return b
}

// Binary marks the body as base64-encoded binary rather than text.
func (b *ResponseBuilder) Binary() *ResponseBuilder {
	b.ensureIs()
	b.resp.Is.Mode = "binary"
	return b
}

// After delays the response by d. The engine takes milliseconds; sub-millisecond precision
// is not representable on the wire and is rounded down.
func (b *ResponseBuilder) After(d time.Duration) *ResponseBuilder {
	b.ensureBehaviors()
	b.resp.Behaviors.Wait = int(d.Milliseconds())
	return b
}

// AfterBetween delays the response by a random duration in [min, max].
func (b *ResponseBuilder) AfterBetween(minD, maxD time.Duration) *ResponseBuilder {
	b.ensureBehaviors()
	b.resp.Behaviors.Wait = map[string]JSON{
		"min": int(minD.Milliseconds()),
		"max": int(maxD.Milliseconds()),
	}
	return b
}

// Repeat serves this response n times before the cycle advances.
func (b *ResponseBuilder) Repeat(n int) *ResponseBuilder {
	b.ensureBehaviors()
	b.resp.Behaviors.Repeat = n
	return b
}

// Decorate post-processes the response with a JavaScript function. Requires the engine to be
// started with injection enabled.
func (b *ResponseBuilder) Decorate(js string) *ResponseBuilder {
	b.ensureBehaviors()
	b.resp.Behaviors.Decorate = js
	return b
}

// Copy copies values out of the request into the response. The shape is the engine's `copy`
// behavior config, passed through verbatim.
func (b *ResponseBuilder) Copy(spec JSON) *ResponseBuilder {
	b.ensureBehaviors()
	b.resp.Behaviors.Copy = spec
	return b
}

// Lookup substitutes values from an external data source. The shape is the engine's `lookup`
// behavior config, passed through verbatim.
func (b *ResponseBuilder) Lookup(spec JSON) *ResponseBuilder {
	b.ensureBehaviors()
	b.resp.Behaviors.Lookup = spec
	return b
}

// ShellTransform pipes the response through an external command. Requires a host shell.
func (b *ResponseBuilder) ShellTransform(cmd ...string) *ResponseBuilder {
	b.ensureBehaviors()
	if len(cmd) == 1 {
		b.resp.Behaviors.ShellTransform = cmd[0]
		return b
	}
	vs := make([]JSON, len(cmd))
	for i, c := range cmd {
		vs[i] = c
	}
	b.resp.Behaviors.ShellTransform = vs
	return b
}

// Templated marks the response body as containing engine template expressions.
func (b *ResponseBuilder) Templated() *ResponseBuilder {
	if b.resp.Rift == nil {
		b.resp.Rift = &RiftResponse{}
	}
	b.resp.Rift.Templated = true
	return b
}

func (b *ResponseBuilder) ensureIs() {
	if b.resp.Is == nil {
		b.resp.Is = &IsResponse{}
	}
}

func (b *ResponseBuilder) ensureBehaviors() {
	if b.resp.Behaviors == nil {
		b.resp.Behaviors = &Behaviors{}
	}
}

// --- non-`is` responses ---

// Fault returns a connection-level fault instead of a response. Known values include
// "CONNECTION_RESET_BY_PEER", "EMPTY_RESPONSE", "MALFORMED_RESPONSE_CHUNK" and
// "RANDOM_DATA_THEN_CLOSE"; the SDK passes the string through so a newer engine's fault works
// without an SDK release.
func Fault(kind string) *ResponseBuilder {
	return &ResponseBuilder{resp: StubResponse{Fault: kind}}
}

// InjectResponse computes the response with a JavaScript function. Requires injection enabled.
func InjectResponse(js string) *ResponseBuilder {
	return &ResponseBuilder{resp: StubResponse{Inject: js}}
}

// Proxy forwards matching requests to an upstream.
func Proxy(to string) *ProxyBuilder {
	return &ProxyBuilder{proxy: ProxyResponse{To: to}}
}

// ProxyBuilder builds a proxy response.
type ProxyBuilder struct {
	proxy ProxyResponse
}

func (b *ProxyBuilder) BuildResponse() StubResponse {
	p := b.proxy
	return StubResponse{Proxy: &p}
}

// Build returns the wire response.
func (b *ProxyBuilder) Build() StubResponse { return b.BuildResponse() }

// Once records the first response and replays it thereafter (proxyOnce).
func (b *ProxyBuilder) Once() *ProxyBuilder { b.proxy.Mode = "proxyOnce"; return b }

// Always proxies every request upstream (proxyAlways).
func (b *ProxyBuilder) Always() *ProxyBuilder { b.proxy.Mode = "proxyAlways"; return b }

// Transparent proxies without recording (proxyTransparent).
func (b *ProxyBuilder) Transparent() *ProxyBuilder { b.proxy.Mode = "proxyTransparent"; return b }

// GeneratingPredicates sets the predicateGenerators that shape recorded stubs.
func (b *ProxyBuilder) GeneratingPredicates(gens ...JSON) *ProxyBuilder {
	b.proxy.PredicateGenerators = gens
	return b
}

// InjectHeader adds a header to the upstream request.
func (b *ProxyBuilder) InjectHeader(name, value string) *ProxyBuilder {
	if b.proxy.InjectHeaders == nil {
		b.proxy.InjectHeaders = map[string]string{}
	}
	b.proxy.InjectHeaders[name] = value
	return b
}

// RewritePath rewrites the request path before forwarding.
func (b *ProxyBuilder) RewritePath(from, to string) *ProxyBuilder {
	b.proxy.PathRewrite = &PathRewrite{From: from, To: to}
	return b
}

// WithClientCert supplies a client certificate for a mutually-authenticated upstream.
func (b *ProxyBuilder) WithClientCert(cert, key string) *ProxyBuilder {
	b.proxy.Cert, b.proxy.Key = cert, key
	return b
}

// ResponseFromJSON parses a raw response document — the escape hatch for a response shape the
// builders do not cover.
func ResponseFromJSON(data []byte) (StubResponse, error) {
	var r StubResponse
	if err := json.Unmarshal(data, &r); err != nil {
		return StubResponse{}, wrapInvalid("parse response", err)
	}
	return r, nil
}
