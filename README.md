# rift-go

Official Go SDK for [Rift](https://github.com/achird-labs/rift) — a high-performance,
Mountebank-compatible HTTP/HTTPS mock server written in Rust.

```go
func TestUserLookup(t *testing.T) {
	users := rifttest.Imposter(t, rift.NewImposter("users").
		Stub(rift.OnGet("/api/users/1").
			Return(rift.OKJSON(map[string]rift.JSON{"id": 1, "name": "Alice"}))))

	resp := callSUT(t, users.BaseURL())

	rifttest.AssertReceived(t, users, rift.OnGet("/api/users/1"), rift.Once())
}
```

## Install

```sh
go get github.com/achird-labs/rift-go
```

## No cgo

The engine runs **in your test process**, loaded through
[purego](https://github.com/ebitengine/purego) rather than cgo. purego resolves symbols through
the dynamic linker at runtime, so:

- `CGO_ENABLED=0` keeps working
- no C toolchain, and cross-compilation is unaffected
- your users' builds are unchanged by depending on this

There is no Docker daemon in the loop, no image pull, no port negotiation, and no health-check
wait. Starting a mock is a function call.

## Packages

| Package | Contents |
|---|---|
| `rift` | typed wire model, chainable builders, `Connect` (admin API) and `Spawn` (managed binary) transports |
| `riftembed` | in-process engine over the C ABI, plus TLS-MITM intercept |
| `rifttest` | `testing.T` helpers: shared engine, `t.Cleanup` teardown, request assertions |
| `riftfetch` + `cmd/rift-fetch` | download and SHA-256-verify the native library |
| `conformance` | replays the SDK conformance corpus — the DSL↔engine parity gate |

## Transports

All three implement `rift.Client`, so a suite written against one runs against the others:

```go
// In-process. No binary, no port, no cleanup.
eng, err := riftembed.Start(riftembed.Options{})

// An engine already running somewhere.
eng, err := rift.Connect("http://localhost:2525", rift.RemoteOptions{})

// A binary this process manages. ctx bounds startup; Close stops it.
eng, err := rift.Spawn(ctx, rift.SpawnOptions{})
```

### Finding the native library

`riftembed` looks, in order: an explicit `Options.LibraryPath`, `$RIFT_FFI_LIB`, the on-disk
cache, then `./natives`. It will **not** download anything implicitly — a test run that quietly
reaches out to the network is a bad default, and explicit fetching is what makes CI and
air-gapped hosts predictable.

`riftembed.LibraryPath()` reports what it would load, and its error lists every path tried.

To populate the cache:

```sh
go run github.com/achird-labs/rift-go/cmd/rift-fetch@latest -version v0.1.0
```

Every download is verified against the SHA-256 the release manifest publishes, and there is no
flag to skip it — an unverified shared library is one you are about to load into your own
process. A mismatch installs nothing.

```sh
rift-fetch -version v0.1.0 -platform linux-x86_64-musl   # Alpine: Go cannot detect musl
RIFT_RELEASE_BASE=https://mirror.internal/rift \
  rift-fetch -version v0.1.0                             # air-gapped mirror
```

## The DSL

Builders produce the wire model; nothing needs raw JSON, and `rift.ImposterFromJSON` is there for
grammar the builders do not cover yet.

```go
imp := rift.NewImposter("orders").Record().
	Stub(rift.OnPost("/orders").
		WithHeader("Content-Type", rift.Contains("json")).
		WithBody(rift.Matches(`"total":\s*\d+`)).
		Return(rift.Created("/orders/1")).
		Return(rift.Status(409))).            // repeated Return cycles responses
	Stub(rift.OnGet("/orders/1").
		Return(rift.OKJSON(order).After(50 * time.Millisecond))).
	Stub(rift.OnAny().Return(rift.Status(404)))
```

**Stub order decides the winner** — the engine serves the first stub whose predicates match, so a
catch-all belongs last. `Handle.AddStubAt(0, ...)` puts a new stub in front of an existing one.

Fields sharing an operator collapse into one predicate, which is the shape the engine uses:

```go
rift.OnGet("/x")   // → {"equals": {"method": "GET", "path": "/x"}}
```

## Assertions

Counting happens **in the engine**, through the same predicate evaluator that serves requests — so
an assertion means exactly what an equivalent stub would have matched, including `xpath`,
`jsonpath` and `inject` predicates that a client-side reimplementation could not honour.

```go
rifttest.AssertReceived(t, h, rift.OnGet("/health"), rift.Times(3))
rifttest.AssertReceived(t, h, rift.OnPost("/orders"), rift.AtLeast(1))
rifttest.AssertNotReceived(t, h, rift.OnDelete("/orders/1"))
```

A failure prints the near miss the engine identified — which clauses failed and the request's
actual values — not just a count.

## Mocking a dependency you cannot repoint

When the system under test hard-codes an HTTPS host, intercept it. The SDK hands back a client
that already trusts the CA and already routes through the proxy:

```go
ic, _ := eng.StartIntercept(ctx, riftembed.InterceptOptions{})
defer ic.Stop(ctx)

ic.AddRules(ctx, riftembed.InterceptForward("cdn.example.com", imposterPort))

client, _ := ic.HTTPClient(ctx)   // trusts the CA, proxies through the listener
sut.SetHTTPClient(client)
```

For a JVM or other non-Go system under test, `ic.ExportTruststore(ctx, "jks", pw, path)` writes a
truststore instead.

## Requirements

- Go 1.24+
- The Rift native library, for the embedded transport (see above)
- macOS, Linux, FreeBSD or Windows. Elsewhere, use `Connect` or `Spawn`.

## Conformance

This SDK replays the engine's [SDK conformance corpus](https://github.com/achird-labs/rift/tree/master/sdk-conformance)
on every transport. Two gates:

1. **DSL-expressibility** — every fixture is reconstructed through the typed model and must
   serialize back to a document deep-equal to the fixture. A fixture the DSL cannot express means
   the DSL has drifted from the engine grammar, and fails the build.
2. **Serve & replay** — every fixture is created and its `_verify` transcripts are asserted.

```sh
RIFT_CORPUS_DIR=/path/to/sdk-conformance go test ./conformance/...
```

## Status

Milestone M5 is feature-complete: wire model and DSL, all three transports, the full C-ABI
surface, `rifttest`, intercept, `rift-fetch`, and corpus conformance on the embedded and remote
lanes.

Not yet: a published release tag. Until one exists, depend on this by commit rather than version.

## Licence

Apache-2.0.
