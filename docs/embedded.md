# Embedded engine (no cgo)

`riftembed` runs a Rift engine inside your process: a Tokio runtime on its own threads, the
matching engine it drives, and optionally an admin plane served over it.

```go
eng, err := riftembed.Start(riftembed.Options{})
if err != nil {
	t.Fatal(err)
}
defer eng.Close()

port, err := eng.CreateImposter(ctx, rift.NewImposter("users").Record().
	Stub(rift.OnGet("/health").Return(rift.OKText("ok"))))
```

## Why purego and not cgo

cgo would have been simpler to write, and it would have poisoned the build for every consumer
downstream: a C toolchain requirement, slower builds, cross-compilation pain, and larger CI
images — costs paid by people who never asked for them.

[purego](https://github.com/ebitengine/purego) resolves symbols through the dynamic linker at
runtime instead. You give up some ergonomics at the boundary — no automatic struct marshalling,
more manual work — and in exchange a Go user adds a dependency and **nothing about their build
changes**.

!!! note "The claim, precisely"
    `CGO_ENABLED=0 go test ./...` passes, including the tests that load the shared library and
    serve real HTTP. This is verified in CI on Linux, macOS and Windows.

The engine's ABI is deliberately all `char*` and integers, which is exactly the subset purego
handles well. That is not an accident — it is what makes a binding like this possible at all.

## Thread affinity

The engine reports failures through `rift_last_error`, which is **thread-local**. A Go goroutine
may migrate between OS threads at any call boundary, so a downcall on one thread followed by an
error read on another would read an empty error — intermittently, under load, in a way that looks
like the engine silently succeeded.

Every call in this package therefore pins its goroutine with `runtime.LockOSThread` for the
duration of the *(downcall, error-read)* pair. You do not need to do anything about this; it is
noted because it is the kind of thing that looks like an unnecessary cost until it isn't.

## Finding the native library

`riftembed` looks, in order:

1. an explicit `Options.LibraryPath`
2. `$RIFT_FFI_LIB`
3. the on-disk cache (`riftembed.CachePath()`)
4. `./natives`, `./natives/<goos>-<goarch>`, and the working directory

It will **not** download anything implicitly. A test run that quietly reaches out to the network
is a bad default, and being explicit is what makes CI and air-gapped hosts predictable. See
[Native library & CI](natives.md).

```go
// Report what would be loaded, without loading it. The error lists every path tried.
path, err := riftembed.LibraryPath()
```

## Version checking

`Start` verifies the library's C-ABI version before doing anything else. A mismatch means the
library and the SDK disagree about symbol signatures and ownership, which is not something either
side can paper over at runtime:

```go
eng, err := riftembed.Start(riftembed.Options{})
if errors.Is(err, rift.ErrVersionMismatch) {
	// the installed library is for a different ABI generation
}
```

`Options.SkipVersionCheck` exists for deliberately testing against a pre-release library. Using it
otherwise is undefined behaviour, not a warning.

The same sentinel reports a serve option the loaded engine does not accept; see
[Serve options](#serve-options).

## Lifecycle

`Engine` is safe for concurrent use. `Close` is idempotent, and calls after it fail cleanly with
`rift.ErrClosed` rather than using a freed handle:

```go
eng.Close()
_, err := eng.CreateImposter(ctx, imp)   // errors.Is(err, rift.ErrClosed)
```

## Contexts

Every method takes a `context.Context`. The embedded lane checks it **before** the downcall and
cannot interrupt one already in flight — an FFI call is synchronous. That is documented rather
than pretended otherwise; honouring cancellation at the boundary is the honest amount of support
to offer, and it is enough for the case that matters: a cancelled test not queueing more work
against an engine it is about to close.

## Serving the admin API

`ServeAdmin` puts the engine's HTTP admin API on a port, for tools that talk to it over HTTP:

```go
raw, err := eng.ServeAdmin(ctx, riftembed.ServeOptions{APIKey: token})
// {"adminPort":49321,"adminUrl":"http://127.0.0.1:49321","metricsPort":null}
```

With `APIKey` set, clients must send it as the raw `Authorization` header value, which is what
`rift.Connect` with `RemoteOptions.APIKey` does. Leave it empty to run unauthenticated. A
whitespace-only key is refused before the engine is called, and engines from 0.17.0 on refuse one
too: it would switch the auth gate on and then admit every request.

## Build info and capabilities

```go
info, _ := eng.BuildInfo()
info.Version                           // "0.18.0"
info.Features                          // compiled features: ["redis-backend", "javascript"]
info.SupportsServeOption("noParse")    // true on 0.18.0, false on 0.17.0
```

`ServeOptions` is the supported way to feature-detect. The **absence** of a key means an engine
too old to accept it. A rejection only ever comes from an engine that already knows the field, so
you cannot detect a new capability by watching for an error: an older engine silently ignores it.

`ServeAdmin` applies this check itself. An option the loaded engine does not advertise fails with
`rift.ErrVersionMismatch` before the engine is called. That matters most for `RequireAdminAuth`,
which an engine older than 0.17.0 would drop and then serve an open admin plane. Engines older than
0.17.0 publish no list; on them only the seven original options count as supported.

## Serve options

| Field | Engine | Effect |
|---|---|---|
| `Host`, `Port` | all | Admin API bind address. `Host` must be an IP literal. |
| `APIKey` | all | Clients send it as the raw `Authorization` header. |
| `MetricsPort` | all | Serve Prometheus metrics on this port, on the same host. |
| `ConfigFile` | all | Load imposters from a JSON or YAML file; `POST /admin/reload` re-reads it. |
| `Config` | all | Apply an imposters document at serve time. |
| `AllowInjection` | all | Admit inject and script imposters arriving through the admin plane or `ConfigFile`. |
| `RequireAdminAuth` | 0.17.0 | Refuse an off-host admin plane with no `APIKey`; from 0.18.0 it also governs `StartIntercept`. |

Imposters this process hands the engine directly (`CreateImposter`, `ApplyConfig` and
`ServeOptions.Config`) are never gated by `AllowInjection`: the embedding process can already run
code here.

## Unsupported platforms

`riftembed` requires a platform purego can load libraries on: macOS, Linux, FreeBSD or Windows.
Anywhere else, `Start` returns `rift.ErrEngineUnavailable` and you should use
[`Connect` or `Spawn`](transports.md), which need no native library.
