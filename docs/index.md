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

No Docker daemon, no image pull, no port negotiation, no health-check wait. Starting a mock is a
function call.

## Install

```sh
go get github.com/achird-labs/rift-go
```

Then fetch the native library once (see [Native library & CI](natives.md)):

```sh
go run github.com/achird-labs/rift-go/cmd/rift-fetch@latest -version v0.17.0
```

## Why in-process

The engine runs **inside your test binary**, loaded through
[purego](https://github.com/ebitengine/purego) rather than cgo. purego resolves symbols through
the dynamic linker at runtime, which means:

- `CGO_ENABLED=0` keeps working
- no C toolchain, and cross-compilation is unaffected
- your users' builds are unchanged by depending on this

See [Embedded engine](embedded.md) for what that costs and what it buys.

## Packages

| Package | Contents |
|---|---|
| [`rift`](dsl.md) | typed wire model, chainable builders, typed errors, `Connect` and `Spawn` transports |
| [`riftembed`](embedded.md) | in-process engine over the C ABI, plus [TLS-MITM intercept](intercept.md) |
| [`rifttest`](testing.md) | `testing.T` helpers — shared engine, `t.Cleanup` teardown, near-miss assertions |
| [`riftfetch`](natives.md) | download and SHA-256-verify the native library |
| [`conformance`](conformance.md) | replays the SDK conformance corpus — the DSL↔engine parity gate |

## Three transports, one interface

All three implement `rift.Client`, so a suite written against one runs against the others without
changing a line of test code:

```go
// In-process. No binary, no port, no cleanup.
eng, err := riftembed.Start(riftembed.Options{})

// An engine already running somewhere.
eng, err := rift.Connect("http://localhost:2525", rift.RemoteOptions{})

// A binary this process manages. ctx bounds startup; Close stops it.
eng, err := rift.Spawn(ctx, rift.SpawnOptions{})
```

[Transports →](transports.md)

## Requirements

- **Go 1.24+**
- The Rift native library, for the embedded transport
- **macOS, Linux, FreeBSD or Windows.** Elsewhere, use `Connect` or `Spawn` — they need no native
  library at all.

## Where to go next

- **Writing your first mock** → [Building imposters](dsl.md)
- **Wiring it into `go test`** → [Testing with rifttest](testing.md)
- **Mocking an HTTPS host you cannot repoint** → [Intercept](intercept.md)
- **Setting up CI or an air-gapped host** → [Native library & CI](natives.md)
- **Something failed and the error is unclear** → [Errors](errors.md)

## Licence

Apache-2.0.
