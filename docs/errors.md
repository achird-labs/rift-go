# Errors

Every error the SDK returns wraps a sentinel, so callers branch on kind without matching strings.

```go
if errors.Is(err, rift.ErrImposterNotFound) {
	// …
}
```

## Sentinels

| Sentinel | Means |
|---|---|
| `ErrEngineUnavailable` | the engine could not be reached, started, or loaded — a refused admin connection, a spawn failure, a missing native library |
| `ErrInvalidDefinition` | a definition was rejected before it reached the engine, or could not be encoded/decoded |
| `ErrImposterNotFound` | the addressed imposter does not exist |
| `ErrVersionMismatch` | the loaded library reports a C-ABI version this SDK does not support |
| `ErrClosed` | the engine or handle has already been stopped |
| `ErrVerificationFailed` | a verification's match count did not meet expectations |

The distinction that matters most is **`ErrEngineUnavailable` vs `ErrInvalidDefinition`**: the
engine was never reached, versus the engine understood the request and said no. They call for
completely different responses, and conflating them sends people to debug the wrong layer.

## EngineError

Failures the engine itself reported carry its own detail:

```go
var engErr *rift.EngineError
if errors.As(err, &engErr) {
	engErr.Code      // HTTP status for remote transports; 0 for the embedded lane
	engErr.Message   // the engine's own description
	engErr.Op        // the SDK operation, e.g. "create imposter"
}
```

`EngineError` also unwraps to a sentinel, so `errors.Is(err, rift.ErrImposterNotFound)` works on one
regardless of which transport produced it.

## Verification failures

`rifttest.AssertReceived` reports through `t.Errorf` with the full diagnostic. When you need the
data programmatically:

```go
res, err := client.Verify(ctx, port, rift.VerifyRequest{
	Predicates:     rift.OnGet("/x").Build().Predicates,
	IncludeClosest: true,
})
res.Matched          // count satisfying every predicate
res.Total            // recorded requests in scope
res.Closest.Request  // the nearest non-match
res.Closest.FailedPredicates // which clauses it failed, and the actual values
```

## Common failures

### `no Rift native library found`

The embedded transport cannot find the shared library. The error lists every path tried. Run
`rift-fetch`, or set `RIFT_FFI_LIB`. See [Native library & CI](natives.md).

### `library … reports C-ABI vN, this SDK requires v2`

The installed library belongs to a different ABI generation. Fetch the version matching your SDK
release rather than skipping the check.

### `engine did not become ready within 20s`

`Spawn` timed out. If the child **died**, the error says so instead — that distinction is
deliberate, because a crash-on-startup otherwise reads as a slow machine.

### Connection refused right after `Spawn` succeeded

Almost always the context lifetime trap. `Spawn`'s `ctx` bounds **startup only**; the engine runs
until `Close`. If you are seeing this, check whether something else is closing the returned
`Remote` early. See [Transports](transports.md#spawn-a-managed-child-process).

### A stub never matches

Two usual causes:

1. **Order.** The engine serves the first stub whose predicates match, so a catch-all earlier in
   the list wins. See [Building imposters](dsl.md#stub-order-decides-the-winner).
2. **An appended stub behind a catch-all** is unreachable — use `AddStubAt(0, …)`.

Turn on `IncludeClosest` (or just use `rifttest.AssertReceived`, which does) and the engine will
tell you which clause failed and what the request actually carried.

### An empty journal

The imposter was created without recording. `rifttest.Imposter` turns it on automatically;
`NewImposter(…)` alone does not — add `.Record()`.
