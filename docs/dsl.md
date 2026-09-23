# Building imposters

An **imposter** is a mock server on a port. It holds **stubs**; each stub has **predicates** that
select requests and **responses** served when they match.

```go
imp := rift.NewImposter("orders").Record().
	Stub(rift.OnPost("/orders").
		WithHeader("Content-Type", rift.Contains("json")).
		Return(rift.Created("/orders/1"))).
	Stub(rift.OnGet("/orders/1").
		Return(rift.OKJSON(order))).
	Stub(rift.OnAny().Return(rift.Status(404)))
```

## Stub order decides the winner

The engine serves the **first stub whose predicates match**. A catch-all belongs last:

```go
// Wrong — the catch-all wins every time, and /health is dead code.
rift.NewImposter("x").
	Stub(rift.OnAny().Return(rift.Status(404))).
	Stub(rift.OnGet("/health").Return(rift.OKText("ok")))
```

The same applies when adding a stub to a **live** imposter: `AddStub` appends, so a stub added
behind an existing catch-all is unreachable. Use `AddStubAt(0, …)` to put one in front.

## Predicates

Matchers are values, so they compose freely:

```go
rift.OnGet("/search").
	WithQuery("q", rift.Equals("widgets")).
	WithHeader("Accept", rift.Contains("json")).
	WithBody(rift.Matches(`"total":\s*\d+`))
```

| Matcher | Matches when |
|---|---|
| `Equals(v)` | the field equals `v` |
| `DeepEquals(v)` | the whole object corresponds — no extra keys |
| `Contains(v)` | the field contains `v` |
| `StartsWith(v)` / `EndsWith(v)` | prefix / suffix |
| `Matches(re)` | the regular expression matches |
| `Exists(bool)` | the field is present / absent |

Modifiers chain onto any matcher:

```go
rift.Contains("json").CaseSensitive(true)
rift.Equals("x").Except(`\s+`)          // strip before comparing
rift.Equals(hdrs).KeyCaseSensitive(true) // object *keys*
```

### Fields sharing an operator collapse

```go
rift.OnGet("/x")   // → {"equals": {"method": "GET", "path": "/x"}}
```

Not two separate `equals` predicates. They would AND identically, but the merged form is what the
engine and the conformance fixtures use — and it is what a `_verify` comparison expects.

Matchers with **different parameters** are *not* merged, because case sensitivity is a property of
the predicate rather than the field; merging would silently change the other field's semantics.

### Composites and selectors

```go
rift.OnAny().WithPredicate(
	rift.Or(
		rift.PredicateOn("path", rift.Equals("/a")),
		rift.PredicateOn("path", rift.Equals("/b")),
	),
	rift.Not(rift.PredicateOn("method", rift.Equals("DELETE"))),
	rift.PredicateOn("body", rift.Equals("42")).WithJSONPath("$.count"),
)
```

`rift.Inject(js)` builds a predicate evaluated by JavaScript. It requires the engine started with
`--allowInjection` over the admin API; the embedded C ABI accepts it unconditionally, because the
in-process embedder is already trusted.

## Responses

```go
rift.OK()                        // 200, no body
rift.OKText("pong")              // 200 text/plain
rift.OKJSON(map[string]any{…})   // 200 application/json
rift.Status(503)
rift.Created("/orders/1")        // 201 + Location
rift.NoContent()                 // 204
rift.NotFound()                  // 404
```

### Response cycling

Call `Return` repeatedly. The engine walks the cycle and wraps around:

```go
rift.OnGet("/flaky").
	Return(rift.Status(503).Repeat(2)).   // twice
	Return(rift.OKText("ok"))             // then this
```

### Behaviours

```go
rift.OKJSON(body).
	After(250 * time.Millisecond).        // fixed delay; AfterBetween(min, max) for a random one
	Repeat(3).
	Decorate(`(cfg) => { cfg.response.headers['X-Seen'] = '1' }`).
	Templated()
```

`Copy`, `Lookup` and `ShellTransform` pass their engine config through verbatim.

#### Evaluation order is the engine's, not the call order

The builders write the object form, `_behaviors`, and the engine runs it in a fixed order:
**wait, lookup, copy, shellTransform, decorate**. That is Mountebank's order, and Rift follows it
from engine 0.18.0. Earlier engines ran wait, copy, lookup, decorate, shellTransform.

So a chain such as `.Decorate(js).ShellTransform(cmd)` runs `shellTransform` first, whatever order
you called the methods in. And `lookup` runs before `copy`, so text that `copy` puts into the
response is never scanned for lookup tokens.

When you need a different order, use the array form, `behaviors`: a list the engine runs element by
element, in order. The builders do not produce it, but the model carries it through `Extra`, so
either of these works:

```go
resp := rift.OKText("…").Build()
resp.Extra = map[string]rift.JSON{"behaviors": []rift.JSON{
	map[string]rift.JSON{"decorate": js},
	map[string]rift.JSON{"shellTransform": cmd},
}}

// or write the response as JSON
resp, err := rift.ResponseFromJSON([]byte(`{"is":{…},"behaviors":[{"decorate":"…"},{"shellTransform":"…"}]}`))
```

`GET /imposters` writes behaviors back in Mountebank's grammar: `repeat` on the response, and a
one-item list as a bare value. A config read back from the engine can therefore differ textually
from the one you sent while meaning the same thing.

### Faults and proxies

```go
rift.Fault("CONNECTION_RESET_BY_PEER")   // connection-level failure, no response

rift.Proxy("http://upstream:8080").
	Once().                               // record the first response, replay it after
	InjectHeader("X-Via", "rift").
	RewritePath("^/api", "/v2")
```

Fault names are passed through as strings, so a newer engine's fault works without an SDK release.

A proxy response takes the same behaviors as a canned one. They run on the upstream's response
before it reaches the client and before it is recorded, as Mountebank does:

```go
rift.Proxy("http://upstream:8080").Once().
	After(500 * time.Millisecond)         // delays the live first call; the recorded replay has no delay
```

That needs engine 0.18.0 or later; older engines ignore behaviors on a proxy response. The stub a
recording generates holds the transformed response, not the behaviors, so nothing runs twice.

A fault takes only `Repeat`. Any other behavior on a fault is kept but never runs, and engines
from 0.18.0 on report it as a `config_key_ignored` warning.

`AfterBetween(min, max)` needs `min` no greater than `max`. Engines from 0.18.0 refuse the imposter
otherwise; earlier ones accepted it and then failed every request.

## HTTPS and client certificates

`HTTPS(certPEM, keyPEM)` serves TLS with your certificate; empty strings select the engine's
self-signed default. `RequireClientCert` makes the imposter demand a client certificate at the
handshake:

```go
rift.NewImposter("bank").
	HTTPS(serverCert, serverKey).
	RequireClientCert(clientCA).        // the certificate must chain to clientCA
	Stub(rift.OnAny().Return(rift.OK()))

rift.NewImposter("any-client").RequireClientCert()   // any certificate will do
```

With one or more CA PEMs the client certificate must chain to one of them. With none, any
certificate the client holds is accepted, but a client that presents nothing still fails the
handshake. `RequireClientCert` sets the protocol to `https` unless you set one.

!!! warning "Engine 0.18.0 enforces this, earlier engines ignored it"
    Before 0.18.0 the engine dropped `mutualAuth`, `rejectUnauthorized` and `ca`, and accepted every
    client. From 0.18.0 they are enforced. A test that used `MutualAuth()` and connected without a
    client certificate passed before and fails now, and `MutualAuth()` on a non-HTTPS imposter is
    refused. `MutualAuth()` is deprecated in favour of `RequireClientCert`.

## Scenarios

A stub can gate on and advance a named state machine:

```go
rift.NewImposter("retry").
	Stub(rift.OnGet("/x").InScenario("s").RequireState("Started").
		SetState("failed-once").Return(rift.Status(503))).
	Stub(rift.OnGet("/x").InScenario("s").RequireState("failed-once").
		Return(rift.OKText("ok")))
```

## Spaces

A space is a per-flow overlay on a shared imposter, so parallel shards can hit one port and stay
isolated, partitioned by flow id:

```go
eng.SpaceAddStub(ctx, port, "flow-a", rift.OnGet("/who").Return(rift.OKText("a")))
eng.SpaceAddStub(ctx, port, "flow-b", rift.OnGet("/who").Return(rift.OKText("b")))
```

## Keys the engine accepts but ignores

Three keys the model carries have no effect on a Rift engine. They are kept so a Mountebank config
round-trips unchanged:

| Key | Why it does nothing | Use instead |
|---|---|---|
| `recordMatches` (`RecordMatches()`) | Rift does not record per-stub matches | `Record()` and the request journal |
| `_rift.metrics` | metrics are process-wide | the metrics port (`ServeOptions.MetricsPort`, or `--metrics-port`) |
| `_rift.proxy` | a proxy response's upstream is its own `proxy.to` | the `Proxy(...)` response |

Engines from 0.18.0 on report each one as a `config_key_ignored` warning. On the embedded engine it
appears in `StubWarnings`, and on a remote one in `_rift.warnings` on `GET /imposters/:port`.
`RecordMatches()` is deprecated.

## The escape hatch

For a config that predates the DSL, is generated elsewhere, or exercises a corner of the grammar
the builders do not model yet:

```go
raw := []byte(`{"port":4545,"stubs":[…],"someFutureKey":true}`)
imp, err := rift.ImposterFromJSON(raw)

// or the bulk envelope
cfg, err := rift.ImpostersFromJSON(raw)
```

Every open struct carries an `Extra` map, so **unknown keys survive an unmarshal/marshal
round-trip untouched**. A config built for a newer engine passes through this SDK unchanged — which
is exactly what the [conformance gate](conformance.md) asserts.

You can also mix: parse a document, adjust it with typed fields, and send it.

```go
imp.Port = 0                     // let the engine assign
imp.RecordRequests = true
```

Declared fields always win over a colliding `Extra` entry, so an escape-hatch value can never
silently overwrite typed state.
