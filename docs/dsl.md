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
	After(250 * time.Millisecond).        // fixed delay
	AfterBetween(50*time.Millisecond, 1*time.Second).
	Repeat(3).
	Decorate(`(cfg) => { cfg.response.headers['X-Seen'] = '1' }`).
	Templated()
```

`Copy`, `Lookup` and `ShellTransform` pass their engine config through verbatim.

### Faults and proxies

```go
rift.Fault("CONNECTION_RESET_BY_PEER")   // connection-level failure, no response

rift.Proxy("http://upstream:8080").
	Once().                               // record the first response, replay it after
	InjectHeader("X-Via", "rift").
	RewritePath("^/api", "/v2")
```

Fault names are passed through as strings, so a newer engine's fault works without an SDK release.

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
