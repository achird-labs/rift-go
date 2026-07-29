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

## Build info and capabilities

```go
info, _ := eng.BuildInfo()
// {"version":"0.16.0","commit":"…","features":["redis-backend","javascript"],
//  "serveOptions":["host","port","apiKey",…]}
```

`serveOptions` is the supported way to feature-detect. The **absence** of a key means an engine
too old to accept it — a rejection only ever comes from an engine that already knows the field, so
you cannot detect a new capability by watching for an error.

## Unsupported platforms

`riftembed` requires a platform purego can load libraries on: macOS, Linux, FreeBSD or Windows.
Anywhere else, `Start` returns `rift.ErrEngineUnavailable` and you should use
[`Connect` or `Spawn`](transports.md), which need no native library.
