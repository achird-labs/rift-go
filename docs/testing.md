# Testing with rifttest

`rifttest` wires Rift into `go test`. It optimises for the common case: a test needs a mock API,
wants it torn down automatically, and wants a failed assertion to say what actually arrived.

```go
func TestUserLookup(t *testing.T) {
	users := rifttest.Imposter(t, rift.NewImposter("users").
		Stub(rift.OnGet("/api/users/1").
			Return(rift.OKJSON(map[string]rift.JSON{"id": 1}))))

	callSUT(t, users.BaseURL())

	rifttest.AssertReceived(t, users, rift.OnGet("/api/users/1"), rift.Once())
}
```

No `defer`, no cleanup, no port bookkeeping. The imposter is destroyed at `t.Cleanup`.

## One engine per test binary

Starting an engine costs a runtime and its threads. Doing that per test would dominate a large
suite, so the engine is created **once, lazily, and shared**.

Isolation comes from each test getting its **own imposter on its own port**, not from a fresh
engine. Two imposters in one binary cannot see each other's traffic.

```go
a := rifttest.Imposter(t, rift.NewImposter("a")…)
b := rifttest.Imposter(t, rift.NewImposter("b")…)
// a.Port() != b.Port(); their journals are independent
```

## Recording is on by default

`rifttest.Imposter` enables request recording unless the definition already set it. An imposter you
cannot assert against is rarely what a test wants, and forgetting `.Record()` produces a confusing
empty journal rather than an error.

## Assertions

Counting happens **in the engine**, through the same predicate evaluator that serves requests. An
assertion therefore means exactly what an equivalent stub would have matched — including `xpath`,
`jsonpath` and `inject` predicates, which a client-side reimplementation could not honour.

```go
rifttest.AssertReceived(t, h, rift.OnGet("/health"), rift.Times(3))
rifttest.AssertReceived(t, h, rift.OnPost("/orders"), rift.AtLeast(1))
rifttest.AssertReceived(t, h, rift.OnGet("/x"), rift.Between(1, 5))
rifttest.AssertNotReceived(t, h, rift.OnDelete("/orders/1"))
```

Count matchers: `Times(n)`, `Once()`, `Never()`, `AtLeast(n)`, `AtMost(n)`, `Between(lo, hi)`.

### Failure output

A failure prints the near miss the engine identified — which clauses failed, and the request's
actual values — not just a count:

```
rifttest: imposter "users" (port 4545): expected exactly 1 matching request(s), got 0 of 2 recorded

  expected a request matching:
    {"equals":{"method":"GET","path":"/api/users/2"}}

  closest recorded request:
    GET /api/users/1 headers=map[Accept:application/json]

  it failed these clauses:
    wanted {"equals":{"method":"GET","path":"/api/users/2"}}
      actual: map[path:/api/users/1]

  journal:
    GET /api/users/1 headers=map[Accept:application/json]
    GET /health
```

An empty journal says so explicitly, and points at the usual cause — the system under test not
being pointed at `Handle.BaseURL()`.

## The handle

```go
h.Port()                       // uint16
h.BaseURL()                    // http://localhost:4545
h.URL("/api/users/1")          // joined
h.Client()                     // the underlying rift.Client
h.Recorded()                   // []rift.RecordedRequest
h.ClearRecorded()              // reset, so a later assertion counts only what follows
h.AddStub(stub)                // append to a live imposter
h.AddStubAt(0, stub)           // insert in front of an existing catch-all
h.SetScenarioState("s", "done")
```

## Running against another transport

The tests do not change; only where the engine comes from:

```go
func TestMain(m *testing.M) {
	eng, _ := rift.Connect("http://localhost:2525", rift.RemoteOptions{})
	rifttest.Engine(nil, rifttest.Options{Client: eng})
	code := m.Run()
	rifttest.Close()
	os.Exit(code)
}
```

!!! note "When to call `rifttest.Close`"
    The default in-process engine does not need it — the test binary's exit releases everything.
    Call it from `TestMain` when the engine owns a **child process or a remote connection** that
    should not outlive the suite.

## Staying green without a native library

```go
rifttest.Engine(t, rifttest.Options{SkipIfUnavailable: true})
```

Skips instead of failing when no engine can be started — useful for a suite that should stay green
on a contributor's machine while remaining required in CI. See [Native library & CI](natives.md).
